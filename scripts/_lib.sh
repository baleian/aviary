# Shared helpers. Sourced, not run.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_INFRA_DIR="$PROJECT_DIR/local-infra"
CHARTS_DIR="$PROJECT_DIR/charts"
HELM_IMAGE="alpine/helm:3.14.4"

VALID_GROUPS=(infra service)

infra_compose()   { (cd "$LOCAL_INFRA_DIR" && docker compose --profile k3s "$@"); }
service_compose() { (cd "$PROJECT_DIR"     && docker compose "$@"); }
k8s()             { infra_compose exec -T k8s "$@"; }

ensure_env_symlink() {
  if [ ! -L "$LOCAL_INFRA_DIR/.env" ]; then
    rm -f "$LOCAL_INFRA_DIR/.env"
    ln -s ../.env "$LOCAL_INFRA_DIR/.env"
  fi
}

ensure_config_yaml() {
  local target="$PROJECT_DIR/config.yaml"
  if [ -d "$target" ]; then
    echo "config.yaml exists as a directory — refusing to touch it. Inspect and remove manually." >&2
    exit 1
  fi
  [ -e "$target" ] || cp "$PROJECT_DIR/config.example.yaml" "$target"
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

k3s_running() {
  infra_compose ps --status running --services 2>/dev/null | grep -qx k8s
}

render_chart() {
  local release=$1 chart=$2 values=$3 gateway_ip=$4
  shift 4
  docker run --rm -v "$CHARTS_DIR:/charts:ro" "$HELM_IMAGE" template \
    "$release" "/charts/$chart" -f "/charts/$chart/$values" \
    --set hostGatewayIP="$gateway_ip" \
    "$@"
}

collect_build_args() {
  BUILD_ARGS=()
  [ -f "$PROJECT_DIR/.env" ] || return 0
  for v in UV_INDEX_URL NPM_CONFIG_REGISTRY; do
    val=$(set -a; source "$PROJECT_DIR/.env" 2>/dev/null; printf '%s' "${!v:-}")
    if [ -n "$val" ]; then BUILD_ARGS+=(--build-arg "$v=$val"); fi
  done
}

LOAD_CACHE_FILE="${LOAD_CACHE_FILE:-$HOME/.cache/aviary/loaded-images.txt}"

# Build then ctr-import; skip import when the local image digest is unchanged.
build_and_load_image() {
  local image=$1 dockerfile=$2 context=$3
  shift 3
  echo "[build] $image"
  docker build "$@" ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} \
    -f "$PROJECT_DIR/$dockerfile" -t "$image" "$PROJECT_DIR/$context"

  mkdir -p "$(dirname "$LOAD_CACHE_FILE")"
  touch "$LOAD_CACHE_FILE"
  local digest
  digest=$(docker inspect --format='{{.Id}}' "$image")
  if grep -qFx "${image}=${digest}" "$LOAD_CACHE_FILE"; then
    echo "[load] $image — unchanged, skipping ctr import"
    return 0
  fi
  echo "[load] $image → k3s containerd"
  docker save "$image" | k8s ctr images import -
  grep -v "^${image}=" "$LOAD_CACHE_FILE" > "$LOAD_CACHE_FILE.tmp" || true
  echo "${image}=${digest}" >> "$LOAD_CACHE_FILE.tmp"
  mv "$LOAD_CACHE_FILE.tmp" "$LOAD_CACHE_FILE"
}

helm_apply() {
  local release=$1 chart=$2 values=$3
  shift 3
  render_chart "$release" "$chart" "$values" "${K8S_GATEWAY_IP:-}" "$@" \
    | k8s kubectl apply -f -
}

helm_delete() {
  local release=$1 chart=$2 values=$3
  render_chart "$release" "$chart" "$values" "${K8S_GATEWAY_IP:-}" \
    | k8s kubectl delete --ignore-not-found -f - || true
}

wait_rollout() {
  local ns=$1 deploy=$2 timeout=${3:-180s}
  k8s kubectl -n "$ns" rollout status "deploy/$deploy" --timeout="$timeout"
}

is_selected() {
  local chart=$1
  if [ -n "${SKIP_CHARTS:-}" ]; then
    for s in $SKIP_CHARTS; do [ "$s" = "$chart" ] && return 1; done
  fi
  if [ -n "${ONLY_CHARTS:-}" ]; then
    for s in $ONLY_CHARTS; do [ "$s" = "$chart" ] && return 0; done
    return 1
  fi
  return 0
}
