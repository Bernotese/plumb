#!/usr/bin/env bash
# T-116: the firewall guest test (TestGuestFirewall) in unprivileged LXC
# containers and in VMs of LXD: Debian 13 and Ubuntu 26.04. run.sh covers
# the privileged Docker containers. Needs a working `lxc` (LXD) with
# network access for the images and apt; VMs need KVM.
#
#   LXD_KINDS="container vm"  which instance types to test (default both)
#   GUEST_RUN_ID              suffix of the instance names (default $$)
set -euo pipefail
cd "$(dirname "$0")/../.."
run_id=${GUEST_RUN_ID:-$$}
if [[ ! "$run_id" =~ ^[0-9]+(-[0-9]+)?$ ]]; then
  echo "GUEST_RUN_ID must be numeric with an optional numeric attempt suffix" >&2
  exit 2
fi
kinds=${LXD_KINDS:-container vm}
tmp=$(mktemp -d)
instances=()
cleanup() {
  for name in "${instances[@]}"; do lxc delete -f "$name" >/dev/null 2>&1 || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

CGO_ENABLED=0 GOOS=linux go test -c -tags=guest -o "$tmp/agent.test" ./internal/agent
CGO_ENABLED=0 GOOS=linux go test -c -tags=guest -o "$tmp/netplan-probe.test" ./test/guest

# inst runs a command in the instance; the VM agent may need a moment.
inst() { lxc exec "$name" -- "$@"; }

n=0
for image in images:debian/13 ubuntu:26.04; do
  for kind in $kinds; do
    n=$((n + 1))
    name="plumb-fw-$n-$run_id"
    source="$image"
    if [[ "$kind" == vm && "$image" == images:debian/13 ]]; then source=images:debian/13/cloud; fi
    args=(launch "$source" "$name")
    case "$kind" in
      container) args+=(-c security.privileged=false -c security.nesting=false) ;;
      vm) args+=(--vm -c limits.cpu=2 -c limits.memory=2GiB) ;;
      *) echo "unknown kind $kind" >&2; exit 2 ;;
    esac
    instances+=("$name")
    echo "== $source $kind ($name)"
    lxc "${args[@]}"
    lxc config get "$name" volatile.base_image
    ready=false
    for ((attempt = 0; attempt < 180; attempt++)); do
      state=$(inst systemctl is-system-running 2>/dev/null || true)
      if [[ "$state" == running || "$state" == degraded ]]; then ready=true; break; fi
      sleep 2
    done
    if [[ "$ready" != true ]]; then
      echo "systemd did not boot in $name (state: ${state:-none})" >&2
      exit 1
    fi
    echo "$name systemd state: $state"
    if [[ "$kind" == container ]]; then
      # Unprivileged: root of the container is not root of the host.
      map=$(inst cat /proc/self/uid_map)
      if [[ $(awk 'NR == 1 {print $2}' <<<"$map") == 0 ]]; then
        echo "$name is privileged: uid_map $map" >&2
        exit 1
      fi
    fi
    # cloud-init and apt timers of the image hold the apt lock at first boot.
    inst sh -c 'command -v cloud-init >/dev/null && cloud-init status --wait >/dev/null || true'
    inst env DEBIAN_FRONTEND=noninteractive sh -c \
      'apt-get -o DPkg::Lock::Timeout=300 -o Acquire::Retries=5 update -q && apt-get -o DPkg::Lock::Timeout=300 -o Acquire::Retries=5 install -y -q --no-install-recommends nftables iproute2' >/dev/null
    lxc file push "$tmp/agent.test" "$name/root/agent.test"
    inst /root/agent.test -test.v -test.timeout=5m -test.run '^TestGuestFirewall$'
    if [[ "$kind" == vm ]]; then
      lxc file push "$tmp/netplan-probe.test" "$name/root/netplan-probe.test"
      PLUMB_NETPLAN_VM="$name" "$tmp/netplan-probe.test" -test.v -test.timeout=50s -test.run '^TestNetplanProbe$'
    fi
    lxc delete -f "$name"
  done
done
cleanup
trap - EXIT
if lxc list --format csv -c n | grep -q -- "-$run_id\$"; then
  echo "LXD instances remain after cleanup" >&2
  exit 1
fi
