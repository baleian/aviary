"""Vault client always reads from Vault."""

from __future__ import annotations

from unittest.mock import AsyncMock, patch

import pytest

from app.services import vault_client


@pytest.fixture(autouse=True)
def _reset_caches():
    vault_client._client = None
    yield
    vault_client._client = None


@pytest.mark.asyncio
async def test_fetch_uses_vault():
    fake = AsyncMock(return_value="ghp_from_vault")
    with patch("aviary_shared.vault.VaultClient.read_user_credential", fake):
        creds = await vault_client.fetch_user_credentials("dev-user")

    assert creds == {"github_token": "ghp_from_vault"}
    fake.assert_awaited_once_with("dev-user", "aviary", "github-token")


@pytest.mark.asyncio
async def test_fetch_returns_empty_when_vault_misses():
    fake = AsyncMock(return_value=None)
    with patch("aviary_shared.vault.VaultClient.read_user_credential", fake):
        creds = await vault_client.fetch_user_credentials("dev-user")

    assert creds == {}
