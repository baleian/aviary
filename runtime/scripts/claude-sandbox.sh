#!/bin/bash
# claude-sandbox.sh — bubblewrap wrapper for session isolation.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE is bind-mounted to /workspace (shared session workspace)
#   - $SESSION_CLAUDE_DIR is bind-mounted to /workspace/.claude (per-agent CLI state)
#   - $SESSION_TMP is bind-mounted to /tmp (per-agent temp, NOT shared)
#   - PID namespace isolated
#
# If SESSION_WORKSPACE is not set, runs without sandbox.

set -euo pipefail

REAL_CLAUDE="$(dirname "$0")/claude-real"

if [ -z "${SESSION_WORKSPACE:-}" ]; then
    exec "$REAL_CLAUDE" "$@"
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
    "$REAL_CLAUDE" "$@"
