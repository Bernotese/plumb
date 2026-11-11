#!/usr/bin/env bash
# test-pve.sh – real-host tests of the Proxmox driver (make test-pve).
#
# The token stays where the installer put it (/etc/plumb/plumb.env in the
# Plumb LXC): this script builds the test binary, runs it INSIDE the LXC
# with that file, removes it again, and fails when a guest of the Plumb
# VMID range is left on the host.
#
#   PLUMB_TEST_ENV     file with PLUMB_TEST_PVE_SSH (default ~/.config/plump/testhost.env;
#                      never in the repo)
#   PLUMB_TEST_LXC_ID  the Plumb LXC (default 100)
#   PLUMB_PVE_IMAGE_URL, PLUMB_PVE_IMAGE_SHA256  test image (default: the pinned
#                      Debian 13 genericcloud image of the T-012 log)
#   PLUMB_PVE_RUN      -test.run pattern (default TestHostLifecycle$)
#   PLUMB_PVE_RECORD=1 record the answers; they are copied to ./pve-record/ for a
#                      leak check before they go to testdata/pve
set -euo pipefail
env_file=${PLUMB_TEST_ENV:-$HOME/.config/plump/testhost.env}
[[ -r $env_file ]] || { echo "test-pve: $env_file is missing (address of the test host)"; exit 1; }
# shellcheck disable=SC1090
source "$env_file"
: "${PLUMB_TEST_PVE_SSH:?PLUMB_TEST_PVE_SSH is not set in $env_file}"
lxc=${PLUMB_TEST_LXC_ID:-100}
url=${PLUMB_PVE_IMAGE_URL:-https://cloud.debian.org/images/cloud/trixie/20261103-2651/debian-13-genericcloud-amd64-20261103-2651.qcow2}
sha=${PLUMB_PVE_IMAGE_SHA256:-de9d63f5e5ec5927d7548bc05b6043b87457b8c1cb9bcb95365168e8d0293b4f}
run=${PLUMB_PVE_RUN:-TestHostLifecycle\$}
[[ $url =~ ^https://[A-Za-z0-9./_-]+$ && $sha =~ ^[0-9a-f]{64}$ && $run =~ ^[A-Za-z0-9_$|^]+$ && $lxc =~ ^[0-9]+$ ]] ||
	{ echo "test-pve: bad image URL, SHA-256, run pattern or LXC id"; exit 1; }
record=""
[[ ${PLUMB_PVE_RECORD:-0} == 1 ]] && record="PLUMB_PVE_RECORD=/root/pve-record"

bin=$(mktemp)
trap 'rm -f "$bin"' EXIT
CGO_ENABLED=0 go test -c -tags pve -o "$bin" ./internal/driver/proxmox
ssh=(ssh -o BatchMode=yes "$PLUMB_TEST_PVE_SSH")
remote=$("${ssh[@]}" 'mktemp -d /root/plumb-test-pve.XXXXXX')
scp -q -o BatchMode=yes "$bin" "$PLUMB_TEST_PVE_SSH:$remote/pvetest"

status=0
# shellcheck disable=SC2029 # the values are checked above and expand here on purpose
"${ssh[@]}" "set -e
pct push $lxc $remote/pvetest /root/pvetest --perms 0700
rm -rf $remote
pct exec $lxc -- rm -rf /root/pve-record
pct exec $lxc -- sh -c 'set -a; . /etc/plumb/plumb.env; set +a; $record PLUMB_PVE_IMAGE_URL=$url PLUMB_PVE_IMAGE_SHA256=$sha /root/pvetest -test.v -test.run \"$run\" -test.timeout 45m'" || status=$?
if [[ -n $record ]]; then
	mkdir -p pve-record
	"${ssh[@]}" "pct exec $lxc -- tar -C /root -czf - pve-record" | tar -xzf - -C . && echo "test-pve: answers in ./pve-record (check them before testdata/pve)"
fi
"${ssh[@]}" "pct exec $lxc -- rm -rf /root/pvetest /root/pve-record"

# No guest of the Plumb range may stay behind.
left=$("${ssh[@]}" "qm list | awk 'NR > 1 && \$1 >= 9000 && \$1 <= 9099 {print \$1}'")
if [[ -n $left ]]; then
	echo "test-pve: guests left on the host: $left"
	status=1
fi
exit "$status"
