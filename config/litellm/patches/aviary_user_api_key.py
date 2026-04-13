"""LiteLLM CustomLogger hook — per-user Anthropic API key injection via Vault.

For Anthropic-prefixed model requests, extracts the user's OIDC JWT from the
``X-Aviary-User-Token`` header, validates it against Keycloak JWKS, looks up
the user's personal Anthropic API key in Vault, and overrides the key used
for the upstream call. If no per-user key is stored, the request is passed
through unchanged so the configured api_key (or none, for local backends)
applies — Anthropic itself will then 401 if a key was actually required.

Loaded at Python startup via the ``.pth`` file alongside other patches.
"""

from __future__ import annotations

import hashlib
import logging
import os
import time
from typing import Any

import httpx

logger = logging.getLogger("aviary.user_api_key")

VAULT_ADDR = os.environ.get("VAULT_ADDR", "")
VAULT_TOKEN = os.environ.get("VAULT_TOKEN", "")
OIDC_ISSUER = os.environ.get("OIDC_ISSUER", "")
OIDC_INTERNAL_ISSUER = os.environ.get("OIDC_INTERNAL_ISSUER", "") or OIDC_ISSUER

try:
    from jose import jwt as _jose_jwt, JWTError  # type: ignore[import-untyped]

    def _decode_jwt(token: str, rsa_key: dict, issuer: str) -> dict:
        return _jose_jwt.decode(
            token,
            rsa_key,
            algorithms=["RS256"],
            issuer=issuer,
            options={"verify_aud": False},
        )

    def _get_unverified_header(token: str) -> dict:
        return _jose_jwt.get_unverified_header(token)

except ImportError:
    import jwt as _pyjwt  # type: ignore[import-untyped]

    JWTError = _pyjwt.PyJWTError  # type: ignore[assignment,misc]

    def _decode_jwt(token: str, rsa_key: dict, issuer: str) -> dict:  # type: ignore[misc]
        from jwt.algorithms import RSAAlgorithm  # type: ignore[import-untyped]

        public_key = RSAAlgorithm.from_jwk(rsa_key)
        return _pyjwt.decode(
            token,
            public_key,
            algorithms=["RS256"],
            issuer=issuer,
            options={"verify_aud": False},
        )

    def _get_unverified_header(token: str) -> dict:  # type: ignore[misc]
        return _pyjwt.get_unverified_header(token)


_jwks: dict | None = None
_jwks_fetched_at: float = 0
_JWKS_TTL = 3600


async def _fetch_jwks() -> dict:
    discovery_url = f"{OIDC_INTERNAL_ISSUER}/.well-known/openid-configuration"
    async with httpx.AsyncClient() as client:
        resp = await client.get(discovery_url, timeout=10)
        resp.raise_for_status()
        jwks_uri = resp.json()["jwks_uri"]
        if OIDC_INTERNAL_ISSUER != OIDC_ISSUER and jwks_uri.startswith(OIDC_ISSUER):
            jwks_uri = OIDC_INTERNAL_ISSUER + jwks_uri[len(OIDC_ISSUER):]
        resp2 = await client.get(jwks_uri, timeout=10)
        resp2.raise_for_status()
        return resp2.json()


async def _get_jwks(force: bool = False) -> dict:
    global _jwks, _jwks_fetched_at
    now = time.time()
    if force or _jwks is None or (now - _jwks_fetched_at) > _JWKS_TTL:
        _jwks = await _fetch_jwks()
        _jwks_fetched_at = now
    return _jwks


_sub_cache: dict[str, tuple[str, float]] = {}
_SUB_CACHE_TTL = 1800


def _token_cache_key(token: str) -> str:
    parts = token.rsplit(".", 1)
    sig = parts[-1] if len(parts) > 1 else token
    return hashlib.sha256(sig.encode()).hexdigest()[:32]


def _find_key(jwks: dict, kid: str | None) -> dict | None:
    for key in jwks.get("keys", []):
        if key.get("kid") == kid:
            return key
    return None


async def _extract_sub(token: str) -> str:
    cache_key = _token_cache_key(token)
    now = time.time()

    cached = _sub_cache.get(cache_key)
    if cached and (now - cached[1]) < _SUB_CACHE_TTL:
        return cached[0]

    header = _get_unverified_header(token)
    kid = header.get("kid")
    jwks = await _get_jwks()

    rsa_key = _find_key(jwks, kid)
    if rsa_key is None:
        jwks = await _get_jwks(force=True)
        rsa_key = _find_key(jwks, kid)
    if rsa_key is None:
        raise Exception("Token signing key not found in JWKS")

    payload = _decode_jwt(token, rsa_key, OIDC_ISSUER)
    sub = payload.get("sub")
    if not sub:
        raise Exception("Token missing 'sub' claim")

    _sub_cache[cache_key] = (sub, now)
    return sub


_api_key_cache: dict[str, tuple[str | None, float]] = {}
_API_KEY_TTL = 30

VAULT_CREDENTIAL_NAME = "anthropic-api-key"


async def _get_vault_api_key(sub: str) -> str | None:
    now = time.time()
    cached = _api_key_cache.get(sub)
    if cached and (now - cached[1]) < _API_KEY_TTL:
        return cached[0]

    if not VAULT_ADDR or not VAULT_TOKEN:
        raise Exception("Credential service not configured (Vault)")

    vault_path = f"aviary/credentials/{sub}/{VAULT_CREDENTIAL_NAME}"
    url = f"{VAULT_ADDR}/v1/secret/data/{vault_path}"

    async with httpx.AsyncClient() as client:
        resp = await client.get(
            url,
            headers={"X-Vault-Token": VAULT_TOKEN},
            timeout=10,
        )
        if resp.status_code == 404:
            _api_key_cache[sub] = (None, now)
            return None
        resp.raise_for_status()
        token = resp.json()["data"]["data"].get("value")
        _api_key_cache[sub] = (token, now)
        return token


try:
    from litellm.integrations.custom_logger import CustomLogger  # type: ignore[import-untyped]
except ImportError:
    CustomLogger = None  # type: ignore[assignment,misc]


def _register():
    if CustomLogger is None:
        logger.warning("LiteLLM CustomLogger not available — skipping user API key hook")
        return

    import litellm  # type: ignore[import-untyped]
    from litellm.exceptions import AuthenticationError  # type: ignore[import-untyped]

    def _auth_error(msg: str) -> AuthenticationError:
        return AuthenticationError(message=msg, llm_provider="anthropic", model="")

    class AviaryUserApiKeyHook(CustomLogger):
        async def async_pre_call_hook(
            self,
            user_api_key_dict: dict[str, Any],
            cache: Any,
            data: dict[str, Any],
            call_type: str,
        ) -> dict[str, Any]:
            model = data.get("model", "")
            if not model.startswith("anthropic/"):
                return data

            proxy_req = data.get("proxy_server_request") or (
                data.get("metadata", {}).get("proxy_server_request")
            ) or {}
            headers = proxy_req.get("headers", {})
            user_token = headers.get("x-aviary-user-token")

            if not user_token:
                return data

            try:
                sub = await _extract_sub(user_token)
            except Exception as exc:
                raise _auth_error(f"Invalid user token: {exc}") from exc

            try:
                api_key = await _get_vault_api_key(sub)
            except Exception as exc:
                raise _auth_error(f"Credential service error: {exc}") from exc

            if not api_key:
                # No per-user key configured — pass through with the model's
                # configured api_key. Local/keyless backends keep working;
                # Anthropic itself will 401 if a real key was required.
                return data

            data["api_key"] = api_key
            if "litellm_params" in data:
                data["litellm_params"]["api_key"] = api_key

            logger.info("Using per-user Anthropic API key for sub=%s", sub)
            return data

    hook = AviaryUserApiKeyHook()
    litellm.callbacks.append(hook)
    logger.info("Aviary per-user API key hook registered")


if OIDC_ISSUER:
    try:
        _register()
    except Exception:
        logger.warning("Failed to register user API key hook", exc_info=True)
else:
    logger.info("OIDC_ISSUER not set — per-user API key hook disabled")
