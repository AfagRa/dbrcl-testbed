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
    assert c["stages"]["min_dwell_seconds"] == [120] * len(w)


def test_required_defaults():
    c = load()
    e = c["experiment"]
    assert (e["horizon_seconds"], e["warmup_seconds"], e["trials"]) == (1200, 90, 3)
    assert c["controller"]["max_rollout_seconds"] == 480
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
    f = c["fusion"]
    assert (f["alpha"], f["throttle_fraction"], f["kappa"], f["theta_max"], f["epsilon"]) == (0.3, 0.7, 0.05, 0.8, 0.05)


def test_load_defaults():
    ld = load()["load"]
    assert ld["steady"]["rps"] == 50
    assert (ld["diurnal"]["min_rps"], ld["diurnal"]["max_rps"]) == (25, 75)
    assert (ld["spike"]["base_rps"], ld["spike"]["factor"], ld["spike"]["duration_seconds"]) == (50, 4, 180)
    assert ld["duration_seconds"] == load()["experiment"]["horizon_seconds"]


def test_matrix_scenarios_are_defined():
    c = load()
    sc = c["scenarios"]
    assert c["matrix"]["main"]["scenarios"] == [
        "mem_leak", "latency_regression", "error_burst", "unexpected_egress", "healthy_spike"]
    assert c["matrix"]["demo"]["scenarios"] == ["cpu_saturation", "dependency_failure"]
    for m in c["matrix"].values():
        for name in m["scenarios"]:
            assert name in sc
    for name, s in sc.items():
        assert s["load"] in ("steady", "diurnal", "spike")
        assert s["fault"] == "none" or s["fault"] in c["faults"], name
        assert s["class"] in ("operational", "security", "none")


def test_calibration_seeds_disjoint_from_evaluation():
    c = load()
    eval_seeds = {c["experiment"]["base_seed"] + t for t in range(c["experiment"]["trials"])}
    assert len(c["calibration"]["seeds"]) == 3
    assert not eval_seeds & set(c["calibration"]["seeds"])
    for name in c["calibration"]["scenarios"]:
        assert c["scenarios"][name]["fault"] == "none"


def test_analysis_settings():
    a = load()["analysis"]
    assert a["figure_width_in"] == 3.5 and set(a["figure_formats"]) == {"pdf", "png"}
    assert a["stats"] == ["mean", "std", "min", "max"]
