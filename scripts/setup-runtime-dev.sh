#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_INFRA_DIR="$PROJECT_DIR/local-infra"
CHARTS_DIR="$PROJECT_DIR/charts"
HELM_IMAGE="alpine/helm:3.14.4"

infra() { (cd "$LOCAL_INFRA_DIR" && docker compose "$@"); }

BUILD_ARGS=()
for var in UV_INDEX_URL NPM_CONFIG_REGISTRY; do
  for env_file in "$PROJECT_DIR/.env" "$LOCAL_INFRA_DIR/.env"; do
    [ -f "$env_file" ] || continue
    val=$(set -a; source "$env_file" 2>/dev/null; printf '%s' "${!var:-}")
    if [ -n "$val" ]; then
      BUILD_ARGS+=(--build-arg "$var=$val")
      break
    fi
  done
done

K8S_GATEWAY_IP=$(infra --profile k3s exec -T k8s ip route | awk '/default/ {print $3}' | head -1)

render_chart() {
  local release=$1 chart=$2 values=$3
  docker run --rm -v "$CHARTS_DIR:/charts:ro" "$HELM_IMAGE" template \
    "$release" "/charts/$chart" -f "/charts/$chart/$values" \
    --set hostGatewayIP="$K8S_GATEWAY_IP"
}

load_image() {
  docker save "$1" | infra --profile k3s exec -T k8s ctr images import -
}

echo "[1/4] Building runtime images..."
docker build ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -t aviary-runtime:latest "$PROJECT_DIR/runtime/"
load_image aviary-runtime:latest
docker build ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -f "$PROJECT_DIR/runtime/Dockerfile.custom" -t aviary-runtime-custom:latest "$PROJECT_DIR/runtime/"
load_image aviary-runtime-custom:latest

echo "[2/4] Applying Helm charts..."
render_chart aviary-platform    aviary-platform    values-dev.yaml \
  | infra --profile k3s exec -T k8s kubectl apply -f -
render_chart aviary-env-default aviary-environment values-dev.yaml \
  | infra --profile k3s exec -T k8s kubectl apply -f -
render_chart aviary-env-custom  aviary-environment values-custom.yaml \
  | infra --profile k3s exec -T k8s kubectl apply -f -

# K3s leaves running pods alone when the image tag is unchanged (`Never`
# pull policy). Force a rolling restart so pods pick up the freshly
# imported `aviary-runtime:latest` content.
echo "[3/4] Restarting runtime pods..."
infra --profile k3s exec -T k8s kubectl -n agents rollout restart deploy/aviary-env-default
infra --profile k3s exec -T k8s kubectl -n agents rollout restart deploy/aviary-env-custom

echo "[4/4] Waiting for runtime rollouts..."
echo -n "  default..."
infra --profile k3s exec -T k8s kubectl -n agents rollout status deploy/aviary-env-default --timeout=180s
echo " ready."
echo -n "  custom..."
infra --profile k3s exec -T k8s kubectl -n agents rollout status deploy/aviary-env-custom --timeout=180s
echo " ready."
