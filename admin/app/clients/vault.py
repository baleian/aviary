"""Lazy singleton VaultClient for per-user credential admin."""

from aviary_shared.vault import VaultClient

from app.config import settings

_client: VaultClient | None = None


def vault() -> VaultClient:
    global _client
    if _client is None:
        _client = VaultClient(settings.vault_addr, settings.vault_token)
    return _client
