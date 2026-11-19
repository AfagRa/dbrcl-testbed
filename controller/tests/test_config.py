"""Sanity checks on config/experiment.yaml defaults."""
from pathlib import Path

import yaml

CONFIG = Path(__file__).resolve().parents[2] / "config" / "experiment.yaml"


def load():
    return yaml.safe_load(CONFIG.read_text(encoding="utf-8"))


def test_stages_increasing_and_dwell_per_stage():
    c = load()
    w = c["stages"]["weights"]
    assert w == sorted(w) and 0 < w[0] and w[-1] == 1.0
    assert len(c["stages"]["min_dwell_seconds"]) == len(w)


def test_required_defaults():
    c = load()
    assert c["experiment"]["horizon_seconds"] == 1800
    assert c["experiment"]["warmup_seconds"] == 120
    assert c["controller"]["step_seconds"] == 10
    assert c["controller"]["window_seconds"] == 60
    assert c["controller"]["post_check_seconds"] == 60
    assert c["slo"] == {"latency_ms": 300, "error_rate": 0.01}
    assert c["static"]["failure_limit"] == 3
    assert c["manual"]["review_interval_seconds"] == 300
    assert c["manual"]["action_delay_seconds"] == 180
    assert c["forecast"]["seasonal_period_seconds"] == 600
    assert c["exhaustion"]["tte_horizon_seconds"] == 600
    assert c["iforest"]["quantile"] == 0.995
    assert c["fusion"]["alpha"] == 0.3
    assert c["fusion"]["throttle_fraction"] == 0.7
    assert c["fusion"]["kappa"] == 0.02
    assert c["fusion"]["theta_max"] == 0.8
    assert c["experiment"]["trials"] == 5


def test_every_fault_has_params():
    c = load()
    assert set(c["experiment"]["faults"]) == set(c["faults"])
