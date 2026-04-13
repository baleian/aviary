"""FastAPI auth dependencies. Single entry point for both REST and WebSocket
request authentication."""

from fastapi import Cookie, Depends, HTTPException, WebSocket, status
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth import session_store
from app.auth.oidc import validator
from app.deps import get_db
from aviary_shared.auth import TokenClaims
from aviary_shared.db.models import User


async def _upsert_user(db: AsyncSession, claims: TokenClaims) -> User:
    result = await db.execute(select(User).where(User.external_id == claims.sub))
    user = result.scalar_one_or_none()
    if user is None:
        user = User(
            external_id=claims.sub,
            email=claims.email,
            display_name=claims.display_name,
        )
        db.add(user)
        await db.flush()
    elif user.email != claims.email or user.display_name != claims.display_name:
        user.email = claims.email
        user.display_name = claims.display_name
        await db.flush()
    return user


async def _user_from_sid(sid: str, db: AsyncSession) -> User:
    data = await session_store.get_fresh(sid)
    if data is None:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "Session expired")
    try:
        claims = await validator.validate_token(data.access_token)
    except ValueError as e:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, str(e)) from e
    return await _upsert_user(db, claims)


async def get_current_user(
    aviary_session: str | None = Cookie(default=None, alias=session_store.COOKIE_NAME),
    db: AsyncSession = Depends(get_db),
) -> User:
    if not aviary_session:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "Not authenticated")
    return await _user_from_sid(aviary_session, db)


async def authenticate_ws(websocket: WebSocket, db: AsyncSession) -> User | None:
    """Validate WS handshake (origin + session cookie). Returns user or None
    after closing the socket with an appropriate code."""
    from app.config import settings

    origin = websocket.headers.get("origin")
    if not origin or origin not in settings.cors_origins:
        await websocket.close(code=4001, reason="Invalid origin")
        return None

    sid = websocket.cookies.get(session_store.COOKIE_NAME)
    if not sid:
        await websocket.close(code=4001, reason="Missing session")
        return None

    try:
        return await _user_from_sid(sid, db)
    except HTTPException:
        await websocket.close(code=4001, reason="Invalid or expired session")
        return None
