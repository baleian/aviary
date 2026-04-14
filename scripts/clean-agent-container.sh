#!/usr/bin/env bash
set -euo pipefail

echo "Removing supervisor-spawned agent containers…"
managed=$(docker ps -aq --filter "label=aviary.managed=true" || true)
if [ -n "$managed" ]; then
  docker rm -f $managed
else
  echo "  (none)"
fi
