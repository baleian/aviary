"""Per-user credential management — names go to client, values stay in Vault.

Path: ``aviary/credentials/{user.external_id}/{name}`` (KV v2 under ``secret/``).
The LiteLLM hook reads ``anthropic-api-key`` from this prefix on every request.
"""

from __future__ import annotations

import logging
import re

import httpx
from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from app.auth.dependencies import get_current_user
from app.services import vault_service
from aviary_shared.db.models import User

logger = logging.getLogger(__name__)

router = APIRouter()

NAME_RE = re.compile(r"^[a-zA-Z0-9._\-]{1,64}$")


class CredentialPut(BaseModel):
    value: str = Field(min_length=1)


class CredentialList(BaseModel):
    names: list[str]


def _validate_name(name: str) -> None:
    if not NAME_RE.match(name):
        raise HTTPException(status.HTTP_400_BAD_REQUEST, "Invalid credential name")


@router.get("", response_model=CredentialList)
async def list_credentials(user: User = Depends(get_current_user)) -> CredentialList:
    try:
        names = await vault_service.get_client().list_user_credentials(user.external_id)
    except httpx.HTTPError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"Vault error: {exc}") from exc
    return CredentialList(names=names)


@router.put("/{name}", status_code=status.HTTP_204_NO_CONTENT)
async def put_credential(
    name: str,
    body: CredentialPut,
    user: User = Depends(get_current_user),
) -> None:
    _validate_name(name)
    try:
        await vault_service.get_client().write_user_credential(
            user.external_id, name, body.value,
        )
    except httpx.HTTPError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"Vault error: {exc}") from exc


@router.delete("/{name}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_credential(
    name: str,
    user: User = Depends(get_current_user),
) -> None:
    _validate_name(name)
    try:
        await vault_service.get_client().delete_user_credential(user.external_id, name)
    except httpx.HTTPError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"Vault error: {exc}") from exc
