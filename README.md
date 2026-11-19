# dbrcl-testbed

A small, reproducible Kubernetes testbed for the paper *"Minimizing the Blast Radius: Automated Staged Rollouts Driven by Real-Time Health Metrics and AI-Assisted Threat Mitigation for Safe Software Updates"*. It produces timestamped measurements that compare three rollout strategies (manual, static-rule, DBRCL) on MTTD, MTTR, blast radius exposure and false-positive rollback rate.

> Status: Phase 0 (scaffold). The sections below are filled in as phases land.

## Architecture

_TODO (Phase 9)._

## Quick start

```
make preflight     # host and tool check, PASS/WARN per item
make cluster-up    # minikube, 4 CPU / 8 GiB / Calico (override with MINIKUBE_CPUS, MINIKUBE_MEMORY, MINIKUBE_CNI, MINIKUBE_DRIVER)
make build deploy  # images into minikube (tag = git commit), Prometheus + testbed chart
make cluster-check # checkout via router, every query in docs/promql.md returns data
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
| Dwell per stage | `stages.min_dwell_seconds` | 180 s each | placeholder, to be justified |
| Horizon T_H | `experiment.horizon_seconds` | 1800 s | placeholder, to be justified |
| T_max | `controller.max_rollout_seconds` | not set | placeholder, meaning to confirm |
| kappa | `fusion.kappa` | 0.02 | placeholder, to be justified |
| theta_max | `fusion.theta_max` | 0.8 | placeholder, to be justified |
| epsilon | `fusion.epsilon` | not set | placeholder, meaning to confirm |
| Reviewer interval | `manual.review_interval_seconds` | 300 s | placeholder, to be justified |

## Manual baseline: modeling assumptions

_TODO (Phase 5)._

## Scoped substitutions

_TODO (Phase 9)._ In short: a small Go router replaces Istio and Argo Rollouts for exact, nested hash cohorts; dwell times and horizon are time-compressed.

## Secrets

Nothing sensitive is committed. Copy `.env.example` to `.env` (gitignored) and fill in optional values.
