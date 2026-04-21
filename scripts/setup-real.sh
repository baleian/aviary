#!/usr/bin/env bash
# Real-mode setup: all services run from their baked image (no bind-mounts,
# no --reload, web served from `next build` output). Mirrors setup-dev.sh but
# passes `-f docker-compose.yml` so docker-compose.override.yml is ignored.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=_common.sh
source "$SCRIPT_DIR/_common.sh"
cd "$PROJECT_DIR"

COMPOSE=(docker compose -f docker-compose.yml)

echo "=== Aviary Real (built) Environment Setup ==="

load_env_and_build_args

echo "[1/6] Building and starting Docker Compose services (no override)..."
"${COMPOSE[@]}" up -d --build

echo "[2/6] Waiting for PostgreSQL..."
until "${COMPOSE[@]}" exec -T postgres pg_isready -U aviary > /dev/null 2>&1; do
  sleep 1
done
echo "  PostgreSQL is ready."

echo "[3/6] Waiting for Keycloak..."
until curl -sf http://localhost:8080/realms/aviary/.well-known/openid-configuration > /dev/null 2>&1; do
  sleep 2
done
echo "  Keycloak is ready."

echo "[4/6] Waiting for K8s..."
until "${COMPOSE[@]}" exec -T k8s kubectl get nodes 2>/dev/null | grep -q " Ready"; do
  sleep 2
done
echo "  K8s is ready."

K8S_GATEWAY_IP=$("${COMPOSE[@]}" exec -T k8s ip route | awk '/default/ {print $3}' | head -1)
echo "  K8s gateway IP: $K8S_GATEWAY_IP"

echo "[5/6] Building and loading runtime images..."
docker build ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -t aviary-runtime:latest ./runtime/
docker save aviary-runtime:latest | "${COMPOSE[@]}" exec -T k8s ctr images import -
docker build ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -f ./runtime/Dockerfile.custom -t aviary-runtime-custom:latest ./runtime/
docker save aviary-runtime-custom:latest | "${COMPOSE[@]}" exec -T k8s ctr images import -

echo "  Rendering and applying Helm charts..."
HELM_IMAGE="alpine/helm:3.14.4"

render_chart() {
  local release=$1 chart=$2 values=$3
  docker run --rm -v "$PROJECT_DIR/charts:/charts:ro" "$HELM_IMAGE" template \
    "$release" "/charts/$chart" -f "/charts/$chart/$values" \
    --set hostGatewayIP="$K8S_GATEWAY_IP"
}

render_chart aviary-platform    aviary-platform    values-dev.yaml \
  | "${COMPOSE[@]}" exec -T k8s kubectl apply -f -
render_chart aviary-env-default aviary-environment values-dev.yaml \
  | "${COMPOSE[@]}" exec -T k8s kubectl apply -f -
render_chart aviary-env-custom  aviary-environment values-custom.yaml \
  | "${COMPOSE[@]}" exec -T k8s kubectl apply -f -

echo "  Platform + default + custom environments applied."

echo -n "  Waiting for default runtime rollout..."
"${COMPOSE[@]}" exec -T k8s kubectl -n agents rollout status deploy/aviary-env-default --timeout=180s
echo " ready."
echo -n "  Waiting for custom runtime rollout..."
"${COMPOSE[@]}" exec -T k8s kubectl -n agents rollout status deploy/aviary-env-custom --timeout=180s
echo " ready."

echo "[6/6] Waiting for application services..."
echo -n "  Temporal server..."
until curl -sf http://localhost:8233 > /dev/null 2>&1; do sleep 2; done
echo " ready."
echo -n "  Agent supervisor..."
until curl -sf http://localhost:9000/v1/health > /dev/null 2>&1; do sleep 2; done
echo " ready."
echo -n "  LiteLLM gateway..."
until curl -sf http://localhost:8090/health/liveliness > /dev/null 2>&1; do sleep 2; done
echo " ready."
echo -n "  API server..."
until curl -sf http://localhost:8000/api/health > /dev/null 2>&1; do sleep 2; done
echo " ready."
echo -n "  Admin console..."
until curl -sf http://localhost:8001/ > /dev/null 2>&1; do sleep 2; done
echo " ready."
echo -n "  Web UI..."
until curl -sf http://localhost:3000 > /dev/null 2>&1; do sleep 2; done
echo " ready."

echo ""
echo "=== Real (built) environment is ready ==="
echo ""
echo "  Web UI:       http://localhost:3000   (next build + next start, no HMR)"
echo "  API Server:   http://localhost:8000   (uvicorn, no --reload)"
echo "  Admin:        http://localhost:8001   (uvicorn, no --reload)"
echo "  Supervisor:   http://localhost:9000"
echo ""
echo "Source edits will NOT reflect live — rebuild with:"
echo "  ./scripts/quick-rebuild.sh real"
echo ""
echo "To return to dev (hot-reload) mode:"
echo "  docker compose down && ./scripts/setup-dev.sh"
