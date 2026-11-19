"""Print shell assignments for a k6 scenario from config/experiment.yaml.

Usage: python scripts/load_env.py <steady|diurnal|spike>
Values already set in the environment win, so any parameter can be overridden.
"""
import os
import shlex
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    scenario = sys.argv[1]
    cfg = yaml.safe_load((ROOT / "config" / "experiment.yaml").read_text(encoding="utf-8"))
    ld = cfg["load"]
    vals = {"SCENARIO": scenario, "DURATION_S": ld["duration_seconds"], "UID_POOL": ld["uid_pool"],
            "SEED": cfg["experiment"]["base_seed"]}
    if scenario == "steady":
        vals["RPS"] = ld["steady"]["rps"]
    elif scenario == "diurnal":
        d = ld["diurnal"]
        vals.update(MIN_RPS=d["min_rps"], MAX_RPS=d["max_rps"], PERIOD_S=d["period_seconds"])
    elif scenario == "spike":
        s = ld["spike"]
        vals.update(BASE_RPS=s["base_rps"], FACTOR=s["factor"], SPIKE_DURATION_S=s["duration_seconds"],
                    SPIKE_OFFSET_S=s["offset_seconds"])
    else:
        print(f"unknown scenario {scenario}", file=sys.stderr)
        return 2
    for k, v in vals.items():
        print(f"{k}={shlex.quote(str(os.environ.get(k, v)))}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
