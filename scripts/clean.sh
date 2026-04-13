#!/usr/bin/env bash
# Tear the dev stack down completely — compose services, their volumes,
# and the supervisor-spawned agent runtime containers (which are created
# via the Docker API, not compose, so `docker compose down -v` alone
# leaves them running).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$(dirname "$SCRIPT_DIR")"

echo "[1/2] Removing supervisor-spawned agent containers…"
managed=$(docker ps -aq --filter "label=aviary.managed=true" || true)
if [ -n "$managed" ]; then
  docker rm -f $managed
else
  echo "  (none)"
fi

echo "[2/2] docker compose down -v…"
docker compose down -v "$@"
