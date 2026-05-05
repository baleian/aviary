# Shared helpers. Sourced, not run.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_INFRA_DIR="$PROJECT_DIR/local-infra"

VALID_GROUPS=(infra service)

infra_compose()   { (cd "$LOCAL_INFRA_DIR" && docker compose "$@"); }
service_compose() { (cd "$PROJECT_DIR"     && docker compose "$@"); }

ensure_env_symlink() {
  if [ ! -L "$LOCAL_INFRA_DIR/.env" ]; then
    rm -f "$LOCAL_INFRA_DIR/.env"
    ln -s ../.env "$LOCAL_INFRA_DIR/.env"
  fi
}

parse_groups() {
  local raw="${1:-}"
  if [ -z "$raw" ]; then
    _GROUPS=("${VALID_GROUPS[@]}")
    return
  fi
  IFS=',' read -ra _GROUPS <<< "$raw"
  for g in "${_GROUPS[@]}"; do
    case "$g" in
      infra|service) ;;
      *) echo "unknown group: $g (valid: ${VALID_GROUPS[*]})" >&2; exit 1 ;;
    esac
  done
}

has_group() {
  local target=$1
  for g in "${_GROUPS[@]}"; do [ "$g" = "$target" ] && return 0; done
  return 1
}
