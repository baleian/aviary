#!/usr/bin/env bash
# Aviary dev/deploy entrypoint. Single command, single compose project (`aviary`).
#
# Usage: setup-dev.sh [SUBCMD] [SCOPE] [-- <docker-compose args>...]
#   SUBCMD : dev (default) | build | deploy | run | down | clean | logs | ps
#   SCOPE  : all (default) | app | infra
#
# Examples
#   setup-dev.sh                    # dev all   — build + up + hot reload (auto override)
#   setup-dev.sh dev app            # dev app   — assumes infra is already up
#   setup-dev.sh build              # build all images
#   setup-dev.sh build app          # build only app images
#   setup-dev.sh deploy             # prod-mode up (no override)
#   setup-dev.sh run                # build + deploy all
#   setup-dev.sh run app            # rebuild + redeploy app, infra stays as-is
#   setup-dev.sh down               # stop+remove all
#   setup-dev.sh down app           # remove only app, infra preserved
#   setup-dev.sh clean              # down + drop volumes (all)
#   setup-dev.sh clean app          # down + drop runtime-workspace volume (app side)
#   setup-dev.sh logs api           # follow api logs
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

dev_files() {  # dev mode: app override included; scope=all also pulls compose.deps.yml.
  case "${1:-all}" in
    all)   echo "-f compose.yml -f compose.infra.yml -f compose.deps.yml -f compose.override.yml" ;;
    app)   echo "-f compose.yml -f compose.override.yml" ;;
    infra) echo "-f compose.infra.yml" ;;
    *)     echo "unknown scope: $1 (use all|app|infra)" >&2; exit 1 ;;
  esac
}

prod_files() {  # build/deploy/run/down/clean/logs/ps: no override; scope=all pulls deps.
  case "${1:-all}" in
    all)   echo "-f compose.yml -f compose.infra.yml -f compose.deps.yml" ;;
    app)   echo "-f compose.yml" ;;
    infra) echo "-f compose.infra.yml" ;;
    *)     echo "unknown scope: $1 (use all|app|infra)" >&2; exit 1 ;;
  esac
}

# --remove-orphans on partial scope would kill containers from the other
# scope (everything in the same project). Only attach it for scope=all.
orphans_flag() { [ "${1:-all}" = "all" ] && echo "--remove-orphans" || echo ""; }

cmd="${1:-dev}"
scope="${2:-all}"

# Drop the consumed args so $@ holds extra docker-compose passthrough args.
[ $# -gt 0 ] && shift || true
[ $# -gt 0 ] && shift || true

case "$cmd" in
  dev|"")
    docker compose $(dev_files  "$scope") up -d --build "$@"
    ;;
  build)
    docker compose $(prod_files "$scope") build "$@"
    ;;
  deploy)
    docker compose $(prod_files "$scope") up -d "$@"
    ;;
  run)
    docker compose $(prod_files "$scope") build
    docker compose $(prod_files "$scope") up -d "$@"
    ;;
  down)
    docker compose $(prod_files "$scope") down $(orphans_flag "$scope") "$@"
    ;;
  clean)
    docker compose $(prod_files "$scope") down -v $(orphans_flag "$scope") "$@"
    ;;
  logs)
    docker compose $(prod_files "$scope") logs -f "$@"
    ;;
  ps)
    docker compose $(prod_files "$scope") ps "$@"
    ;;
  *)
    echo "usage: $0 [dev|build|deploy|run|down|clean|logs|ps] [all|app|infra]" >&2
    exit 1
    ;;
esac
