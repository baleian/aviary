#!/usr/bin/env bash
# Restart stopped compose containers. K8s lives in local-deploy.sh.
# Usage: start-dev.sh [infra|service|<csv>]   (no arg → both groups)
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"
parse_groups "${1:-}"
ensure_env_symlink

if has_group infra; then
  echo "[infra] starting local-infra..."
  infra_compose start
fi

if has_group service; then
  echo "[service] starting services..."
  service_compose start
fi
