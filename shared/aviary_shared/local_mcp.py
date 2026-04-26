"""Loader for config.yaml mcp_servers (direct, non-gateway)."""

from __future__ import annotations

from pathlib import Path

import yaml


def load_servers(path: str | Path) -> dict[str, dict]:
    p = Path(path)
    if not p.exists():
        return {}
    raw = yaml.safe_load(p.read_text()) or {}
    return raw.get("mcp_servers") or {}
