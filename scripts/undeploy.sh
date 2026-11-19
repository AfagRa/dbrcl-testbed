#!/usr/bin/env bash
# Remove the testbed chart and Prometheus. Namespaces are removed with them.
set -uo pipefail
helm uninstall dbrcl-testbed -n dbrcl --wait 2>/dev/null || echo "dbrcl-testbed not installed"
helm uninstall prometheus -n monitoring --wait 2>/dev/null || echo "prometheus not installed"
kubectl delete namespace dbrcl monitoring sink --ignore-not-found --wait=true
