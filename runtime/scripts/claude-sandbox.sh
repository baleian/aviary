#!/bin/bash
# Drop-in replacement for the `claude` binary (installed as /usr/local/bin/claude).
# Forwards to the generic sandbox wrapper with claude-real as the command.
# Sandbox flags live in sandbox.sh — keep only the binary choice here.

set -euo pipefail

exec /app/scripts/sandbox.sh "$(dirname "$0")/claude-real" "$@"
