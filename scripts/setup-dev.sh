#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

echo "=== Aviary Dev Environment Setup (Phase 1) ==="

if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$PROJECT_DIR/.env"
  set +a
fi

BUILD_ARGS=()
[ -n "${UV_INDEX_URL:-}" ]        && BUILD_ARGS+=(--build-arg "UV_INDEX_URL=$UV_INDEX_URL")
[ -n "${NPM_CONFIG_REGISTRY:-}" ] && BUILD_ARGS+=(--build-arg "NPM_CONFIG_REGISTRY=$NPM_CONFIG_REGISTRY")

# 1. Tear down any previous stack (keep volumes → DB / Vault / workspaces preserved).
#    Supervisor-spawned agent runtime containers aren't compose-owned, so bring
#    them down separately by label before `compose down`.
echo "[1/4] Tearing down existing stack (volumes preserved)..."
managed=$(docker ps -aq --filter "label=aviary.managed=true" || true)
if [ -n "$managed" ]; then
  docker rm -f $managed > /dev/null
  echo "  Removed $(echo "$managed" | wc -l) runtime container(s)."
fi
docker compose down --remove-orphans > /dev/null 2>&1 || true

# 2. Build runtime image
echo "[2/4] Building runtime image..."
docker build "${BUILD_ARGS[@]}" -t aviary-runtime:latest ./runtime/
echo "  Runtime image built."

# 3. Build and start compose services
echo "[3/4] Building and starting Docker Compose services..."
docker compose up -d --build

# 4. Wait for services
echo "[4/4] Waiting for services..."
echo -n "  PostgreSQL..."
until docker compose exec -T postgres pg_isready -U aviary > /dev/null 2>&1; do
  sleep 1
done
echo " ready."

echo -n "  Agent Supervisor..."
until curl -sf http://localhost:9000/v1/health > /dev/null 2>&1; do
  sleep 2
done
echo " ready."

echo ""
echo "=== Dev environment is ready! ==="
echo ""
echo "Services:"
echo "  API:               http://localhost:8000"
echo "  Agent Supervisor:  http://localhost:9000"
echo "  MCP Gateway:       http://localhost:8100"
echo "  LiteLLM:           http://localhost:4000"
echo ""
echo "Infrastructure:"
echo "  PostgreSQL:  localhost:5432  (aviary/aviary)"
echo "  Redis:       localhost:6379"
echo "  Keycloak:    localhost:8180  (dev/dev)"
echo "  Vault:       localhost:8200  (dev-root-token)"
echo ""
echo "Runtime image: aviary-runtime:latest"
echo "  Containers are created dynamically by the supervisor."
echo ""
echo "Hot reload:"
echo "  Edit files in agent-supervisor/ or shared/"
echo "  — changes apply automatically via bind-mount."
echo "  If you change runtime/:"
echo "    docker build -t aviary-runtime:latest ./runtime/"
