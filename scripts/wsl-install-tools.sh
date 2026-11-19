#!/usr/bin/env bash
# Install the pinned toolchain inside WSL2 Ubuntu. Run as root:
#   sudo bash scripts/wsl-install-tools.sh
# Idempotent: skips tools that are already at the pinned version.
set -euo pipefail
[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }

GO_VERSION=1.27.0
KUBECTL_VERSION=v1.37.0   # matches the cluster's Kubernetes version
HELM_VERSION=v4.3.0
MINIKUBE_VERSION=v1.39.0
K6_VERSION=v2.2.0

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq make git curl ca-certificates python3 python3-venv python3-pip conntrack >/dev/null

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT; cd "$TMP"
get() { curl -fsSL --retry 3 -o "$1" "$2"; }

if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go$GO_VERSION "; then
  get go.tgz "https://go.dev/dl/go$GO_VERSION.linux-amd64.tar.gz"
  get go.sha "https://go.dev/dl/?mode=json&include=all"
  want=$(python3 -c "import json,sys; d=json.load(open('go.sha')); print(next(f['sha256'] for r in d if r['version']=='go$GO_VERSION' for f in r['files'] if f['filename']=='go$GO_VERSION.linux-amd64.tar.gz'))")
  echo "$want  go.tgz" | sha256sum --check --quiet
  rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
fi

if ! kubectl version --client 2>/dev/null | grep -q "$KUBECTL_VERSION"; then
  get kubectl "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl"
  get kubectl.sha256 "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl.sha256"
  echo "$(cat kubectl.sha256)  kubectl" | sha256sum --check --quiet
  install -m 0755 kubectl /usr/local/bin/kubectl
fi

if ! helm version --short 2>/dev/null | grep -q "$HELM_VERSION"; then
  get helm.tgz "https://get.helm.sh/helm-$HELM_VERSION-linux-amd64.tar.gz"
  get helm.sha "https://get.helm.sh/helm-$HELM_VERSION-linux-amd64.tar.gz.sha256sum"
  echo "$(cut -d' ' -f1 helm.sha)  helm.tgz" | sha256sum --check --quiet
  tar -xzf helm.tgz && install -m 0755 linux-amd64/helm /usr/local/bin/helm
fi

if ! minikube version --short 2>/dev/null | grep -q "$MINIKUBE_VERSION"; then
  get minikube "https://github.com/kubernetes/minikube/releases/download/$MINIKUBE_VERSION/minikube-linux-amd64"
  get minikube.sha "https://github.com/kubernetes/minikube/releases/download/$MINIKUBE_VERSION/minikube-linux-amd64.sha256"
  echo "$(cut -d' ' -f1 minikube.sha)  minikube" | sha256sum --check --quiet
  install -m 0755 minikube /usr/local/bin/minikube
fi

if ! k6 version 2>/dev/null | grep -q "$K6_VERSION"; then
  get k6.tgz "https://github.com/grafana/k6/releases/download/$K6_VERSION/k6-$K6_VERSION-linux-amd64.tar.gz"
  get k6.sha "https://github.com/grafana/k6/releases/download/$K6_VERSION/k6-$K6_VERSION-checksums.txt"
  echo "$(grep "k6-$K6_VERSION-linux-amd64.tar.gz" k6.sha | cut -d' ' -f1)  k6.tgz" | sha256sum --check --quiet
  tar -xzf k6.tgz && install -m 0755 "k6-$K6_VERSION-linux-amd64/k6" /usr/local/bin/k6
fi

echo "installed:"
go version; kubectl version --client | head -1; helm version --short; minikube version --short; k6 version | head -1
python3 --version; make --version | head -1
