#!/bin/bash
# Generic bwrap sandbox wrapper — the single source of truth for the
# per-session mount namespace. Callers pass the command + args to exec inside.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE is bind-mounted to /workspace (shared session workspace)
#   - $SESSION_CLAUDE_DIR is bind-mounted to /workspace/.claude (per-agent CLI state)
#   - $SESSION_TMP is bind-mounted to /tmp (per-agent temp, NOT shared)
#   - PID namespace isolated
#
# Usage:
#   SESSION_WORKSPACE=... SESSION_CLAUDE_DIR=... SESSION_TMP=... \
#     sandbox.sh <command> [args...]
#
# If SESSION_WORKSPACE is not set, runs the command without a sandbox
# (direct CLI usage, not a real session).

set -euo pipefail

if [ -z "${SESSION_WORKSPACE:-}" ]; then
    exec "$@"
fi

mkdir -p "$SESSION_WORKSPACE"
mkdir -p "${SESSION_CLAUDE_DIR:?SESSION_CLAUDE_DIR must be set}"
mkdir -p "${SESSION_TMP:?SESSION_TMP must be set}"

exec bwrap \
    --ro-bind / / \
    --dev /dev \
    --proc /proc \
    --tmpfs /workspace-shared \
    --bind "$SESSION_WORKSPACE" /workspace \
    --bind "$SESSION_CLAUDE_DIR" /workspace/.claude \
    --bind "$SESSION_TMP" /tmp \
    --unshare-pid \
    --die-with-parent \
    --setenv HOME /workspace \
    -- \
    "$@"
