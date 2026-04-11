"""HashiCorp Vault KV v2 client.

Centralized Vault access for all Aviary services. Per-user credentials live at
``secret/aviary/credentials/{user_external_id}/{key_name}`` (KV v2). The
``user_external_id`` is the OIDC ``sub`` claim from Keycloak.
"""

from __future__ import annotations

import httpx


def credential_path(user_external_id: str, key_name: str) -> str:
    """KV v2 logical path (without ``/v1/secret/data/`` prefix) for a user credential."""
    return f"aviary/credentials/{user_external_id}/{key_name}"


class VaultClient:
    """Async Vault KV v2 client.

    All methods raise ``httpx.HTTPError`` on transport / non-404 status errors so
    callers can distinguish "secret not found" (None) from "Vault unreachable".
    """

    def __init__(self, addr: str, token: str, *, timeout: float = 10.0) -> None:
        if not addr or not token:
            raise ValueError("Vault addr and token are required")
        self._addr = addr.rstrip("/")
        self._token = token
        self._timeout = timeout

    @property
    def _headers(self) -> dict[str, str]:
        return {"X-Vault-Token": self._token}

    async def read(self, path: str) -> dict | None:
        """Read a KV v2 secret. Returns ``None`` if it does not exist."""
        url = f"{self._addr}/v1/secret/data/{path}"
        async with httpx.AsyncClient() as client:
            resp = await client.get(url, headers=self._headers, timeout=self._timeout)
            if resp.status_code == 404:
                return None
            resp.raise_for_status()
            return resp.json()["data"]["data"]

    async def write(self, path: str, data: dict) -> None:
        """Write a KV v2 secret."""
        url = f"{self._addr}/v1/secret/data/{path}"
        async with httpx.AsyncClient() as client:
            resp = await client.post(
                url,
                headers={**self._headers, "Content-Type": "application/json"},
                json={"data": data},
                timeout=self._timeout,
            )
            resp.raise_for_status()

    async def delete(self, path: str) -> None:
        """Delete a KV v2 secret. 404 is treated as already-deleted."""
        url = f"{self._addr}/v1/secret/metadata/{path}"
        async with httpx.AsyncClient() as client:
            resp = await client.delete(url, headers=self._headers, timeout=self._timeout)
            if resp.status_code != 404:
                resp.raise_for_status()

    async def list_keys(self, path: str) -> list[str]:
        """List child keys at a metadata path. Returns empty list if no entries."""
        url = f"{self._addr}/v1/secret/metadata/{path}"
        async with httpx.AsyncClient() as client:
            resp = await client.request(
                "LIST", url, headers=self._headers, timeout=self._timeout,
            )
            if resp.status_code == 404:
                return []
            resp.raise_for_status()
            return resp.json().get("data", {}).get("keys", [])

    # Convenience helpers for the per-user credential path convention.

    async def read_user_credential(
        self, user_external_id: str, key_name: str,
    ) -> str | None:
        """Read the ``value`` field of a per-user credential. Returns None if missing."""
        secret = await self.read(credential_path(user_external_id, key_name))
        if secret is None:
            return None
        return secret.get("value")

    async def write_user_credential(
        self, user_external_id: str, key_name: str, value: str,
    ) -> None:
        await self.write(credential_path(user_external_id, key_name), {"value": value})

    async def delete_user_credential(
        self, user_external_id: str, key_name: str,
    ) -> None:
        await self.delete(credential_path(user_external_id, key_name))

    async def list_user_credentials(self, user_external_id: str) -> list[str]:
        keys = await self.list_keys(f"aviary/credentials/{user_external_id}")
        return [k.rstrip("/") for k in keys]
