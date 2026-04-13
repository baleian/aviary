"""OIDC JWT validation + Bearer extraction."""

from fastapi import Request

from aviary_shared.auth.oidc import OIDCValidator, TokenClaims

from app.config import settings

_validator = OIDCValidator(
    issuer=settings.oidc_issuer,
    internal_issuer=settings.oidc_internal_issuer,
    audience=settings.oidc_audience,
)


async def init_oidc() -> None:
    await _validator.init()


async def validate_token(token: str) -> TokenClaims:
    return await _validator.validate_token(token)


async def get_current_user(request: Request) -> TokenClaims:
    auth_header = request.headers.get("authorization", "")
    if not auth_header.startswith("Bearer "):
        raise ValueError("Missing or invalid Authorization header")
    token = auth_header.removeprefix("Bearer ").strip()
    return await validate_token(token)
