#!/usr/bin/env bash
# Host-side smoke test: builds payment, checkout and router, runs payment,
# a stable and a canary checkout and the router on localhost, exercises
# error_burst (Phase 1) and weight changes through the router (Phase 2).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
EXE=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) EXE=.exe;; esac
PIDS=()
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

(cd "$ROOT/services" && for s in payment checkout router; do go build -o "$WORK/$s$EXE" ./$s; done)

PORT=18081 POD_NAME=payment-local "$WORK/payment$EXE" > "$WORK/payment.log" 2>&1 & PIDS+=($!)
PORT=18080 VERSION=canary POD_NAME=checkout-canary-local PAYMENT_URL=http://127.0.0.1:18081 \
  MEMORY_LIMIT_BYTES=268435456 "$WORK/checkout$EXE" > "$WORK/canary.log" 2>&1 & PIDS+=($!)
PORT=18082 VERSION=stable POD_NAME=checkout-stable-local PAYMENT_URL=http://127.0.0.1:18081 \
  "$WORK/checkout$EXE" > "$WORK/stable.log" 2>&1 & PIDS+=($!)
PORT=18090 STABLE_URL=http://127.0.0.1:18082 CANARY_URL=http://127.0.0.1:18080 \
  "$WORK/router$EXE" > "$WORK/router.log" 2>&1 & PIDS+=($!)

C="curl -s --max-time 5"
for _ in $(seq 50); do
  $C -o /dev/null http://127.0.0.1:18080/healthz && $C -o /dev/null http://127.0.0.1:18082/healthz \
    && $C -o /dev/null http://127.0.0.1:18090/healthz && break
  sleep 0.2
done

CART='{"items":[{"sku":"book","qty":2,"price_cents":1299},{"sku":"pen","qty":3,"price_cents":150}]}'
burst() { for _ in $(seq 10); do $C -o /dev/null -w '%{http_code} ' -X POST http://127.0.0.1:18080/checkout -d "$CART"; done; echo; }

echo "== healthy checkout via canary";  $C -w ' HTTP %{http_code}\n' -X POST http://127.0.0.1:18080/checkout -d "$CART"
echo "== healthy checkout via stable";  $C -w ' HTTP %{http_code}\n' -X POST http://127.0.0.1:18082/checkout -d "$CART"
echo "== stable refuses fault";         $C -w ' HTTP %{http_code}\n' -X POST http://127.0.0.1:18082/admin/fault -d '{"fault":"error_burst","params":{"p":1}}'
echo "== canary: error_burst p=1";      $C -w ' HTTP %{http_code}\n' -X POST http://127.0.0.1:18080/admin/fault -d '{"fault":"error_burst","params":{"p":1}}'
echo "== 10 canary checkouts (fault on)";  burst
echo "== canary: fault none";           $C -w ' HTTP %{http_code}\n' -X POST http://127.0.0.1:18080/admin/fault -d '{"fault":"none"}'
echo "== 10 canary checkouts (fault off)"; burst
echo "== canary metrics"
$C http://127.0.0.1:18080/metrics | grep -E '^checkout_(requests_total|payment_errors_total|fault_active\{fault="error_burst")'
echo "== canary log (ground truth)"; cat "$WORK/canary.log"

echo
echo "== router: user-00000..user-00999 once per stage (counted from the router request log)"
for st in "0.01 1" "0.05 2" "0.25 3" "1.0 4"; do
  set -- $st
  $C -o /dev/null -X PUT http://127.0.0.1:18090/admin/weight -d "{\"weight\":$1,\"stage\":$2,\"reason\":\"smoke\"}"
  # one curl process, 1000 requests (a curl config file avoids Windows' argv limit)
  esc=${CART//\"/\\\"}
  for i in $(seq 0 999); do
    [ "$i" -gt 0 ] && echo next
    printf 'silent\nrequest = "POST"\nheader = "X-User-ID: user-%05d"\ndata = "%s"\nurl = "http://127.0.0.1:18090/checkout"\n' "$i" "$esc"
  done > "$WORK/curl.cfg"
  curl -K "$WORK/curl.cfg" > /dev/null
  n=$(grep '"event":"request"' "$WORK/router.log" | grep "\"stage\":$2," | grep -c '"version":"canary"' || true)
  ok=$(grep '"event":"request"' "$WORK/router.log" | grep "\"stage\":$2," | grep -c '"status":200' || true)
  echo "  w=$1 stage=$2 -> canary $n/1000, status 200: $ok/1000"
done
echo "== router: request without X-User-ID"
$C -o /dev/null -D - -X POST http://127.0.0.1:18090/checkout -d "$CART" | tr -d '\r' | grep -iE '^(HTTP|x-served-by|x-user-id)'
echo "== router: weight_change log"; grep '"weight_change"' "$WORK/router.log"
echo "== router: sample request log line"; grep '"event":"request"' "$WORK/router.log" | tail -n1
echo "== router metrics"
$C http://127.0.0.1:18090/metrics | grep -E '^router_(requests_total|canary_weight|slo_violations_total)'
