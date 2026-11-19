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

.PHONY: help preflight cluster-up build deploy undeploy test load-steady load-spike run analyze

help:
	@echo "targets: preflight cluster-up build deploy undeploy test load-steady load-spike run analyze"

preflight:
	@bash scripts/preflight.sh

cluster-up:
	@bash scripts/cluster-up.sh

build:
	@echo "build: implemented in Phase 1-3 (minikube image build)"

deploy:
	@echo "deploy: implemented in Phase 3 (helm install)"

undeploy:
	@echo "undeploy: implemented in Phase 3"

test:
	@for d in $(GO_MODULES); do echo "== go vet/test $$d"; (cd $$d && go vet ./... && go test ./...); done
	@echo "== pytest"; $(PYTHON) -m pytest -q controller/tests experiments

load-steady:
	@echo "load-steady: implemented in Phase 4"

load-spike:
	@echo "load-spike: implemented in Phase 4"

run:
	@echo "run: implemented in Phase 8"

analyze:
	@echo "analyze: implemented in Phase 8"
