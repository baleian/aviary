#!/usr/bin/env bash
# Stop compose containers (volumes preserved). K8s lives in local-deploy.sh.
# Usage: stop-dev.sh [infra|service|<csv>]   (no arg → both groups)
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"
parse_groups "${1:-}"

if has_group infra; then
  echo "[infra] stopping local-infra..."
  infra_compose stop
fi

if has_group service; then
  echo "[service] stopping services..."
  service_compose stop
fi
