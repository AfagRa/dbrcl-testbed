# PromQL used by the testbed

Prometheus runs in namespace `monitoring` (chart `prometheus-community/prometheus` 29.47.2, values in [deploy/prometheus/values.yaml](../deploy/prometheus/values.yaml)): scrape interval 5 s, retention 2 d. In-cluster URL: `http://prometheus-server.monitoring.svc.cluster.local`. From the host:

```
kubectl -n monitoring port-forward svc/prometheus-server 19090:80
python scripts/promq.py "<name>" '<query>'
```

`make cluster-check` runs every query below against live traffic and fails if any returns no data.

## Where the labels come from

- App pods are scraped by the chart's default `kubernetes-pods` job through `prometheus.io/scrape|port|path` annotations. That job has `honor_labels: true`, so the labels our services expose win: `version` (`stable`|`canary`) and `pod` (`POD_NAME` from the downward API, equal to the Kubernetes pod name).
- cAdvisor metrics (`container_*`) come from the kubelet (`kubernetes-nodes-cadvisor` job). They have no `version` label; it is derived from the pod name with `label_replace` (pods are named `checkout-stable-…` / `checkout-canary-…`).
- cAdvisor series carry per-cgroup labels (`id`, `image`, `name`, and `cpu` on CPU counters), so every ratio aggregates both sides to `(namespace, pod, container)` and joins `on` those labels. Spec metrics use `max by` so a stale series from a restarted container cannot double-count. Selecting `container="checkout"` excludes the pod-level cgroup (`container=""`).

## Traffic and SLIs (router view: what users saw)

| Signal | Query |
|---|---|
| Request rate (rps) by version | `sum by (version) (rate(router_requests_total[1m]))` |
| Error rate (5xx share) by version | `(sum by (version) (rate(router_requests_total{code=~"5.."}[1m])) or 0 * sum by (version) (rate(router_requests_total[1m]))) / sum by (version) (rate(router_requests_total[1m]))` |
| SLO-violation share by version | `sum by (version) (rate(router_slo_violations_total[1m])) / sum by (version) (rate(router_requests_total[1m]))` |
| Router P99 latency (s) by version | `histogram_quantile(0.99, sum by (le, version) (rate(router_request_duration_seconds_bucket[1m])))` |
| Current canary weight | `max(router_canary_weight)` |

The `or 0 * …` term makes the error rate 0 instead of "no data" while no 5xx series exists yet.

## Checkout service view

| Signal | Query |
|---|---|
| Checkout P99 latency (s) by version | `histogram_quantile(0.99, sum by (le, version) (rate(checkout_request_duration_seconds_bucket[1m])))` |
| Payment errors (per s) by version | `sum by (version) (rate(checkout_payment_errors_total[1m]))` |
| Active fault (ground-truth aid, not used by detectors) | `max by (version, fault) (checkout_fault_active) == 1` |

## Resources (cAdvisor)

| Signal | Query |
|---|---|
| Memory working set (bytes) | `label_replace(max by (namespace, pod, container) (container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| Memory fraction of limit | `label_replace(max by (namespace, pod, container) (container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}) / on (namespace, pod, container) max by (namespace, pod, container) (container_spec_memory_limit_bytes{namespace="dbrcl",container="checkout"}), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| Memory slope (bytes/s) | `label_replace(max by (namespace, pod, container) (deriv(container_memory_working_set_bytes{namespace="dbrcl",container="checkout"}[2m])), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| CPU usage (cores) | `label_replace(sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{namespace="dbrcl",container="checkout"}[1m])), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| CPU fraction of limit | `label_replace(sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{namespace="dbrcl",container="checkout"}[1m])) / on (namespace, pod, container) (max by (namespace, pod, container) (container_spec_cpu_quota{namespace="dbrcl",container="checkout"}) / max by (namespace, pod, container) (container_spec_cpu_period{namespace="dbrcl",container="checkout"})), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| CPU throttling share | `label_replace(sum by (namespace, pod, container) (rate(container_cpu_cfs_throttled_periods_total{namespace="dbrcl",container="checkout"}[1m])) / on (namespace, pod, container) sum by (namespace, pod, container) (rate(container_cpu_cfs_periods_total{namespace="dbrcl",container="checkout"}[1m])), "version", "$1", "pod", "checkout-(stable\|canary)-.*")` |
| Container restarts (OOM kills show here) | `kube_pod_container_status_restarts_total{namespace="dbrcl",container="checkout"}` |

(`\|` in the table is Markdown escaping; the query itself uses a plain `|`.)

## Security fallback (sink view)

| Signal | Query |
|---|---|
| Connections to the sink by source IP and port (window) | `sum by (source_ip, port) (increase(sink_connections_total[2m]))` |
| Canary pod IPs (to map `source_ip` → pod) | `kube_pod_info{namespace="dbrcl", pod=~"checkout-canary-.*"}` (label `pod_ip`) |

The sink records the TCP peer address it accepted. Pod-to-Service traffic is not SNATed on this cluster (Calico), so `source_ip` is the canary pod IP: `make cluster-check` asserts this.

## Notes

- `rate(...[1m])` with a 5 s scrape uses ~12 samples; the controller window (`controller.window_seconds`, default 60) matches it.
- `histogram_quantile` interpolates linearly inside a bucket; the buckets are in [services/checkout/server.go](../services/checkout/server.go) (5 ms to 5 s).
- Results for the paper are computed from raw logs (router, fault engine, controller), not from these queries; Prometheus is the controller's live decision input.
