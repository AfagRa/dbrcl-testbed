#!/usr/bin/env bash
# Build all service images directly into the minikube node (no registry).
# Tag defaults to the git commit (+ "-dirty" if the tree has changes), so a
# run's meta.json commit hash identifies the exact images.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PROFILE=${MINIKUBE_PROFILE:-dbrcl}
TAG=${IMAGE_TAG:-$("$ROOT/scripts/image-tag.sh")}
SERVICES=${SERVICES:-"checkout payment router egress-sink"}

cd "$ROOT/services"
for s in $SERVICES; do
  echo "== building dbrcl/$s:$TAG"
  # -f is relative to the build context (services/)
  minikube -p "$PROFILE" image build -t "dbrcl/$s:$TAG" -f "$s/Dockerfile" .
done
echo "== images in minikube"
minikube -p "$PROFILE" image ls | grep "dbrcl/" | grep ":$TAG" || { echo "no images tagged $TAG found" >&2; exit 1; }
