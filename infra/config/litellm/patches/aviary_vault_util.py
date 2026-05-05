"""Per-user credential lookup for LiteLLM patches.

Intentionally uncached — credential updates must take effect on the next call.
"""

from __future__ import annotations

import logging
import os
import time

import httpx


logger = logging.getLogger(__name__)

VAULT_ADDR = os.environ["VAULT_ADDR"]
VAULT_TOKEN = os.environ["VAULT_TOKEN"]

PLATFORM_NAMESPACE = "aviary"

# Warn above this — sustained slow fetches mean we should add invalidation-aware caching.
_SLOW_FETCH_SECONDS = 0.5


async def fetch_credential(sub: str, namespace: str, key: str) -> str | None:
    """Return the credential or ``None`` if missing.

    Raises ``Exception`` on Vault transport error so callers can surface
    a "credential service unavailable" 5xx.
    """
    url = f"{VAULT_ADDR}/v1/secret/data/aviary/credentials/{sub}/{namespace}/{key}"
    started = time.monotonic()
    try:
        async with httpx.AsyncClient() as client:
            resp = await client.get(
                url, headers={"X-Vault-Token": VAULT_TOKEN}, timeout=10,
            )
            if resp.status_code == 404:
                logger.debug("Vault miss sub=%s ns=%s key=%s", sub, namespace, key)
                return None
            resp.raise_for_status()
            return resp.json()["data"]["data"].get("value")
    except httpx.HTTPError as exc:
        raise Exception(f"Vault error fetching '{namespace}/{key}': {exc}") from exc
    finally:
        elapsed = time.monotonic() - started
        if elapsed > _SLOW_FETCH_SECONDS:
            logger.warning(
                "Vault fetch slow ns=%s key=%s elapsed=%.2fs",
                namespace, key, elapsed,
            )
