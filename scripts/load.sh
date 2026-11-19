#!/usr/bin/env bash
# Run one k6 load scenario against the router.
#   scripts/load.sh <steady|diurnal|spike>
# Defaults come from config/experiment.yaml; any of DURATION_S, RPS, MIN_RPS,
# MAX_RPS, PERIOD_S, BASE_RPS, FACTOR, SPIKE_OFFSET_S, SPIKE_DURATION_S,
# UID_POOL, SEED can be overridden from env. Output goes to RUN_DIR
# (default runs/load-<scenario>-<utc>): k6_summary.json and load_window.json.
#
# k6 runs as a container on minikube's docker network ("--network <profile>")
# and targets the router NodePort directly: no port-forward tunnel, and the
# generator's CPU is outside the minikube node's budget.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SC=${1:?usage: load.sh <steady|diurnal|spike>}
PROFILE=${MINIKUBE_PROFILE:-dbrcl}
PY=${PYTHON:-python3}
K6_IMAGE=${K6_IMAGE:-grafana/k6:2.2.0}

eval "$("$PY" "$ROOT/scripts/load_env.py" "$SC")"
NODE_IP=$(minikube -p "$PROFILE" ip)
NODE_PORT=$(kubectl -n dbrcl get svc router -o jsonpath='{.spec.ports[0].nodePort}')
TARGET=${TARGET:-http://$NODE_IP:$NODE_PORT}

RUN_DIR=${RUN_DIR:-$ROOT/runs/load-$SC-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$RUN_DIR"
RUN_DIR=$(cd "$RUN_DIR" && pwd)

envs=()
for k in SCENARIO DURATION_S UID_POOL SEED RPS MIN_RPS MAX_RPS PERIOD_S BASE_RPS FACTOR SPIKE_OFFSET_S SPIKE_DURATION_S; do
  [ -n "${!k:-}" ] && envs+=(-e "$k=${!k}")
done
# k6 -e flags (not docker -e): k6 does not expose container env vars in __ENV.

echo "k6 $SC -> $TARGET for ${DURATION_S}s, output $RUN_DIR"
start=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
docker run --rm --network "$PROFILE" --user "$(id -u):$(id -g)" \
  -v "$ROOT/load/k6:/scripts:ro" -v "$RUN_DIR:/out" \
  "$K6_IMAGE" run --quiet --no-usage-report \
  "${envs[@]}" -e TARGET="$TARGET" -e SUMMARY_PATH=/out/k6_summary.json /scripts/load.js
end=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)

# Host-clock window, only used to choose Prometheus query ranges; results use
# in-cluster timestamps.
printf '{"scenario":"%s","target":"%s","k6_image":"%s","start":"%s","end":"%s"}\n' \
  "$SC" "$TARGET" "$K6_IMAGE" "$start" "$end" > "$RUN_DIR/load_window.json"
