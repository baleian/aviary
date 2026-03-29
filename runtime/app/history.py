"""Conversation history manager — persists to /workspace/.history/ for PVC survival."""

import json
import os
from pathlib import Path
from typing import Any

HISTORY_DIR = Path("/workspace/.history")
MESSAGES_FILE = HISTORY_DIR / "messages.jsonl"


def ensure_history_dir():
    HISTORY_DIR.mkdir(parents=True, exist_ok=True)


def load_messages() -> list[dict[str, Any]]:
    """Load all messages from the JSONL history file."""
    if not MESSAGES_FILE.exists():
        return []
    messages = []
    with open(MESSAGES_FILE) as f:
        for line in f:
            line = line.strip()
            if line:
                messages.append(json.loads(line))
    return messages


def append_message(role: str, content: str, metadata: dict | None = None):
    """Append a message to the history file."""
    ensure_history_dir()
    entry = {"role": role, "content": content}
    if metadata:
        entry["metadata"] = metadata
    with open(MESSAGES_FILE, "a") as f:
        f.write(json.dumps(entry) + "\n")


def get_conversation_for_sdk() -> list[dict[str, str]]:
    """Format conversation history for Claude Agent SDK."""
    messages = load_messages()
    return [{"role": m["role"], "content": m["content"]} for m in messages]
