"""Service-local OIDC singleton."""

from aviary_shared.auth import OIDCValidator

from app.config import settings

validator = OIDCValidator(
    issuer=settings.oidc_issuer,
    internal_issuer=settings.oidc_internal_issuer,
    audience=settings.oidc_audience,
)
