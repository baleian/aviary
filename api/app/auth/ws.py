from fastapi import WebSocket

from app.auth.oidc import validate_token
from app.auth.session_store import SESSION_COOKIE_NAME, get_fresh_session
from app.config import settings


async def handshake_ws(websocket: WebSocket) -> tuple[str, object] | None:
    origin = websocket.headers.get("origin")
    if not origin or origin not in settings.cors_origins:
        await websocket.close(code=4001, reason="Invalid origin")
        return None

    aviary_session_id = websocket.cookies.get(SESSION_COOKIE_NAME)
    if not aviary_session_id:
        await websocket.close(code=4001, reason="Missing session")
        return None

    initial_session = await get_fresh_session(aviary_session_id)
    if initial_session is None:
        await websocket.close(code=4001, reason="Invalid or expired session")
        return None

    try:
        claims = await validate_token(initial_session.id_token or "")
    except ValueError:
        await websocket.close(code=4001, reason="Invalid token")
        return None

    return aviary_session_id, claims
