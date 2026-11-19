# dbrcl-testbed. Recipes are POSIX shell; on Windows run make from Git Bash or
# with Git's sh.exe on PATH.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

PYTHON         ?= python
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

.PHONY: help preflight cluster-up build deploy undeploy cluster-check test load-steady load-spike calibrate run analyze

help:
	@echo "targets: preflight cluster-up build deploy undeploy cluster-check test load-steady load-spike calibrate run analyze"

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

load-steady:
	@echo "load-steady: implemented in Phase 4"

load-spike:
	@echo "load-spike: implemented in Phase 4"

calibrate:
	@echo "calibrate: implemented with the DBRCL engine and runner (Phases 6 and 8)"

run:
	@echo "run: implemented in Phase 8"

analyze:
	@echo "analyze: implemented in Phase 8"
