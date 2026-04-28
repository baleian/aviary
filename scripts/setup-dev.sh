#!/usr/bin/env bash
# Build and (re)start the compose stacks. K8s lives in local-deploy.sh.
# Usage: setup-dev.sh [infra|service|<csv>]   (no arg → both groups)
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"
parse_groups "${1:-}"
ensure_env_symlink
ensure_config_yaml

# Infra first — service dials postgres/redis/temporal via host.docker.internal.
if has_group infra; then
  echo "[infra] building & starting local-infra..."
  infra_compose build
  infra_compose up -d
fi

if has_group service; then
  echo "[service] building & starting services..."
  service_compose build
  service_compose up -d
fi
