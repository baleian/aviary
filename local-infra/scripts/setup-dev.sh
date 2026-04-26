#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

docker compose --profile k3s build
docker compose --profile k3s up -d

echo -n "Waiting for K3s..."
until docker compose --profile k3s exec -T k8s kubectl get nodes 2>/dev/null | grep -q " Ready"; do
  sleep 2
done
echo " ready."
