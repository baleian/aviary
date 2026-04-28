#!/usr/bin/env bash
# Tail compose logs. K8s: local-deploy.sh logs <chart>.
# Usage: logs.sh {infra|service}
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"

case "${1:-}" in
  infra)
    infra_compose logs -f
    ;;
  service)
    service_compose logs -f
    ;;
  *)
    echo "usage: $0 {infra|service}" >&2
    echo "(K8s pods: ./scripts/local-deploy.sh logs <chart>)" >&2
    exit 1
    ;;
esac
