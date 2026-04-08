#!/bin/bash
# claude-sandbox.sh — Drop-in replacement for the `claude` binary.
#
# This script IS the `claude` command. The real binary lives at `claude-real`.
# SDK invokes `claude` → hits this wrapper → bwrap namespace → `claude-real`.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE (hostPath) is bind-mounted to /workspace (shared across agents)
#   - $SESSION_CLAUDE_DIR (PVC) is bind-mounted to /workspace/.claude (per-agent overlay)
#   - $SESSION_TMP is bind-mounted to /tmp (per-agent, NOT shared)
#   - PID namespace isolated
#
# If SESSION_WORKSPACE is not set (e.g. direct CLI usage), runs without sandbox.

set -euo pipefail

REAL_CLAUDE="$(dirname "$0")/claude-real"

if [ -z "${SESSION_WORKSPACE:-}" ]; then
    exec "$REAL_CLAUDE" "$@"
fi

mkdir -p "$SESSION_WORKSPACE"
mkdir -p "${SESSION_CLAUDE_DIR:?SESSION_CLAUDE_DIR must be set}"
mkdir -p "${SESSION_TMP:?SESSION_TMP must be set}"

# Ensure Node.js fetch() respects proxy env vars inside the sandbox.
if [ -f /app/scripts/proxy-bootstrap.js ] && [ -n "${HTTP_PROXY:-}" ]; then
    export NODE_OPTIONS="--require /app/scripts/proxy-bootstrap.js ${NODE_OPTIONS:-}"
fi

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
