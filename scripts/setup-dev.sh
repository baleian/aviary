#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

echo "=== Aviary Dev Environment Setup ==="

# Load .env for registry settings (UV_INDEX_URL, NPM_CONFIG_REGISTRY)
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$PROJECT_DIR/.env"
  set +a
fi

# Collect --build-arg flags from registry env vars
BUILD_ARGS=()
[ -n "${UV_INDEX_URL:-}" ]        && BUILD_ARGS+=(--build-arg "UV_INDEX_URL=$UV_INDEX_URL")
[ -n "${NPM_CONFIG_REGISTRY:-}" ] && BUILD_ARGS+=(--build-arg "NPM_CONFIG_REGISTRY=$NPM_CONFIG_REGISTRY")

# Prepare the supervisor SA credential directory so docker-compose's read-only
# bind-mount target exists before `docker compose up` (setup populates it later).
mkdir -p "$PROJECT_DIR/config/agent-supervisor"

# 1. Build and start all Docker Compose services
echo "[1/7] Building and starting Docker Compose services..."
docker compose up -d --build

# 2. Wait for PostgreSQL
echo "[2/7] Waiting for PostgreSQL..."
until docker compose exec -T postgres pg_isready -U aviary > /dev/null 2>&1; do
  sleep 1
done
echo "  PostgreSQL is ready."

# 3. Wait for Keycloak
echo "[3/7] Waiting for Keycloak..."
until curl -sf http://localhost:8080/realms/aviary/.well-known/openid-configuration > /dev/null 2>&1; do
  sleep 2
done
echo "  Keycloak is ready."

# 4. Wait for K8s
echo "[4/7] Waiting for K8s..."
until docker compose exec -T k8s kubectl get nodes 2>/dev/null | grep -q " Ready"; do
  sleep 2
done
echo "  K8s is ready."

# Resolve K8s gateway IP (docker host as seen from K8s cluster)
K8S_GATEWAY_IP=$(docker compose exec -T k8s ip route | awk '/default/ {print $3}' | head -1)
echo "  K8s gateway IP: $K8S_GATEWAY_IP"

# 5. Build the runtime image and load it into K3s. K3s in local dev only hosts
# runtime pool pods — supervisor/api/admin all run in docker-compose.
echo "[5/7] Building runtime image..."
docker build "${BUILD_ARGS[@]}" -t aviary-runtime:latest ./runtime/

echo "  Loading runtime image into K8s..."
docker save aviary-runtime:latest | docker compose exec -T k8s ctr images import -
echo "  Runtime image loaded."

# 6. Apply K8s manifests and extract a supervisor SA token.
echo "[6/7] Applying K8s resources..."
docker compose exec -T k8s kubectl apply -f - < ./k8s/platform/namespace.yaml
docker compose exec -T k8s kubectl apply -f - < ./k8s/platform/agent-supervisor.yaml

# Pool catalog — infra-team-owned runtime pools.
# `_shared-workspace.yaml` sorts first (leading `_`), so the PVC exists before
# the pool Deployments that mount it.
for f in ./k8s/platform/pools/*.yaml; do
  HOST_GATEWAY_IP="$K8S_GATEWAY_IP" envsubst '${HOST_GATEWAY_IP}' < "$f" \
    | docker compose exec -T k8s kubectl apply -f -
done

# Seed the shared workspace root on the K8s node so the hostPath PV has a
# valid directory with correct ownership (runtime runs as UID 1000).
docker compose exec -T k8s sh -c \
  'mkdir -p /var/lib/aviary/agent-platform && chown 1000:1000 /var/lib/aviary/agent-platform'

# Provision supervisor SA credentials for docker-compose. `kubectl create token`
# issues a short-ish TGT-style token; 8760h = one year is plenty for local dev.
echo "  Extracting agent-supervisor ServiceAccount token..."
docker compose exec -T k8s kubectl create token agent-supervisor -n agents --duration=8760h \
  > "$PROJECT_DIR/config/agent-supervisor/token"
docker compose exec -T k8s cat /var/lib/rancher/k3s/server/tls/server-ca.crt \
  > "$PROJECT_DIR/config/agent-supervisor/ca.crt"
# supervisor container reads these via read-only bind-mount; restart it so the
# new token is picked up on the next `new_k8s_client()` call (file read is lazy
# but restart guarantees a clean state).
docker compose restart agent-supervisor > /dev/null
echo "  Supervisor credentials written; service restarted."

docker compose exec -T k8s kubectl rollout restart deployment -n agents \
  -l aviary/role=agent-runtime 2>/dev/null || true
echo "  agents namespace ready."

# 7. Wait for application services
echo "[7/7] Waiting for application services..."
echo -n "  Agent supervisor..."
until curl -sf http://localhost:9000/health > /dev/null 2>&1; do
  sleep 2
done
echo " ready."
echo -n "  LiteLLM gateway..."
until curl -sf http://localhost:8090/health/liveliness > /dev/null 2>&1; do
  sleep 2
done
echo " ready."
echo -n "  API server..."
until curl -sf http://localhost:8000/api/health > /dev/null 2>&1; do
  sleep 2
done
echo " ready."
echo -n "  Web UI..."
until curl -sf http://localhost:3000 > /dev/null 2>&1; do
  sleep 2
done
echo " ready."

echo ""
echo "=== Dev environment is ready! ==="
echo ""
echo "Application:"
echo "  Web UI:            http://localhost:3000"
echo "  API Server:        http://localhost:8000"
echo "  API Health:        http://localhost:8000/api/health"
echo ""
echo "Platform Services:"
echo "  Agent Supervisor:  http://localhost:9000  (docker-compose)"
echo "  LiteLLM Gateway:   http://localhost:8090"
echo "  MCP Gateway:       http://localhost:8100"
echo ""
echo "Infrastructure:"
echo "  PostgreSQL:  localhost:5432  (aviary/aviary)"
echo "  Redis:       localhost:6379"
echo "  Keycloak:    http://localhost:8080  (admin/admin)"
echo "  Vault:       http://localhost:8200  (token: dev-root-token)"
echo "  K8s API:     https://localhost:6443  (hosts runtime pool pods only)"
echo ""
echo "Test users (Keycloak):"
echo "  admin@test.com / password  (platform_admin, team: engineering)"
echo "  user1@test.com / password  (regular_user,   team: engineering, product)"
echo "  user2@test.com / password  (regular_user,   team: data-science)"
echo ""
echo "Hot reload:"
echo "  Edit files in api/, web/, admin/, or agent-supervisor/"
echo "  — changes apply automatically via bind-mount."
echo "  If you change dependencies:"
echo "    docker compose up -d --build <service>"
echo "  To rebuild the runtime image (after runtime/ changes):"
echo "    docker build -t aviary-runtime:latest ./runtime/"
echo "    docker save aviary-runtime:latest | docker compose exec -T k8s ctr images import -"
echo "    docker compose exec -T k8s kubectl rollout restart deployment -n agents -l aviary/role=agent-runtime"
