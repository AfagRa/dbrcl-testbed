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

Install the pinned toolchain (Go 1.27.0, kubectl v1.37.0, Helm v4.3.0, minikube v1.39.0, k6 v2.2.0, make, Python 3, venv). Every download is checked against its published SHA-256; the script is idempotent:

```bash
sudo bash scripts/wsl-install-tools.sh     # from a checkout, or straight from the Windows path:
# wsl -d Ubuntu-24.04 -u root -- bash /mnt/d/<path-to-repo>/scripts/wsl-install-tools.sh
```

Create your user once if the distro has none (PowerShell; asks for a password):

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -c "adduser NAME && usermod -aG sudo NAME && printf '[user]\ndefault=NAME\n' >> /etc/wsl.conf"
wsl --terminate Ubuntu-24.04
```

minikube's docker driver refuses to run as root, so run the testbed as that user.

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
