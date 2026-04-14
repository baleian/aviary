"""Keycloak admin client — create/list/delete users in the realm."""

from __future__ import annotations

import time

import httpx

from app.config import settings


class KeycloakClient:
    def __init__(self) -> None:
        self._base = settings.keycloak_url.rstrip("/")
        self._realm = settings.keycloak_realm
        self._token: str | None = None
        self._expires_at = 0.0

    async def _authed(self) -> httpx.AsyncClient:
        token = await self._access_token()
        return httpx.AsyncClient(
            base_url=self._base,
            headers={"Authorization": f"Bearer {token}"},
            timeout=15.0,
        )

    async def _access_token(self) -> str:
        if self._token and time.time() < self._expires_at - 30:
            return self._token
        async with httpx.AsyncClient(timeout=10.0) as client:
            r = await client.post(
                f"{self._base}/realms/master/protocol/openid-connect/token",
                data={
                    "client_id": "admin-cli",
                    "grant_type": "password",
                    "username": settings.keycloak_admin,
                    "password": settings.keycloak_password,
                },
            )
            r.raise_for_status()
            data = r.json()
        self._token = data["access_token"]
        self._expires_at = time.time() + data.get("expires_in", 60)
        return self._token

    async def list_users(self) -> list[dict]:
        async with await self._authed() as c:
            r = await c.get(f"/admin/realms/{self._realm}/users", params={"max": 200})
            r.raise_for_status()
            return r.json()

    async def create_user(
        self, *, username: str, email: str, display_name: str, password: str,
    ) -> str:
        """Returns the Keycloak user id."""
        parts = display_name.split(" ", 1)
        first = parts[0]
        last = parts[1] if len(parts) > 1 else ""
        async with await self._authed() as c:
            r = await c.post(
                f"/admin/realms/{self._realm}/users",
                json={
                    "username": username,
                    "email": email,
                    "firstName": first,
                    "lastName": last,
                    "enabled": True,
                    "emailVerified": True,
                    "credentials": [
                        {"type": "password", "value": password, "temporary": False},
                    ],
                },
            )
            if r.status_code not in (201, 204):
                r.raise_for_status()
            # Keycloak returns 201 with Location header containing the user id.
            loc = r.headers.get("Location", "")
            return loc.rsplit("/", 1)[-1] if loc else ""

    async def delete_user(self, keycloak_user_id: str) -> None:
        async with await self._authed() as c:
            r = await c.delete(f"/admin/realms/{self._realm}/users/{keycloak_user_id}")
            if r.status_code not in (204, 404):
                r.raise_for_status()


client = KeycloakClient()
