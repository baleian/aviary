"""Lazy Vault client used by the API server."""

from aviary_shared.vault import VaultClient

from app.config import settings

_client: VaultClient | None = None


def _vault() -> VaultClient:
    global _client
    if _client is None:
        _client = VaultClient(settings.vault_addr, settings.vault_token)
    return _client


async def read_secret(path: str) -> dict | None:
    return await _vault().read(path)


async def write_secret(path: str, data: dict) -> None:
    await _vault().write(path, data)


async def delete_secret(path: str) -> None:
    await _vault().delete(path)
