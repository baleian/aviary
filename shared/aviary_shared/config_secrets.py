"""Loader for the project-root config.yaml ``secrets:`` section.

Used as a Vault fallback when ``VAULT_ADDR`` is unset (single-machine dev
without an actual Vault). Layout mirrors the Vault path convention
``secret/aviary/credentials/{sub}/{key_name}``::

    secrets:
      dev-user:
        anthropic-api-key: sk-...
        github-token: ghp_...
"""

from __future__ import annotations

from pathlib import Path

import yaml


class ConfigSecrets:
    def __init__(self, table: dict[str, dict[str, str]]) -> None:
        self._table = table

    def lookup(self, user_external_id: str, key_name: str) -> str | None:
        return (self._table.get(user_external_id) or {}).get(key_name)


def load_secrets(path: str | Path) -> ConfigSecrets:
    p = Path(path)
    if not p.exists():
        return ConfigSecrets({})
    raw = yaml.safe_load(p.read_text()) or {}
    table = raw.get("secrets") or {}
    if not isinstance(table, dict):
        return ConfigSecrets({})
    cleaned: dict[str, dict[str, str]] = {}
    for sub, entries in table.items():
        if isinstance(entries, dict):
            cleaned[str(sub)] = {str(k): str(v) for k, v in entries.items()}
    return ConfigSecrets(cleaned)
