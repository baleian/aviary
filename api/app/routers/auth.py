import httpx
from fastapi import APIRouter, Cookie, Depends, HTTPException, Response, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth import session_store
from app.auth.dependencies import _upsert_user, get_current_user
from app.auth.oidc import validator
from app.config import settings
from app.deps import get_db
from app.schemas.user import (
    AuthConfigResponse,
    PreferencesUpdateRequest,
    TokenExchangeRequest,
    UserResponse,
)
from aviary_shared.db.models import User

router = APIRouter()

_COOKIE_KW = dict(
    key=session_store.COOKIE_NAME,
    httponly=True,
    samesite="lax",
    path="/",
    max_age=session_store.TTL_SECONDS,
)


def _set_cookie(response: Response, sid: str) -> None:
    response.set_cookie(value=sid, secure=settings.cookie_secure, **_COOKIE_KW)


def _clear_cookie(response: Response) -> None:
    response.delete_cookie(key=session_store.COOKIE_NAME, path="/")


@router.get("/config", response_model=AuthConfigResponse)
async def auth_config():
    """OIDC discovery endpoints (public-facing URLs for browser use)."""
    config = await validator.get_config()
    return AuthConfigResponse(
        issuer=validator.to_public_url(config["issuer"]),
        client_id=settings.oidc_client_id,
        authorization_endpoint=validator.to_public_url(config["authorization_endpoint"]),
        token_endpoint=validator.to_public_url(config["token_endpoint"]),
        end_session_endpoint=validator.to_public_url(config.get("end_session_endpoint", "")),
    )


@router.post("/callback", response_model=UserResponse)
async def auth_callback(
    body: TokenExchangeRequest,
    response: Response,
    db: AsyncSession = Depends(get_db),
):
    try:
        tokens = await validator.exchange_code(
            body.code, body.redirect_uri, body.code_verifier, settings.oidc_client_id
        )
    except httpx.HTTPError as e:
        raise HTTPException(status.HTTP_400_BAD_REQUEST, f"Token exchange failed: {e}") from e

    refresh_token = tokens.get("refresh_token")
    if not refresh_token:
        raise HTTPException(status.HTTP_400_BAD_REQUEST, "OIDC server did not return a refresh token")

    try:
        claims = await validator.validate_token(tokens["access_token"])
    except ValueError as e:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, str(e)) from e

    user = await _upsert_user(db, claims)

    sid = await session_store.create(
        user_external_id=claims.sub,
        access_token=tokens["access_token"],
        refresh_token=refresh_token,
        id_token=tokens.get("id_token"),
        expires_in=tokens.get("expires_in", 300),
    )
    _set_cookie(response, sid)
    return user


@router.get("/me", response_model=UserResponse)
async def get_me(user: User = Depends(get_current_user)):
    return user


@router.patch("/me/preferences", response_model=UserResponse)
async def update_preferences(
    body: PreferencesUpdateRequest,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    # SQLAlchemy doesn't track in-place mutations on JSONB; reassign.
    user.preferences = {**(user.preferences or {}), **body.preferences}
    await db.flush()
    return user


@router.post("/logout", status_code=status.HTTP_204_NO_CONTENT)
async def logout(
    response: Response,
    aviary_session: str | None = Cookie(default=None, alias=session_store.COOKIE_NAME),
):
    if aviary_session:
        await session_store.delete(aviary_session)
    _clear_cookie(response)
