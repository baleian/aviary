#!/usr/bin/env bash
# Run the e2e + API test suites against a supervisor configured with short
# scaling/idle-cleanup intervals so tests don't wait minutes for loops to fire.
#
# The main docker-compose ships with production-sensible defaults (30s scaling
# tick, 5 min idle interval, 7 day idle timeout). This script only overrides
# them for the supervisor's env and recreates that container; other services
# keep their normal config. The fast values stay in effect until the user
# restarts the stack without them — run `docker compose up -d --force-recreate
# agent-supervisor` to restore production defaults.

set -euo pipefail

# Values exposed to both docker-compose (supervisor container) and the test
# processes (so test code can size its waits accordingly).
export SCALING_CHECK_INTERVAL="${SCALING_CHECK_INTERVAL:-5}"
export IDLE_CLEANUP_INTERVAL="${IDLE_CLEANUP_INTERVAL:-10}"
export AGENT_IDLE_TIMEOUT="${AGENT_IDLE_TIMEOUT:-20}"

cd "$(dirname "$(readlink -f "$0")")/.."

echo "[test] supervisor env:"
echo "  SCALING_CHECK_INTERVAL=${SCALING_CHECK_INTERVAL}s"
echo "  IDLE_CLEANUP_INTERVAL=${IDLE_CLEANUP_INTERVAL}s"
echo "  AGENT_IDLE_TIMEOUT=${AGENT_IDLE_TIMEOUT}s"

echo "[test] recreating supervisor with fast intervals..."
docker compose up -d --force-recreate agent-supervisor

# Wait until the supervisor healthcheck flips to healthy.
printf "[test] waiting for supervisor to become healthy"
for _ in $(seq 1 60); do
  status=$(docker compose ps agent-supervisor --format '{{.Status}}' || true)
  if echo "$status" | grep -q "healthy"; then
    echo " — ready"
    break
  fi
  printf "."
  sleep 1
done

# Clean up any leftover agent containers from previous runs so tests start
# from a known-empty baseline.
docker ps --filter "label=aviary.managed=true" --format '{{.Names}}' \
  | xargs -r docker rm -f >/dev/null 2>&1 || true

echo "[test] running tests/e2e/test_api.py"
uv run python tests/e2e/test_api.py

echo "[test] running tests/e2e/test_supervisor.py"
uv run python tests/e2e/test_supervisor.py
