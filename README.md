# dbrcl-testbed

A small, reproducible Kubernetes testbed for the paper *"Minimizing the Blast Radius: Automated Staged Rollouts Driven by Real-Time Health Metrics and AI-Assisted Threat Mitigation for Safe Software Updates"*. It produces timestamped measurements that compare three rollout strategies (manual, static-rule, DBRCL) on MTTD, MTTR, blast radius exposure and false-positive rollback rate.

> Status: Phases 0-3 done (services, router, deployment). The sections below are filled in as phases land.

## Platform

Supported setup: Windows with **WSL2 Ubuntu**, Docker Desktop (WSL2 backend, WSL integration enabled for the Ubuntu distro) and `minikube --driver=docker`; run every `make` target from the Ubuntu shell. Setup steps: [docs/wsl-setup.md](docs/wsl-setup.md). The scripts also run from Git Bash on Windows, which is how Phases 0-3 were first verified; `make preflight` says which environment it detected.

## Architecture

_TODO (Phase 9)._

## Quick start

```
make preflight     # host and tool check, PASS/WARN per item
make cluster-up    # minikube, MINIKUBE_CPUS=4 / MINIKUBE_MEMORY=8192 / Calico; warns to use 2 / 4608 if WSL2 has < 10 GB
make build deploy  # images into minikube (tag = git commit), Prometheus + testbed chart
make cluster-check # checkout via router, every query in docs/promql.md returns data
make calibrate     # Phases 6/8: kappa from healthy runs -> config/calibrated.yaml
make run           # Phase 8
make analyze       # Phase 8
```

## Running experiments

_TODO (Phase 8)._

## Mapping to the paper's equations

_TODO (Phase 8/9)._

## Parameters

All tunables live in [config/experiment.yaml](config/experiment.yaml). Paper-level parameters below are **placeholders, to be justified**; the defaults were chosen for a time-compressed laptop testbed.

| Parameter | Config key | Default | Status |
|---|---|---|---|
| Stage weights w_k | `stages.weights` | 0.01, 0.05, 0.25, 1.0 | placeholder, to be justified |
| Dwell per stage | `stages.min_dwell_seconds` | 120 s each | placeholder, to be justified |
| Horizon T_H | `experiment.horizon_seconds` | 1200 s | placeholder, to be justified |
| T_max | `controller.max_rollout_seconds` | 480 s | placeholder, to be justified |
| Warmup | `experiment.warmup_seconds` | 90 s | placeholder, to be justified |
| kappa | `fusion.kappa` | 0.05 (starting value; `make calibrate` sets kappa = 1.2 · max_k(w_k · r_k) in `config/calibrated.yaml`) | placeholder until calibrated |
| theta_max | `fusion.theta_max` | 0.8 | placeholder, to be justified |
| epsilon | `fusion.epsilon` | 0.05 | placeholder, to be justified |
| Reviewer interval | `manual.review_interval_seconds` | 300 s | placeholder, to be justified |

Load: steady 50 rps, diurnal 25-75 rps (period 600 s), spike 4x of 50 rps for 180 s starting 300 s after load start. Trials: 3 per main-matrix cell (main: strategies × {mem_leak, latency_regression, error_burst, unexpected_egress, healthy_spike}; demo: cpu_saturation and dependency_failure once per strategy). Calibration seeds (9001-9003) are disjoint from evaluation seeds (42-44).

## Manual baseline: modeling assumptions

_TODO (Phase 5)._

## Scoped substitutions

_TODO (Phase 9)._ In short: a small Go router replaces Istio and Argo Rollouts for exact, nested hash cohorts; dwell times and horizon are time-compressed.

## Secrets

Nothing sensitive is committed. Copy `.env.example` to `.env` (gitignored) and fill in optional values.
