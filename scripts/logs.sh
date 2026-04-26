#!/usr/bin/env bash
# Tail logs for one group. infra/service via docker compose, runtime via kubectl.
# Usage: logs.sh {infra|runtime|service}
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_lib.sh"

case "${1:-}" in
  infra)
    infra_compose logs -f
    ;;
  service)
    service_compose logs -f
    ;;
  runtime)
    k8s kubectl -n agents logs -f -l aviary/role=agent-runtime --max-log-requests=10 --tail=200
    ;;
  *)
    echo "usage: $0 {infra|runtime|service}" >&2
    exit 1
    ;;
esac
