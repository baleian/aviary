"""FastAPI dependencies for extracting authenticated user from JWT."""

import logging

from fastapi import Request
from starlette.responses import JSONResponse

from app.auth.oidc import TokenClaims, validate_token

logger = logging.getLogger(__name__)


async def get_current_user(request: Request) -> TokenClaims:
    """Extract and validate OIDC JWT from Authorization header."""
    auth_header = request.headers.get("authorization", "")
    if not auth_header.startswith("Bearer "):
        raise ValueError("Missing or invalid Authorization header")

    token = auth_header.removeprefix("Bearer ").strip()
    return await validate_token(token)


async def auth_error_handler(request: Request, exc: ValueError) -> JSONResponse:
    """Return 401 for authentication errors."""
    return JSONResponse(status_code=401, content={"error": str(exc)})
