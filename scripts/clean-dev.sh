#!/usr/bin/env bash
# Wipe compose stacks + volumes. K8s state: local-deploy.sh clean.
# Usage: clean-dev.sh [infra|service|<csv>]   (no arg → both groups)
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"
parse_groups "${1:-}"

if has_group service; then
  echo "[service] removing services and volumes..."
  service_compose down -v --remove-orphans
fi

if has_group infra; then
  echo "[infra] removing local-infra and volumes..."
  infra_compose down -v --remove-orphans
fi
