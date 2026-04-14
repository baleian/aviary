#!/bin/bash
# Generic bwrap sandbox wrapper — the single source of truth for the
# per-session mount namespace. Callers pass the command + args to exec inside.
#
# Creates a mount namespace where:
#   - / is the host filesystem (read-only)
#   - $SESSION_WORKSPACE is bind-mounted to /workspace (shared session workspace)
#   - $SESSION_CLAUDE_DIR is bind-mounted to /workspace/.claude (per-agent overlay)
#   - $SESSION_VENV_DIR  is bind-mounted to /workspace/.venv  (per-session Python venv, optional)
#   - $SESSION_TMP is bind-mounted to /tmp (per-agent temp, NOT shared)
#   - PID namespace isolated
#
# Usage:
#   SESSION_WORKSPACE=... SESSION_CLAUDE_DIR=... SESSION_TMP=... [SESSION_VENV_DIR=...] \
#     sandbox.sh <command> [args...]
#
# If SESSION_WORKSPACE is not set, runs the command without a sandbox
# (direct CLI usage, not a real session).
#
# When SESSION_VENV_DIR is set, bootstraps a persistent Python venv at that
# path (outside bwrap so shebangs resolve against the real path; paths are
# rewritten to /workspace/.venv so the venv still works post-bind).

set -euo pipefail

if [ -z "${SESSION_WORKSPACE:-}" ]; then
    exec "$@"
fi

mkdir -p "$SESSION_WORKSPACE"
mkdir -p "${SESSION_CLAUDE_DIR:?SESSION_CLAUDE_DIR must be set}"
mkdir -p "${SESSION_TMP:?SESSION_TMP must be set}"

VENV_ENV=()
VENV_BIND=()
if [ -n "${SESSION_VENV_DIR:-}" ]; then
    mkdir -p "$SESSION_VENV_DIR"
    VENV_HOST="$SESSION_VENV_DIR"
    VENV_GUEST="/workspace/.venv"
    if [ ! -x "$VENV_HOST/bin/python" ]; then
        rm -rf "$VENV_HOST"
        if python3 -m venv "$VENV_HOST" 2>/dev/null; then
            for f in "$VENV_HOST/bin"/pip* "$VENV_HOST/bin"/activate* "$VENV_HOST/bin"/Activate*; do
                [ -f "$f" ] && sed -i "s|$VENV_HOST|$VENV_GUEST|g" "$f"
            done
        fi
    fi
    if [ -x "$VENV_HOST/bin/python" ]; then
        VENV_ENV=(
            --setenv VIRTUAL_ENV "$VENV_GUEST"
            --setenv PATH "$VENV_GUEST/bin:/usr/local/bin:/usr/bin:/bin"
            --setenv PIP_CACHE_DIR /workspace/.cache/pip
            --setenv NPM_CONFIG_CACHE /workspace/.cache/npm
        )
        VENV_BIND=(--bind "$VENV_HOST" "$VENV_GUEST")
    fi
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
    "$@"
