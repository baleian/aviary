"""Redis-backed session store.

Browser holds only an opaque session id cookie; OIDC tokens live here.
`get_fresh_session` transparently refreshes access tokens via OIDC before
expiry — crucial for long-running WebSocket connections.
"""

import json
import logging
import secrets
import time
from dataclasses import asdict, dataclass

import httpx

from app.auth.oidc import validator
from app.config import settings
from app.deps import get_redis

logger = logging.getLogger(__name__)

COOKIE_NAME = "aviary_session"
KEY_PREFIX = "auth:session:"
TTL_SECONDS = 24 * 60 * 60
REFRESH_BUFFER = 60


@dataclass
class SessionData:
    user_external_id: str
    access_token: str
    refresh_token: str
    id_token: str | None
    access_token_expires_at: int


def _key(sid: str) -> str:
    return f"{KEY_PREFIX}{sid}"


async def create(
    *,
    user_external_id: str,
    access_token: str,
    refresh_token: str,
    id_token: str | None,
    expires_in: int,
) -> str:
    sid = secrets.token_urlsafe(32)
    data = SessionData(
        user_external_id=user_external_id,
        access_token=access_token,
        refresh_token=refresh_token,
        id_token=id_token,
        access_token_expires_at=int(time.time()) + int(expires_in),
    )
    await get_redis().set(_key(sid), json.dumps(asdict(data)), ex=TTL_SECONDS)
    return sid


async def _load(sid: str) -> SessionData | None:
    raw = await get_redis().get(_key(sid))
    if not raw:
        return None
    try:
        return SessionData(**json.loads(raw))
    except (ValueError, TypeError):
        logger.warning("Dropping corrupted session %s", sid)
        await delete(sid)
        return None


async def _save(sid: str, data: SessionData) -> None:
    await get_redis().set(_key(sid), json.dumps(asdict(data)), ex=TTL_SECONDS)


async def delete(sid: str) -> None:
    await get_redis().delete(_key(sid))


async def get_fresh(sid: str) -> SessionData | None:
    """Return session data with a guaranteed-fresh access token, or None if
    the session is gone or the refresh token was rejected."""
    data = await _load(sid)
    if data is None:
        return None
    if data.access_token_expires_at - int(time.time()) > REFRESH_BUFFER:
        return data

    try:
        new_tokens = await validator.refresh(data.refresh_token, settings.oidc_client_id)
    except httpx.HTTPStatusError as e:
        status_code = e.response.status_code if e.response is not None else 0
        if 400 <= status_code < 500:
            logger.info("Refresh token rejected (%s), clearing session %s", status_code, sid)
            await delete(sid)
            return None
        logger.warning("Transient refresh failure (%s) for session %s", status_code, sid)
        return None
    except httpx.HTTPError as e:
        logger.warning("Transient refresh failure (%s) for session %s", type(e).__name__, sid)
        return None

    try:
        claims = await validator.validate_token(new_tokens["access_token"])
    except ValueError:
        await delete(sid)
        return None

    if claims.sub != data.user_external_id:
        logger.error("Refreshed token sub mismatch; dropping session %s", sid)
        await delete(sid)
        return None

    data.access_token = new_tokens["access_token"]
    data.refresh_token = new_tokens.get("refresh_token") or data.refresh_token
    data.id_token = new_tokens.get("id_token") or data.id_token
    data.access_token_expires_at = int(time.time()) + int(new_tokens.get("expires_in", 300))
    await _save(sid, data)
    return data
