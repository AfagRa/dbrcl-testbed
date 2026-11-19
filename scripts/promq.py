"""Run one instant PromQL query and print the series. Exit 1 if no data.

Usage: python scripts/promq.py <name> <query> [prometheus_url]
"""
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

SHOW = ("version", "pod", "source_ip", "port", "code")


def main() -> int:
    name, query = sys.argv[1], sys.argv[2]
    base = sys.argv[3] if len(sys.argv) > 3 else "http://127.0.0.1:19090"
    url = base + "/api/v1/query?" + urllib.parse.urlencode({"query": query})
    try:
        with urllib.request.urlopen(url, timeout=10) as r:
            body = json.load(r)
    except urllib.error.HTTPError as e:  # Prometheus returns JSON errors with 4xx
        body = json.load(e)
    res = body.get("data", {}).get("result", [])
    if body.get("status") != "success" or not res:
        print(f"  [FAIL] {name}: no data ({body.get('error', 'empty result')})")
        return 1
    print(f"  [OK]   {name}")
    for s in res:
        lbl = {k: v for k, v in s["metric"].items() if k in SHOW}
        print(f"           {lbl} = {float(s['value'][1]):.6g}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
