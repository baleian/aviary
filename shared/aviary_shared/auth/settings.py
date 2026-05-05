from __future__ import annotations

from typing import Protocol

from pydantic import BaseModel

from aviary_shared.auth.oidc import OIDCValidator


class IdpSettings(BaseModel):
    oidc_issuer: str
    oidc_internal_issuer: str | None = None
    oidc_client_id: str | None = None
    oidc_client_secret: str | None = None


class _OidcConfigLike(Protocol):
    oidc_issuer: str
    oidc_internal_issuer: str | None


def build_oidc_validator(config: _OidcConfigLike) -> OIDCValidator:
    return OIDCValidator(
        issuer=config.oidc_issuer,
        internal_issuer=config.oidc_internal_issuer,
    )
