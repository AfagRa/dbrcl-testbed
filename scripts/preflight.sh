#!/usr/bin/env bash
# Preflight: report host and tool readiness for dbrcl-testbed.
# Prints PASS/WARN per item. Never exits non-zero.
set -u

PASS=0; WARN=0
pass() { printf '  [PASS] %-22s %s\n' "$1" "$2"; PASS=$((PASS+1)); }
warn() { printf '  [WARN] %-22s %s\n' "$1" "$2"; WARN=$((WARN+1)); }
have() { command -v "$1" >/dev/null 2>&1; }
first_line() { head -n1 | tr -d '\r'; }

want_cpus=${MINIKUBE_CPUS:-4}
want_mem=${MINIKUBE_MEMORY:-8192}
PROFILE=${MINIKUBE_PROFILE:-dbrcl}
mk() { minikube -p "$PROFILE" "$@"; }
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

echo "dbrcl-testbed preflight ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
echo
echo "Host"
os="$(uname -s 2>/dev/null) $(uname -r 2>/dev/null)"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) os="Windows ($(uname -s), $(cmd //c ver 2>/dev/null | tr -d '\r' | grep -i version))";;
esac
pass "OS" "$os"

cpus=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 0)
if [ "$cpus" -ge $((want_cpus + 2)) ]; then pass "CPU count" "$cpus"
else warn "CPU count" "$cpus (minikube wants $want_cpus plus headroom for k6 and host)"; fi

mem_mb=0
if [ -r /proc/meminfo ]; then mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
elif have sysctl; then mem_mb=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1048576 )); fi
if [ "$mem_mb" -ge $((want_mem + 4096)) ]; then pass "Total RAM" "${mem_mb} MB"
else warn "Total RAM" "${mem_mb} MB (minikube wants ${want_mem} MB plus headroom)"; fi

echo
echo "WSL2"
if in_wsl; then pass "environment" "inside WSL2 distro ${WSL_DISTRO_NAME:-?} (supported setup)"
else warn "environment" "not inside a WSL2 distro ($(uname -s)); scripts also run here, but the supported setup is WSL2 Ubuntu"; fi
wv=$(wsl_version)
if [ -n "$wv" ]; then pass "WSL version" "$wv"; else warn "WSL version" "wsl.exe not reachable"; fi
wmem=$(wsl_mem_mb); wcpu=$(wsl_cpus)
src="/proc"; in_wsl || src="Docker Desktop WSL2 VM"
if [ "${wmem:-0}" -ge "$WSL_SMALL_MB" ]; then pass "WSL memory" "${wmem} MB ($src)"
else warn "WSL memory" "${wmem} MB ($src) < 10 GB: use MINIKUBE_CPUS=$SMALL_CPUS MINIKUBE_MEMORY=$SMALL_MEMORY"; fi
if [ "${wcpu:-0}" -ge "$want_cpus" ]; then pass "WSL CPUs" "$wcpu ($src)"
else warn "WSL CPUs" "$wcpu ($src) < MINIKUBE_CPUS=$want_cpus"; fi

echo
echo "Tools"
check_tool() { # name, version command
  local name=$1; shift
  if have "$name"; then
    local v; v=$("$@" 2>&1 | first_line)
    pass "$name" "$v"
  else warn "$name" "not found on PATH"; fi
}
if have docker && docker info >/dev/null 2>&1; then
  dmem=$(( $(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0) / 1048576 ))
  pass "docker" "server $(docker version --format '{{.Server.Version}}' 2>/dev/null), engine RAM ${dmem} MB"
  [ "$dmem" -lt "$want_mem" ] && warn "docker engine RAM" "${dmem} MB < ${want_mem} MB requested for minikube"
elif have docker; then warn "docker" "client found but daemon not reachable (is Docker running?)"
else warn "docker" "not found on PATH"; fi
check_tool minikube minikube version --short
check_tool helm helm version --short
check_tool kubectl kubectl version --client
check_tool k6 k6 version
check_tool go go version
check_tool make make --version

PY=""
for c in ${PYTHON:-} python3 python; do
  if have "$c" && "$c" -c 'import sys; sys.exit(0 if sys.version_info >= (3,11) else 1)' 2>/dev/null; then PY=$c; break; fi
done
if [ -n "$PY" ]; then pass "python (>=3.11)" "$PY: $($PY --version 2>&1)"
else warn "python (>=3.11)" "no python>=3.11 found as python3/python"; fi

echo
echo "Cluster"
if have minikube; then
  cfg="${MINIKUBE_HOME:-$HOME/.minikube}/profiles/$PROFILE/config.json"
  driver=""
  [ -f "$cfg" ] && driver=$(tr -d ' \r\n' < "$cfg" | grep -o '"Driver":"[^"]*"' | head -n1 | cut -d'"' -f4)
  if [ -n "$driver" ]; then pass "minikube driver" "$driver (profile $PROFILE)"
  else warn "minikube driver" "profile $PROFILE not created yet (make cluster-up will use ${MINIKUBE_DRIVER:-docker})"; fi

  host_state=$(mk status --format '{{.Host}}' 2>/dev/null | first_line)
  if [ "$host_state" = "Running" ]; then
    pass "minikube status" "Running"
    cmin=$(kubectl version --client -o json 2>/dev/null | tr -d ' \r\n' | grep -o '"minor":"[0-9]*' | head -n1 | grep -o '[0-9]*$')
    smin=$(kubectl version -o json 2>/dev/null | tr -d ' \r\n' | grep -o '"serverVersion".*' | grep -o '"minor":"[0-9]*' | head -n1 | grep -o '[0-9]*$')
    if [ -n "$cmin" ] && [ -n "$smin" ]; then
      d=$((cmin - smin)); d=${d#-}
      if [ "$d" -le 1 ]; then pass "kubectl skew" "client 1.$cmin, server 1.$smin"
      else warn "kubectl skew" "client 1.$cmin vs server 1.$smin (>1 minor; use 'minikube -p $PROFILE kubectl --' or upgrade kubectl)"; fi
    fi
    kver=$(mk ssh -- uname -r 2>/dev/null | first_line)
    # minikube ssh does not propagate exit codes reliably on Windows: compare output.
    btf=$(mk ssh -- 'test -r /sys/kernel/btf/vmlinux && echo BTF_YES || echo BTF_NO' 2>/dev/null | first_line)
    if [ "$btf" = "BTF_YES" ]; then
      pass "BTF (Tetragon)" "/sys/kernel/btf/vmlinux present in the minikube node, kernel $kver -> Tetragon"
    else
      warn "BTF (Tetragon)" "/sys/kernel/btf/vmlinux missing in the minikube node, kernel $kver -> sink_fallback"
    fi
  else
    case "$host_state" in Stopped|Paused) ;; *) host_state="not running" ;; esac  # hide minikube's error text
    warn "minikube status" "$host_state (run make cluster-up, then re-run preflight for BTF)"
    warn "BTF (Tetragon)" "unknown until the cluster is running"
  fi
fi

echo
echo "Summary: $PASS PASS, $WARN WARN"
exit 0
