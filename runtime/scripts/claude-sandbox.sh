#!/bin/bash
# claude-sandbox.sh — Drop-in replacement for the `claude` binary.
#
# This script IS the `claude` command. The real binary lives at `claude-real`.
# SDK invokes `claude` → hits this wrapper → bwrap namespace → `claude-real`.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE is bind-mounted to /workspace (shared across all
#     agents collaborating on the same session)
#   - $SESSION_CLAUDE_DIR is bind-mounted to /workspace/.claude (CLI state
#     overlay for this session)
#   - $SESSION_VENV_DIR   is bind-mounted to /workspace/.venv  (Python venv
#     overlay for this session)
#   - $SESSION_TMP is bind-mounted to /tmp (per-session, NOT shared)
#   - PID namespace isolated
#
# Also bootstraps a per-session Python venv at /workspace/.venv on the first
# turn so the agent can `pip install foo` and have packages persist across
# turns. The pip download cache lives in the shared workspace so sibling
# agents can reuse fetched wheels without going back to the network.
#
# If SESSION_WORKSPACE is not set (e.g. direct CLI usage), runs without sandbox.

set -euo pipefail

REAL_CLAUDE="$(dirname "$0")/claude-real"

if [ -z "${SESSION_WORKSPACE:-}" ]; then
    exec "$REAL_CLAUDE" "$@"
fi

mkdir -p "$SESSION_WORKSPACE"
mkdir -p "${SESSION_CLAUDE_DIR:?SESSION_CLAUDE_DIR must be set}"
mkdir -p "${SESSION_VENV_DIR:?SESSION_VENV_DIR must be set}"
mkdir -p "${SESSION_TMP:?SESSION_TMP must be set}"

# ── Per-session Python venv ───────────────────────────────────
# The venv lives at $SESSION_VENV_DIR (on the shared agents namespace
# workspace volume) and is bwrap-bound to /workspace/.venv inside the
# sandbox. It's created OUTSIDE bwrap so its pip shebangs and activate
# scripts hard-code the host path — we rewrite those to the in-sandbox
# path so `pip` and `source activate` work correctly once bwrap remaps
# the mount.
VENV_HOST="$SESSION_VENV_DIR"
VENV_GUEST="/workspace/.venv"
# Treat the venv as missing unless it has a usable python — covers both
# "never created" and "empty mountpoint left over from a partial run".
if [ ! -x "$VENV_HOST/bin/python" ]; then
    rm -rf "$VENV_HOST"
    if python3 -m venv "$VENV_HOST" 2>/dev/null; then
        for f in "$VENV_HOST/bin"/pip* "$VENV_HOST/bin"/activate* "$VENV_HOST/bin"/Activate*; do
            [ -f "$f" ] && sed -i "s|$VENV_HOST|$VENV_GUEST|g" "$f"
        done
    fi
fi

VENV_ENV=()
VENV_BIND=()
if [ -x "$VENV_HOST/bin/python" ]; then
    VENV_ENV=(
        --setenv VIRTUAL_ENV "$VENV_GUEST"
        --setenv PATH "$VENV_GUEST/bin:$PATH"
        # pip download cache lives in the session workspace so sibling
        # agents collaborating on the same session can reuse already-fetched
        # wheels without going back to the network.
        --setenv PIP_CACHE_DIR /workspace/.cache/pip
    )
    VENV_BIND=(--bind "$VENV_HOST" "$VENV_GUEST")
fi

exec bwrap \
    --ro-bind / / \
    --dev /dev \
    --proc /proc \
    --tmpfs /workspace-shared \
    --bind "$SESSION_WORKSPACE" /workspace \
    --bind "$SESSION_CLAUDE_DIR" /workspace/.claude \
    "${VENV_BIND[@]}" \
    --bind "$SESSION_TMP" /tmp \
    --unshare-pid \
    --die-with-parent \
    --setenv HOME /workspace \
    "${VENV_ENV[@]}" \
    -- \
    "$REAL_CLAUDE" "$@"
