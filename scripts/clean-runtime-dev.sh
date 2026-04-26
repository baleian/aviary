#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_INFRA_DIR="$PROJECT_DIR/local-infra"
CHARTS_DIR="$PROJECT_DIR/charts"
HELM_IMAGE="alpine/helm:3.14.4"

infra() { (cd "$LOCAL_INFRA_DIR" && docker compose "$@"); }

K8S_GATEWAY_IP=$(infra --profile k3s exec -T k8s ip route | awk '/default/ {print $3}' | head -1)

render_chart() {
  local release=$1 chart=$2 values=$3
  docker run --rm -v "$CHARTS_DIR:/charts:ro" "$HELM_IMAGE" template \
    "$release" "/charts/$chart" -f "/charts/$chart/$values" \
    --set hostGatewayIP="$K8S_GATEWAY_IP"
}

echo "Removing runtime resources..."
render_chart aviary-env-custom  aviary-environment values-custom.yaml \
  | infra --profile k3s exec -T k8s kubectl delete --ignore-not-found -f -
render_chart aviary-env-default aviary-environment values-dev.yaml \
  | infra --profile k3s exec -T k8s kubectl delete --ignore-not-found -f -
render_chart aviary-platform    aviary-platform    values-dev.yaml \
  | infra --profile k3s exec -T k8s kubectl delete --ignore-not-found -f -
