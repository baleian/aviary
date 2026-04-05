"""OIDC JWT validation — same dual-URL pattern as api/app/auth/oidc.py."""

import time
from dataclasses import dataclass, field

import httpx
from jose import JWTError, jwt

from app.config import settings

_oidc_config: dict | None = None
_jwks: dict | None = None
_jwks_fetched_at: float = 0
_JWKS_CACHE_TTL = 3600


@dataclass
class TokenClaims:
    sub: str
    email: str
    display_name: str
    roles: list[str] = field(default_factory=list)
    groups: list[str] = field(default_factory=list)


def _internal_issuer() -> str:
    return settings.oidc_internal_issuer or settings.oidc_issuer


async def _fetch_oidc_config() -> dict:
    url = f"{_internal_issuer()}/.well-known/openid-configuration"
    async with httpx.AsyncClient() as client:
        resp = await client.get(url, timeout=10)
        resp.raise_for_status()
        return resp.json()


async def get_oidc_config() -> dict:
    global _oidc_config
    if _oidc_config is None:
        _oidc_config = await _fetch_oidc_config()
    return _oidc_config


async def _fetch_jwks(jwks_uri: str) -> dict:
    async with httpx.AsyncClient() as client:
        resp = await client.get(jwks_uri, timeout=10)
        resp.raise_for_status()
        return resp.json()


def _rewrite_url(url: str) -> str:
    internal = _internal_issuer()
    public = settings.oidc_issuer
    if internal != public and url.startswith(public):
        return internal + url[len(public):]
    return url


async def get_jwks() -> dict:
    global _jwks, _jwks_fetched_at
    now = time.time()
    if _jwks is None or (now - _jwks_fetched_at) > _JWKS_CACHE_TTL:
        config = await get_oidc_config()
        jwks_uri = _rewrite_url(config["jwks_uri"])
        _jwks = await _fetch_jwks(jwks_uri)
        _jwks_fetched_at = now
    return _jwks


async def init_oidc() -> None:
    global _oidc_config, _jwks, _jwks_fetched_at
    try:
        _oidc_config = await _fetch_oidc_config()
        jwks_uri = _rewrite_url(_oidc_config["jwks_uri"])
        _jwks = await _fetch_jwks(jwks_uri)
        _jwks_fetched_at = time.time()
    except Exception:
        _oidc_config = None
        _jwks = None


async def validate_token(token: str) -> TokenClaims:
    """Validate a JWT access token and extract claims."""
    jwks = await get_jwks()

    try:
        unverified_header = jwt.get_unverified_header(token)
    except JWTError as e:
        raise ValueError(f"Invalid token header: {e}") from e

    kid = unverified_header.get("kid")
    rsa_key = None
    for key in jwks.get("keys", []):
        if key.get("kid") == kid:
            rsa_key = key
            break

    if rsa_key is None:
        global _jwks_fetched_at
        _jwks_fetched_at = 0
        jwks = await get_jwks()
        for key in jwks.get("keys", []):
            if key.get("kid") == kid:
                rsa_key = key
                break
        if rsa_key is None:
            raise ValueError("Token signing key not found in JWKS")

    try:
        payload = jwt.decode(
            token,
            rsa_key,
            algorithms=["RS256"],
            issuer=settings.oidc_issuer,
            audience=settings.oidc_audience,
            options={"verify_aud": settings.oidc_audience is not None},
        )
    except JWTError as e:
        raise ValueError(f"Token validation failed: {e}") from e

    sub = payload.get("sub")
    email = payload.get("email", "")
    display_name = payload.get("name") or payload.get("preferred_username") or email

    roles: list[str] = []
    if "realm_roles" in payload:
        roles = payload["realm_roles"]
    elif "realm_access" in payload:
        roles = payload.get("realm_access", {}).get("roles", [])

    raw_groups: list[str] = payload.get("groups", [])
    groups = [g.lstrip("/") for g in raw_groups if g]

    if not sub:
        raise ValueError("Token missing 'sub' claim")

    return TokenClaims(sub=sub, email=email, display_name=display_name, roles=roles, groups=groups)
