# dbrcl-testbed. Run from WSL2 Ubuntu (supported) or Git Bash on Windows.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

# The repo venv (.venv) if present, else python3 (Ubuntu has no "python"), else python.
PYTHON         ?= $(shell [ -x .venv/bin/python ] && echo .venv/bin/python || command -v python3 2>/dev/null || command -v python)
MINIKUBE_CPUS  ?= 4
MINIKUBE_MEMORY?= 8192
MINIKUBE_CNI   ?= calico
MINIKUBE_DRIVER?= docker
MINIKUBE_PROFILE?= dbrcl
export MINIKUBE_CPUS MINIKUBE_MEMORY MINIKUBE_CNI MINIKUBE_DRIVER MINIKUBE_PROFILE PYTHON

GO_MODULES := $(dir $(wildcard services/go.mod))
IMAGE_TAG  ?= $(shell bash scripts/image-tag.sh)
PROM_CHART_VERSION ?= 29.47.2
export IMAGE_TAG PROM_CHART_VERSION

.PHONY: help preflight cluster-up build deploy undeploy cluster-check test load-steady load-diurnal load-spike load-check calibrate run analyze

help:
	@echo "targets: preflight cluster-up build deploy undeploy cluster-check test load-steady load-diurnal load-spike load-check calibrate run analyze"

preflight:
	@bash scripts/preflight.sh

cluster-up:
	@bash scripts/cluster-up.sh

build:
	@bash scripts/build-images.sh

deploy:
	@bash scripts/deploy.sh

undeploy:
	@bash scripts/undeploy.sh

cluster-check:
	@bash scripts/cluster-check.sh

test:
	@for d in $(GO_MODULES); do echo "== go vet/test $$d"; (cd $$d && go vet ./... && go test ./...); done
	@echo "== pytest"; $(PYTHON) -m pytest -q controller/tests experiments

load-steady load-diurnal load-spike: load-%:
	@bash scripts/load.sh $*

load-check:
	@bash scripts/load-check.sh

calibrate:
	@echo "calibrate: implemented with the DBRCL engine and runner (Phases 6 and 8)"

run:
	@echo "run: implemented in Phase 8"

analyze:
	@echo "analyze: implemented in Phase 8"
