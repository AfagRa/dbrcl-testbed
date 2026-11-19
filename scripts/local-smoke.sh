#!/usr/bin/env bash
# Phase 1 smoke test on the host: builds payment and checkout, runs payment,
# a stable and a canary checkout on localhost, and exercises error_burst.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
EXE=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) EXE=.exe;; esac
PIDS=()
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

(cd "$ROOT/services" && go build -o "$WORK/payment$EXE" ./payment && go build -o "$WORK/checkout$EXE" ./checkout)

PORT=18081 POD_NAME=payment-local "$WORK/payment$EXE" > "$WORK/payment.log" 2>&1 & PIDS+=($!)
PORT=18080 VERSION=canary POD_NAME=checkout-canary-local PAYMENT_URL=http://127.0.0.1:18081 \
  MEMORY_LIMIT_BYTES=268435456 "$WORK/checkout$EXE" > "$WORK/canary.log" 2>&1 & PIDS+=($!)
PORT=18082 VERSION=stable POD_NAME=checkout-stable-local PAYMENT_URL=http://127.0.0.1:18081 \
  "$WORK/checkout$EXE" > "$WORK/stable.log" 2>&1 & PIDS+=($!)

C="curl -s --max-time 5"
for _ in $(seq 50); do
  $C -o /dev/null http://127.0.0.1:18080/healthz && $C -o /dev/null http://127.0.0.1:18082/healthz && break
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
