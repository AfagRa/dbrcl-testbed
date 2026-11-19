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

# Per-request router logs are raw experiment data. The kubelet default (10Mi
# per container log file) would rotate within one 30-minute run and drop lines
# from `kubectl logs`, so raise it well above one run's volume.
LOGMAX=${KUBELET_LOG_MAX_SIZE:-500Mi}
EXTRA=(--extra-config=kubelet.container-log-max-size="$LOGMAX" --extra-config=kubelet.container-log-max-files=2)

echo "minikube start -p $PROFILE --driver=$DRIVER --cpus=$CPUS --memory=$MEM --cni=$CNI --kubernetes-version=$K8S ${EXTRA[*]}"
minikube start -p "$PROFILE" --driver="$DRIVER" --cpus="$CPUS" --memory="$MEM" --cni="$CNI" --kubernetes-version="$K8S" "${EXTRA[@]}"
# On an existing profile minikube rewrites the kubelet unit but does not restart
# kubelet; restart it if the running process lacks the log-size flag.
if ! minikube -p "$PROFILE" ssh -- "ps -eo args | grep [k]ubelet/ " | grep -q "container-log-max-size=$LOGMAX"; then
  echo "restarting kubelet to apply container-log-max-size=$LOGMAX"
  minikube -p "$PROFILE" ssh -- "sudo systemctl daemon-reload && sudo systemctl restart kubelet"
  sleep 5
fi
minikube -p "$PROFILE" ssh -- "ps -eo args | grep [k]ubelet/ " | grep -o "container-log-max-size=[^ ]*"
minikube -p "$PROFILE" addons enable metrics-server >/dev/null || true
echo "waiting for node Ready..."
kubectl wait --for=condition=Ready node --all --timeout=300s
echo "waiting for calico..."
kubectl -n kube-system rollout status ds/calico-node --timeout=300s
kubectl get nodes -o wide
