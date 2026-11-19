#!/usr/bin/env bash
# Install Prometheus (namespace monitoring) and the testbed chart (namespaces dbrcl, sink).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TAG=${IMAGE_TAG:-$("$ROOT/scripts/image-tag.sh")}
PROM_CHART_VERSION=${PROM_CHART_VERSION:-29.47.2}

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update prometheus-community >/dev/null

echo "== prometheus (chart $PROM_CHART_VERSION)"
helm upgrade --install prometheus prometheus-community/prometheus --version "$PROM_CHART_VERSION" \
  -n monitoring --create-namespace -f "$ROOT/deploy/prometheus/values.yaml" --wait --timeout 5m

echo "== dbrcl-testbed (image tag $TAG)"
helm upgrade --install dbrcl-testbed "$ROOT/deploy/helm/dbrcl-testbed" \
  -n dbrcl --create-namespace --set image.tag="$TAG" --wait --timeout 5m

kubectl -n dbrcl get pods -o wide
kubectl -n sink get pods -o wide
kubectl -n monitoring get pods
