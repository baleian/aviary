"""Vault client for fetching per-user credentials (GitHub token, etc.)."""

import time

from aviary_shared.vault import VaultClient

from app import metrics
from app.config import settings

_client: VaultClient | None = None


def _vault() -> VaultClient:
    global _client
    if _client is None:
        _client = VaultClient(settings.vault_addr, settings.vault_token)
    return _client


async def fetch_user_credentials(user_external_id: str) -> dict[str, str]:
    creds: dict[str, str] = {}
    started = time.monotonic()
    try:
        github_token = await _vault().read_user_credential(user_external_id, "github-token")
    finally:
        metrics.vault_fetch_duration_seconds.record(time.monotonic() - started)
    if github_token:
        creds["github_token"] = github_token
    return creds
