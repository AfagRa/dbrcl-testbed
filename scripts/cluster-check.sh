#!/usr/bin/env bash
# Phase 3 acceptance: checkout through the router, then the PromQL from
# docs/promql.md must return data for stable and canary. Leaves the router at
# weight 0 and the canary fault off.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PY=${PYTHON:-python}
WORK=$(mktemp -d)
PIDS=()
cleanup() {
  curl -s -o /dev/null -X PUT http://127.0.0.1:18090/admin/weight -d '{"weight":0,"stage":0,"reason":"cluster-check end"}' || true
  curl -s -o /dev/null -X POST http://127.0.0.1:18091/admin/fault -d '{"fault":"none"}' || true
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
}
trap cleanup EXIT

kubectl -n dbrcl port-forward svc/router 18090:8080 > /dev/null 2>&1 & PIDS+=($!)
kubectl -n dbrcl port-forward svc/checkout-canary 18091:8080 > /dev/null 2>&1 & PIDS+=($!)
kubectl -n monitoring port-forward svc/prometheus-server 19090:80 > /dev/null 2>&1 & PIDS+=($!)
for _ in $(seq 50); do
  curl -s -o /dev/null http://127.0.0.1:18090/healthz && curl -s -o /dev/null http://127.0.0.1:19090/-/ready \
    && curl -s -o /dev/null http://127.0.0.1:18091/healthz && break
  sleep 0.3
done

CART='{"items":[{"sku":"book","qty":2,"price_cents":1299}]}'
echo "== checkout through the router"
curl -s -D - -X POST http://127.0.0.1:18090/checkout -H 'X-User-ID: user-00042' -d "$CART" | tr -d '\r' | grep -E '^(HTTP|X-Served-By)|order_id'

echo "== traffic: weight 0.5, user-00000..user-01199 for ~60 s"
curl -s -o /dev/null -X PUT http://127.0.0.1:18090/admin/weight -d '{"weight":0.5,"stage":0,"reason":"cluster-check"}'
esc=${CART//\"/\\\"}
for i in $(seq 0 1199); do
  [ "$i" -gt 0 ] && echo next
  printf 'silent\nrequest = "POST"\nheader = "X-User-ID: user-%05d"\ndata = "%s"\nurl = "http://127.0.0.1:18090/checkout"\n' "$i" "$esc"
done > "$WORK/curl.cfg"
end=$((SECONDS + 60))
while [ $SECONDS -lt $end ]; do curl -K "$WORK/curl.cfg" > /dev/null; done

echo "== unexpected_egress p=1 on the canary for 10 s (sink must see the canary pod IP)"
curl -s -o /dev/null -X POST http://127.0.0.1:18091/admin/fault -d '{"fault":"unexpected_egress","params":{"p":1}}'
end=$((SECONDS + 10))
while [ $SECONDS -lt $end ]; do curl -s -o /dev/null -X POST http://127.0.0.1:18091/checkout -d "$CART"; done
curl -s -o /dev/null -X POST http://127.0.0.1:18091/admin/fault -d '{"fault":"none"}'
canary_ip=$(kubectl -n dbrcl get pod -l app=checkout,track=canary -o jsonpath='{.items[0].status.podIP}')
echo "canary pod IP: $canary_ip"
sleep 12   # let Prometheus scrape the sink

echo "== PromQL (http://127.0.0.1:19090)"
q() { "$PY" "$ROOT/scripts/promq.py" "$1" "$2"; }  # name, query
fail=0
q "request rate by version (rps)" 'sum by (version) (rate(router_requests_total[1m]))' || fail=1
q "error rate by version" '(sum by (version) (rate(router_requests_total{code=~"5.."}[1m])) or 0 * sum by (version) (rate(router_requests_total[1m]))) / sum by (version) (rate(router_requests_total[1m]))' || fail=1
q "SLO violation rate by version" 'sum by (version) (rate(router_slo_violations_total[1m])) / sum by (version) (rate(router_requests_total[1m]))' || fail=1
q "checkout P99 by version (s)" 'histogram_quantile(0.99, sum by (le, version) (rate(checkout_request_duration_seconds_bucket[1m])))' || fail=1
q "router P99 by version (s)" 'histogram_quantile(0.99, sum by (le, version) (rate(router_request_duration_seconds_bucket[1m])))' || fail=1
q "memory working set (bytes)" 'label_replace(max by (namespace, pod, container) (container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}), "version", "$1", "pod", "checkout-(stable|canary)-.*")' || fail=1
q "memory fraction of limit" 'label_replace(max by (namespace, pod, container) (container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}) / on (namespace, pod, container) max by (namespace, pod, container) (container_spec_memory_limit_bytes{namespace="dbrcl",container="checkout"}), "version", "$1", "pod", "checkout-(stable|canary)-.*")' || fail=1
q "memory slope (bytes/s)" 'label_replace(max by (namespace, pod, container) (deriv(container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}[2m])), "version", "$1", "pod", "checkout-(stable|canary)-.*")' || fail=1
q "CPU usage (cores)" 'label_replace(sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{namespace="dbrcl",container="checkout"}[1m])), "version", "$1", "pod", "checkout-(stable|canary)-.*")' || fail=1
q "CPU fraction of limit" 'label_replace(sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{namespace="dbrcl",container="checkout"}[1m])) / on (namespace, pod, container) (max by (namespace, pod, container) (container_spec_cpu_quota{namespace="dbrcl",container="checkout"}) / max by (namespace, pod, container) (container_spec_cpu_period{namespace="dbrcl",container="checkout"})), "version", "$1", "pod", "checkout-(stable|canary)-.*")' || fail=1
q "sink connections by source (2m)" 'sum by (source_ip, port) (increase(sink_connections_total[2m]))' || fail=1
q "router canary weight" 'max(router_canary_weight)' || fail=1

echo "== sink log (last 3 lines)"
kubectl -n sink logs deploy/egress-sink --tail=3
sinklog=$(kubectl -n sink logs deploy/egress-sink)   # no grep -q on a pipe: pipefail + SIGPIPE
if grep -q "\"source_ip\":\"$canary_ip\"" <<< "$sinklog"; then
  echo "  [OK]   sink saw connections from the canary pod IP $canary_ip"
else
  echo "  [FAIL] sink saw no connection from $canary_ip"; fail=1
fi
exit $fail
