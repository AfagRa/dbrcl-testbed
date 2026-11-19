"""Check one load run against Prometheus: rate shape, error rate, capacity.

Usage: python scripts/load_shape.py <run_dir> [prometheus_url]
Reads <run_dir>/load_window.json and k6_summary.json. Exit 1 if the shape or
error rate is off. Capacity numbers are reported, not asserted.
"""
import json
import math
import sys
import urllib.parse
import urllib.request
from datetime import datetime, timedelta
from pathlib import Path

STEP = 5


def prom(base, path, **params):
    url = f"{base}/api/v1/{path}?" + urllib.parse.urlencode(params)
    with urllib.request.urlopen(url, timeout=30) as r:
        body = json.load(r)
    if body["status"] != "success":
        raise RuntimeError(body)
    return body["data"]["result"]


def series(base, q, start, end):
    res = prom(base, "query_range", query=q, start=start.timestamp(), end=end.timestamp(), step=STEP)
    return {json.dumps(s["metric"], sort_keys=True): [(float(t), float(v)) for t, v in s["values"]] for s in res}


def parse(ts):
    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


def expected(win, k6):
    """Expected request rate at offset t (s) from load start, from the k6 params."""
    p = k6["dbrcl"]["params"]
    sc = k6["dbrcl"]["scenario"]
    if sc == "steady":
        r = float(p["RPS"])
        return lambda t: r
    if sc == "diurnal":
        lo, hi, per = float(p["MIN_RPS"]), float(p["MAX_RPS"]), float(p["PERIOD_S"])
        return lambda t: (lo + hi) / 2 - (hi - lo) / 2 * math.cos(2 * math.pi * t / per)
    base, f = float(p["BASE_RPS"]), float(p["FACTOR"])
    off, dur = float(p["SPIKE_OFFSET_S"]), float(p["SPIKE_DURATION_S"])
    return lambda t: base * f if off <= t < off + dur else base


def main() -> int:
    run = Path(sys.argv[1])
    base = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:19090"
    win = json.loads((run / "load_window.json").read_text())
    k6 = json.loads((run / "k6_summary.json").read_text())
    # Load start as recorded by k6 setup() right before the scenarios begin; the
    # host-side window also includes container startup and teardown.
    t0 = parse(k6["dbrcl"]["start"])
    t1 = t0 + timedelta(seconds=float(k6["dbrcl"]["duration_s"]))
    exp = expected(win, k6)

    # rate over W s (>= 6 scrapes, robust to one late scrape), every 10 s, starting
    # one scrape after the first full window (rate() extrapolates at series start)
    W = 30
    rate = series(base, f"sum(rate(router_requests_total[{W}s]))", t0 + timedelta(seconds=W + 2 * STEP), t1)
    pts = next(iter(rate.values()), [])
    print(f"  request rate (rps), {W} s window, every {2 * STEP} s   [expected window average in brackets]")
    bad = judged = 0
    peak = max([v for _, v in pts] + [1])
    for t, v in pts[::2]:
        off = t - t0.timestamp()
        window = [exp(off - W + i + 0.5) for i in range(W)]  # expected rate, 1 s steps
        e = sum(window) / W
        # judge smooth windows (constant or sinusoid); skip windows across a step change
        smooth = max(abs(window[i + 1] - window[i]) for i in range(W - 1)) < 0.05 * max(e, 1)
        ok = (not smooth) or abs(v - e) <= max(0.10 * e, 3)
        judged += smooth
        bad += 0 if ok else 1
        tag = "  " if not smooth else ("ok" if ok else "!!")
        print(f"   t+{off:5.0f}s {v:6.1f} [{e:5.1f}] {tag} {'#' * int(40 * v / peak)}")
    print(f"  shape: {judged} points judged (within 10%), {bad} off; unmarked points straddle a step change")

    m = k6["metrics"]
    reqs, failed = m["http_reqs"]["values"]["count"], m["http_req_failed"]["values"]["rate"]
    dropped = m.get("dropped_iterations", {}).get("values", {}).get("count", 0)
    rng = f"{int((t1 - t0).total_seconds())}s"
    err = prom(base, "query", time=t1.timestamp(),
               query=f'(sum(increase(router_requests_total{{code=~"5.."}}[{rng}])) or vector(0)) / sum(increase(router_requests_total[{rng}]))')
    err_rate = float(err[0]["value"][1]) if err else float("nan")
    print(f"  k6: {reqs} requests, failed {100 * failed:.3f}%, dropped iterations {dropped}")
    print(f"  router 5xx share over the run: {100 * err_rate:.3f}%")

    sel = 'namespace="dbrcl",container="checkout"'
    by = "(namespace, pod, container)"
    cpu = series(base, f"max by (pod) (sum by {by} (rate(container_cpu_usage_seconds_total{{{sel}}}[30s])) / on {by} "
                       f"(max by {by} (container_spec_cpu_quota{{{sel}}}) / max by {by} (container_spec_cpu_period{{{sel}}})))", t0, t1)
    thr = series(base, f"max by (pod) (sum by {by} (rate(container_cpu_cfs_throttled_periods_total{{{sel}}}[30s])) / on {by} "
                       f"sum by {by} (rate(container_cpu_cfs_periods_total{{{sel}}}[30s])))", t0, t1)
    p99 = series(base, "histogram_quantile(0.99, sum by (le, version) (rate(router_request_duration_seconds_bucket[30s])))", t0, t1)
    print("  capacity (max over the run):")
    for k, v in sorted(cpu.items()):
        pod = json.loads(k)["pod"]
        tv = max((x for _, x in thr.get(k, [])), default=0)
        print(f"   {pod:38s} CPU {100 * max(x for _, x in v):5.1f}% of limit, throttled periods {100 * tv:5.1f}%")
    for k, v in sorted(p99.items()):
        vals = [x for _, x in v if not math.isnan(x)]
        if vals:
            print(f"   router P99 {json.loads(k).get('version', '?'):7s} max {1000 * max(vals):7.1f} ms")

    ok = bad == 0 and failed < 0.005 and (err_rate != err_rate or err_rate < 0.005) and dropped == 0
    print(f"  {'[OK]' if ok else '[FAIL]'} shape points off: {bad}, failed {100 * failed:.3f}%, dropped {dropped}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
