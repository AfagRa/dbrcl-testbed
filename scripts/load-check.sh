#!/usr/bin/env bash
# Phase 4 acceptance: run steady, diurnal and spike for 2 minutes each against
# the router (weight 0: all traffic on stable, the single-pod worst case),
# then check rate shape, error rate and capacity in Prometheus.
# The spike window and diurnal period are compressed to fit in 2 minutes.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PY=${PYTHON:-python3}
OUT=${OUT:-$ROOT/runs/load-check-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT"

kubectl -n monitoring port-forward svc/prometheus-server 19090:80 > /dev/null 2>&1 & PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
for _ in $(seq 30); do curl -s -o /dev/null http://127.0.0.1:19090/-/ready && break; sleep 0.5; done

fail=0
run() { # scenario, env overrides...
  local sc=$1; shift
  echo "== $sc ($*)"
  env "$@" RUN_DIR="$OUT/$sc" bash "$ROOT/scripts/load.sh" "$sc"
  sleep 12   # final scrapes
  "$PY" "$ROOT/scripts/load_shape.py" "$OUT/$sc" || fail=1
  sleep 20   # let rates settle before the next scenario
}
run steady  DURATION_S=120
run diurnal DURATION_S=120 PERIOD_S=120
run spike   DURATION_S=120 SPIKE_OFFSET_S=30 SPIKE_DURATION_S=60
echo "summaries in $OUT"
exit $fail
