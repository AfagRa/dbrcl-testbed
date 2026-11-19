# WSL2 Ubuntu setup

The supported environment is WSL2 Ubuntu on Windows, with Docker Desktop (WSL2 backend) and `minikube --driver=docker`. Versions are pinned so runs are reproducible.

## 1. Windows side (PowerShell, once)

```powershell
wsl --install -d Ubuntu-24.04
```

Create the Linux user when prompted. Then in Docker Desktop: *Settings → Resources → WSL integration →* enable **Ubuntu-24.04**, *Apply & restart*.

WSL2 memory and CPUs come from `%UserProfile%\.wslconfig` (Docker Desktop shares the same VM). For the default minikube profile (4 CPU / 8 GiB) the VM needs at least 10 GB, for example:

```
[wsl2]
memory=14GB
processors=8
```

Run `wsl --shutdown` after editing it. With less than 10 GB, `make cluster-up` prints a warning recommending `MINIKUBE_CPUS=2 MINIKUBE_MEMORY=4608`.

If a minikube profile named `dbrcl` was created from Windows earlier, delete it from Windows first (`minikube delete -p dbrcl`): the WSL-side minikube would otherwise collide with its Docker container of the same name.

## 2. Ubuntu side (once)

```bash
sudo apt-get update
sudo apt-get install -y make git curl ca-certificates python3 python3-venv python3-pip

cd /tmp
# Go 1.27.0
curl -fsSLO https://go.dev/dl/go1.27.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.27.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc

# kubectl v1.37.0 (matches the cluster's Kubernetes version)
curl -fsSLO https://dl.k8s.io/release/v1.37.0/bin/linux/amd64/kubectl
curl -fsSLO https://dl.k8s.io/release/v1.37.0/bin/linux/amd64/kubectl.sha256
echo "$(cat kubectl.sha256)  kubectl" | sha256sum --check
sudo install -m 0755 kubectl /usr/local/bin/kubectl

# Helm v4.3.0
curl -fsSLO https://get.helm.sh/helm-v4.3.0-linux-amd64.tar.gz
tar -xzf helm-v4.3.0-linux-amd64.tar.gz && sudo install -m 0755 linux-amd64/helm /usr/local/bin/helm

# minikube v1.39.0
curl -fsSLO https://github.com/kubernetes/minikube/releases/download/v1.39.0/minikube-linux-amd64
sudo install -m 0755 minikube-linux-amd64 /usr/local/bin/minikube

# k6 v2.2.0
curl -fsSLO https://github.com/grafana/k6/releases/download/v2.2.0/k6-v2.2.0-linux-amd64.tar.gz
tar -xzf k6-v2.2.0-linux-amd64.tar.gz && sudo install -m 0755 k6-v2.2.0-linux-amd64/k6 /usr/local/bin/k6

source ~/.bashrc
```

## 3. Repository (inside Ubuntu)

Clone into the Linux filesystem (not `/mnt/c` or `/mnt/d`: file I/O across the Windows boundary is slow and loses executable bits):

```bash
git clone https://github.com/AfagRa/dbrcl-testbed.git ~/dbrcl-testbed
cd ~/dbrcl-testbed
python3 -m venv .venv && . .venv/bin/activate && pip install -r controller/requirements.txt
make preflight
make cluster-up
```

`make preflight` should report `environment: inside WSL2 distro Ubuntu-24.04` and BTF availability inside the minikube node. If BTF is missing, Tetragon is skipped and the sink fallback is used automatically (`security.source: auto`).
