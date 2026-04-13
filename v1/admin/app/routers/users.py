"""User management and Vault MCP credential management — admin only."""

import logging
import uuid

import httpx
from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from aviary_shared.db.models import User, TeamMember, Team
from aviary_shared.vault import VaultClient
from app.config import settings
from app.db import get_db
from app.services import keycloak_client

_vault_client: VaultClient | None = None


def _vault() -> VaultClient:
    global _vault_client
    if _vault_client is None:
        _vault_client = VaultClient(settings.vault_addr, settings.vault_token)
    return _vault_client

logger = logging.getLogger(__name__)

router = APIRouter()


class UserResponse(BaseModel):
    id: str
    external_id: str
    email: str
    display_name: str
    is_platform_admin: bool
    teams: list[str]
    created_at: str


class UserCreateRequest(BaseModel):
    email: str
    display_name: str
    password: str
    groups: list[str] = []
    is_platform_admin: bool = False


class VaultKeyResponse(BaseModel):
    key: str
    value: str


class VaultKeyRequest(BaseModel):
    key: str
    value: str


async def _sync_keycloak_users(db: AsyncSession) -> None:
    """Sync all Keycloak users into the Aviary DB.

    Ensures users who exist in Keycloak but haven't logged in yet
    are visible in the admin console.
    """
    try:
        kc_users = await keycloak_client.list_users()
    except httpx.HTTPError:
        logger.warning("Failed to fetch Keycloak users", exc_info=True)
        return

    for kc_user in kc_users:
        kc_id = kc_user["id"]
        email = kc_user.get("email", "")
        if not email:
            continue

        result = await db.execute(select(User).where(User.external_id == kc_id))
        user = result.scalar_one_or_none()

        display_name = " ".join(filter(None, [
            kc_user.get("firstName", ""), kc_user.get("lastName", ""),
        ])) or email

        if user is None:
            user = User(
                external_id=kc_id, email=email, display_name=display_name,
            )
            db.add(user)
            await db.flush()

        # Sync groups
        try:
            kc_groups = await keycloak_client.get_user_groups(kc_id)
        except httpx.HTTPError:  # Best-effort: group lookup failure is non-critical
            kc_groups = []

        for group_name in kc_groups:
            result = await db.execute(select(Team).where(Team.name == group_name))
            team = result.scalar_one_or_none()
            if team is None:
                team = Team(name=group_name)
                db.add(team)
                await db.flush()

            result = await db.execute(
                select(TeamMember).where(
                    TeamMember.team_id == team.id, TeamMember.user_id == user.id,
                )
            )
            if result.scalar_one_or_none() is None:
                db.add(TeamMember(team_id=team.id, user_id=user.id))

    await db.flush()


@router.get("", response_model=list[UserResponse])
async def list_users(db: AsyncSession = Depends(get_db)):
    """List all users. Syncs from Keycloak first to catch users who haven't logged in yet."""
    await _sync_keycloak_users(db)

    result = await db.execute(select(User).order_by(User.created_at.desc()))
    users = result.scalars().all()

    responses = []
    for u in users:
        # Get teams
        result = await db.execute(
            select(Team.name)
            .join(TeamMember, TeamMember.team_id == Team.id)
            .where(TeamMember.user_id == u.id)
        )
        teams = list(result.scalars().all())

        responses.append(UserResponse(
            id=str(u.id),
            external_id=u.external_id,
            email=u.email,
            display_name=u.display_name,
            is_platform_admin=u.is_platform_admin,
            teams=teams,
            created_at=u.created_at.isoformat(),
        ))
    return responses


@router.post("", response_model=UserResponse, status_code=201)
async def create_user(body: UserCreateRequest, db: AsyncSession = Depends(get_db)):
    """Create a user in Keycloak and register in Aviary DB."""
    # Create in Keycloak
    try:
        kc_user_id = await keycloak_client.create_user(
            body.email, body.display_name, body.password, body.groups,
        )
    except HTTPException:
        raise
    except httpx.HTTPError as e:
        raise HTTPException(status_code=502, detail=f"Keycloak error: {e}")

    # Upsert in DB
    result = await db.execute(select(User).where(User.external_id == kc_user_id))
    user = result.scalar_one_or_none()
    if user is None:
        user = User(
            external_id=kc_user_id,
            email=body.email,
            display_name=body.display_name,
            is_platform_admin=body.is_platform_admin,
        )
        db.add(user)
    else:
        user.email = body.email
        user.display_name = body.display_name
        user.is_platform_admin = body.is_platform_admin

    await db.flush()

    # Sync groups to teams
    for group_name in body.groups:
        result = await db.execute(select(Team).where(Team.name == group_name))
        team = result.scalar_one_or_none()
        if team is None:
            team = Team(name=group_name)
            db.add(team)
            await db.flush()

        result = await db.execute(
            select(TeamMember).where(
                TeamMember.team_id == team.id, TeamMember.user_id == user.id,
            )
        )
        if result.scalar_one_or_none() is None:
            db.add(TeamMember(team_id=team.id, user_id=user.id))

    await db.flush()
    await db.refresh(user)

    return UserResponse(
        id=str(user.id),
        external_id=user.external_id,
        email=user.email,
        display_name=user.display_name,
        is_platform_admin=user.is_platform_admin,
        teams=body.groups,
        created_at=user.created_at.isoformat(),
    )


@router.get("/{user_id}/credentials", response_model=list[VaultKeyResponse])
async def list_user_credentials(user_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    """List all MCP credentials stored in Vault for a user."""
    result = await db.execute(select(User).where(User.id == user_id))
    user = result.scalar_one_or_none()
    if not user:
        raise HTTPException(status_code=404, detail="User not found")

    keys = await _vault().list_user_credentials(user.external_id)
    credentials = []
    for key in keys:
        token = await _vault().read_user_credential(user.external_id, key)
        if token is None:
            continue
        masked = token[:8] + "..." + token[-4:] if len(token) > 16 else "***"
        credentials.append(VaultKeyResponse(key=key, value=masked))
    return credentials


@router.put("/{user_id}/credentials")
async def set_user_credential(
    user_id: uuid.UUID, body: VaultKeyRequest, db: AsyncSession = Depends(get_db),
):
    """Create or update an MCP credential in Vault for a user."""
    result = await db.execute(select(User).where(User.id == user_id))
    user = result.scalar_one_or_none()
    if not user:
        raise HTTPException(status_code=404, detail="User not found")

    await _vault().write_user_credential(user.external_id, body.key, body.value)
    return {"status": "ok", "key": body.key}


@router.delete("/{user_id}/credentials/{key}", status_code=204)
async def delete_user_credential(
    user_id: uuid.UUID, key: str, db: AsyncSession = Depends(get_db),
):
    """Delete an MCP credential from Vault for a user."""
    result = await db.execute(select(User).where(User.id == user_id))
    user = result.scalar_one_or_none()
    if not user:
        raise HTTPException(status_code=404, detail="User not found")

    await _vault().delete_user_credential(user.external_id, key)
    return None
