#!/bin/bash
# claude-sandbox.sh — Drop-in replacement for the `claude` binary.
#
# This script IS the `claude` command. The real binary lives at `claude-real`.
# SDK invokes `claude` → hits this wrapper → bwrap namespace → `claude-real`.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE (hostPath) is bind-mounted to /home/usr (shared home)
#   - $SESSION_CLAUDE_DIR (PVC) is bind-mounted to /home/usr/.claude (per-agent overlay)
#   - $SESSION_TMP (hostPath) is bind-mounted to /tmp (shared /tmp)
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
# NODE_OPTIONS with --require is set here (not just in pod env) to guarantee
# it reaches the CLI process regardless of how the SDK passes environment.
# Requires `undici` npm package (installed in Dockerfile).
if [ -f /app/scripts/proxy-bootstrap.js ] && [ -n "${HTTP_PROXY:-}" ]; then
    export NODE_OPTIONS="--require /app/scripts/proxy-bootstrap.js ${NODE_OPTIONS:-}"
fi

exec bwrap \
    --ro-bind / / \
    --dev /dev \
    --proc /proc \
    --tmpfs /workspace-shared \
    --bind "$SESSION_WORKSPACE" /home/usr \
    --bind "$SESSION_CLAUDE_DIR" /home/usr/.claude \
    --bind "$SESSION_TMP" /tmp \
    --unshare-pid \
    --die-with-parent \
    --setenv HOME /home/usr \
    -- \
    "$REAL_CLAUDE" "$@"
