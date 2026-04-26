import httpx

from aviary_shared.auth import build_oidc_validator
from aviary_shared.auth.oidc import TokenClaims  # noqa: F401 — re-exported

from app.config import settings

_validator = build_oidc_validator(settings)


def idp_enabled() -> bool:
    return _validator.enabled


def dev_user_sub() -> str:
    return _validator.dev_user_sub


async def init_oidc() -> None:
    await _validator.init()


async def validate_token(token: str) -> TokenClaims:
    return await _validator.validate_token(token)


async def get_oidc_config() -> dict:
    return await _validator.get_oidc_config()


async def get_jwks() -> dict:
    return await _validator.get_jwks()


def to_public_url(url: str) -> str:
    return _validator.to_public_url(url)


def _rewrite_url(url: str) -> str:
    return _validator._rewrite_url(url)


def _token_request_data(grant: dict[str, str]) -> dict[str, str]:
    data = dict(grant)
    if settings.oidc_client_id:
        data["client_id"] = settings.oidc_client_id
    if settings.oidc_client_secret:
        data["client_secret"] = settings.oidc_client_secret
    return data


async def refresh_tokens(refresh_token: str) -> dict:
    config = await get_oidc_config()
    token_endpoint = _rewrite_url(config["token_endpoint"])

    async with httpx.AsyncClient() as client:
        resp = await client.post(
            token_endpoint,
            data=_token_request_data({
                "grant_type": "refresh_token",
                "refresh_token": refresh_token,
            }),
            timeout=10,
        )
        resp.raise_for_status()
        return resp.json()


async def exchange_code(code: str, redirect_uri: str, code_verifier: str) -> dict:
    config = await get_oidc_config()
    token_endpoint = _rewrite_url(config["token_endpoint"])

    async with httpx.AsyncClient() as client:
        resp = await client.post(
            token_endpoint,
            data=_token_request_data({
                "grant_type": "authorization_code",
                "code": code,
                "redirect_uri": redirect_uri,
                "code_verifier": code_verifier,
            }),
            timeout=10,
        )
        resp.raise_for_status()
        return resp.json()
