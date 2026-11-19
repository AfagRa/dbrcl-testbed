#!/usr/bin/env bash
# Start a single-node minikube with Calico so NetworkPolicy is enforced.
set -euo pipefail
PROFILE=${MINIKUBE_PROFILE:-dbrcl}
CPUS=${MINIKUBE_CPUS:-4}
MEM=${MINIKUBE_MEMORY:-8192}
CNI=${MINIKUBE_CNI:-calico}
DRIVER=${MINIKUBE_DRIVER:-docker}
K8S=${MINIKUBE_K8S_VERSION:-v1.37.0}

# minikube silently keeps CPU/memory/CNI of an existing profile. Refuse a mismatch.
cfg="${MINIKUBE_HOME:-$HOME/.minikube}/profiles/$PROFILE/config.json"
if [ -f "$cfg" ]; then
  have_cpus=$(tr -d ' \r\n' < "$cfg" | grep -o '"CPUs":[0-9]*' | head -n1 | cut -d: -f2)
  have_mem=$(tr -d ' \r\n' < "$cfg" | grep -o '"Memory":[0-9]*' | head -n1 | cut -d: -f2)
  have_cni=$(tr -d ' \r\n' < "$cfg" | grep -o '"CNI":"[^"]*"' | head -n1 | cut -d'"' -f4)
  if [ "$have_cpus" != "$CPUS" ] || [ "$have_mem" != "$MEM" ] || [ "$have_cni" != "$CNI" ]; then
    echo "ERROR: existing minikube profile '$PROFILE' has cpus=$have_cpus memory=$have_mem cni=$have_cni," >&2
    echo "       wanted cpus=$CPUS memory=$MEM cni=$CNI. Run 'minikube delete -p $PROFILE' first." >&2
    exit 1
  fi
fi

echo "minikube start -p $PROFILE --driver=$DRIVER --cpus=$CPUS --memory=$MEM --cni=$CNI --kubernetes-version=$K8S"
minikube start -p "$PROFILE" --driver="$DRIVER" --cpus="$CPUS" --memory="$MEM" --cni="$CNI" --kubernetes-version="$K8S"
minikube -p "$PROFILE" addons enable metrics-server >/dev/null || true
echo "waiting for node Ready..."
kubectl wait --for=condition=Ready node --all --timeout=300s
echo "waiting for calico..."
kubectl -n kube-system rollout status ds/calico-node --timeout=300s
kubectl get nodes -o wide
