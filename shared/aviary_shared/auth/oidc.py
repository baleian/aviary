"""OIDC JWT validation with JWKS caching.

Parameterized — no service-specific config. Each service creates a validator
instance with its own issuer/audience settings.
"""

import time
from dataclasses import dataclass, field

import httpx
from jose import JWTError, jwt


@dataclass
class TokenClaims:
    sub: str
    email: str
    display_name: str
    roles: list[str] = field(default_factory=list)
    groups: list[str] = field(default_factory=list)


class OIDCValidator:
    def __init__(
        self,
        issuer: str,
        internal_issuer: str | None = None,
        audience: str | None = None,
        jwks_cache_ttl: int = 3600,
    ):
        self.issuer = issuer
        self.internal_issuer = internal_issuer or issuer
        self.audience = audience
        self._jwks_cache_ttl = jwks_cache_ttl
        self._config: dict | None = None
        self._jwks: dict | None = None
        self._jwks_fetched_at: float = 0

    def _rewrite_url(self, url: str) -> str:
        if self.internal_issuer != self.issuer and url.startswith(self.issuer):
            return self.internal_issuer + url[len(self.issuer):]
        return url

    def to_public_url(self, url: str) -> str:
        if self.internal_issuer != self.issuer and url.startswith(self.internal_issuer):
            return self.issuer + url[len(self.internal_issuer):]
        return url

    async def _fetch(self, url: str) -> dict:
        async with httpx.AsyncClient() as client:
            resp = await client.get(url, timeout=10)
            resp.raise_for_status()
            return resp.json()

    async def get_config(self) -> dict:
        if self._config is None:
            self._config = await self._fetch(
                f"{self.internal_issuer}/.well-known/openid-configuration"
            )
        return self._config

    async def get_jwks(self) -> dict:
        now = time.time()
        if self._jwks is None or (now - self._jwks_fetched_at) > self._jwks_cache_ttl:
            config = await self.get_config()
            self._jwks = await self._fetch(self._rewrite_url(config["jwks_uri"]))
            self._jwks_fetched_at = now
        return self._jwks

    async def init(self) -> None:
        try:
            await self.get_jwks()
        except Exception:
            self._config = None
            self._jwks = None

    async def exchange_code(self, code: str, redirect_uri: str, code_verifier: str, client_id: str) -> dict:
        config = await self.get_config()
        token_endpoint = self._rewrite_url(config["token_endpoint"])
        async with httpx.AsyncClient() as client:
            resp = await client.post(
                token_endpoint,
                data={
                    "grant_type": "authorization_code",
                    "client_id": client_id,
                    "code": code,
                    "redirect_uri": redirect_uri,
                    "code_verifier": code_verifier,
                },
                timeout=10,
            )
            resp.raise_for_status()
            return resp.json()

    async def refresh(self, refresh_token: str, client_id: str) -> dict:
        config = await self.get_config()
        token_endpoint = self._rewrite_url(config["token_endpoint"])
        async with httpx.AsyncClient() as client:
            resp = await client.post(
                token_endpoint,
                data={
                    "grant_type": "refresh_token",
                    "client_id": client_id,
                    "refresh_token": refresh_token,
                },
                timeout=10,
            )
            resp.raise_for_status()
            return resp.json()

    async def validate_token(self, token: str) -> TokenClaims:
        jwks = await self.get_jwks()

        try:
            header = jwt.get_unverified_header(token)
        except JWTError as e:
            raise ValueError(f"Invalid token header: {e}") from e

        kid = header.get("kid")
        rsa_key = self._find_key(jwks, kid)
        if rsa_key is None:
            self._jwks_fetched_at = 0  # force refresh on key rotation
            jwks = await self.get_jwks()
            rsa_key = self._find_key(jwks, kid)
            if rsa_key is None:
                raise ValueError("Token signing key not found in JWKS")

        try:
            payload = jwt.decode(
                token, rsa_key,
                algorithms=["RS256"],
                issuer=self.issuer,
                audience=self.audience,
                options={"verify_aud": self.audience is not None},
            )
        except JWTError as e:
            raise ValueError(f"Token validation failed: {e}") from e

        sub = payload.get("sub")
        if not sub:
            raise ValueError("Token missing 'sub' claim")

        email = payload.get("email", "")
        display_name = payload.get("name") or payload.get("preferred_username") or email
        roles = payload.get("realm_access", {}).get("roles", []) if isinstance(payload.get("realm_access"), dict) else []
        groups = [g.lstrip("/") for g in payload.get("groups", []) if g]

        return TokenClaims(sub=sub, email=email, display_name=display_name, roles=roles, groups=groups)

    @staticmethod
    def _find_key(jwks: dict, kid: str | None) -> dict | None:
        for key in jwks.get("keys", []):
            if key.get("kid") == kid:
                return key
        return None
