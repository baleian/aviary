"""Lazy singleton VaultClient for the API process."""

from __future__ import annotations

from app.config import settings
from aviary_shared.vault import VaultClient

_client: VaultClient | None = None


def get_client() -> VaultClient:
    global _client
    if _client is None:
        _client = VaultClient(settings.vault_addr, settings.vault_token)
    return _client
