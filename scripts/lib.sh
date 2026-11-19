# Shared helpers for preflight.sh and cluster-up.sh (source, do not execute).

# in_wsl: true when running inside a WSL2 Linux distro.
in_wsl() { [ -n "${WSL_DISTRO_NAME:-}" ] || grep -qi microsoft /proc/version 2>/dev/null; }

# wsl_version: "WSL x.y.z.w, kernel ..." from wsl.exe (UTF-16 output), or empty.
wsl_version() {
  local out
  out=$(wsl.exe --version 2>/dev/null | tr -d '\0\r') || return 0
  local v k
  v=$(printf '%s\n' "$out" | grep -m1 -E '^WSL' | grep -oE '[0-9]+(\.[0-9]+)+')
  k=$(printf '%s\n' "$out" | grep -m1 -iE '^Kernel' | grep -oE '[0-9]+(\.[0-9]+)+[^ ]*')
  [ -n "$v" ] && echo "WSL $v, kernel $k"
}

# wsl_mem_mb / wsl_cpus: resources of the WSL2 VM that runs Docker and minikube.
# Inside WSL this is /proc; from Git Bash on Windows it is what Docker Desktop
# (WSL2 backend, same VM) reports.
wsl_mem_mb() {
  if in_wsl; then awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo
  else echo $(( $(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0) / 1048576 )); fi
}
wsl_cpus() {
  if in_wsl; then nproc
  else docker info --format '{{.NCPU}}' 2>/dev/null || echo 0; fi
}

# Below this WSL memory, recommend the small minikube profile.
WSL_SMALL_MB=10240
SMALL_CPUS=2
SMALL_MEMORY=4608
