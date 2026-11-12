#!/usr/bin/env bash
# test-pve.sh – real-host tests of the Proxmox driver (make test-pve).
#
# The token stays where the installer put it (/etc/plumb/plumb.env in the
# Plumb LXC): this script builds the test binary, runs it INSIDE the LXC
# with that file and removes it again, also after Ctrl-C or a lost
# connection. It fails when the run left a guest or a disk of the Plumb
# VMID range: it compares the host before and after the run, so guests of
# a live controller in the same pool and range do not count.
#
#   PLUMB_TEST_ENV     file with PLUMB_TEST_PVE_SSH (default ~/.config/plump/testhost.env;
#                      never in the repo)
#   PLUMB_TEST_LXC_ID  the Plumb LXC (default 100)
#   PLUMB_PVE_IMAGE_URL, PLUMB_PVE_IMAGE_SHA256  test image (default: the pinned
#                      Debian 13 genericcloud image of the T-012 log)
#   PLUMB_PVE_RUN      -test.run pattern (default TestHostLifecycle$)
#   PLUMB_PVE_RECORD=1 record the answers into ./pve-record/ (check them for
#                      leaks before they go to testdata/pve)
# The VMID range and the disk storage come from plumb.env
# (PLUMB_PVE_VMID_RANGE, PLUMB_PVE_DISK). Without PLUMB_PVE_NAMESERVER and
# PLUMB_PVE_SEARCHDOMAIN there (installs before D-187), the test guests get
# 192.0.2.53 and plumb.test (TEST-NET: no working DNS in the guest).
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
ssh=(ssh -o BatchMode=yes "$PLUMB_TEST_PVE_SSH")

# Range and disk storage of the install (no secret in these two lines).
# shellcheck disable=SC2029 # lxc is checked above
setting() { "${ssh[@]}" "pct exec $lxc -- sed -n 's/^$1=//p' /etc/plumb/plumb.env"; }
range=$(setting PLUMB_PVE_VMID_RANGE)
disk=$(setting PLUMB_PVE_DISK)
[[ $disk =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || { echo "test-pve: cannot read PLUMB_PVE_DISK from plumb.env"; exit 1; }
# Last match of the test: BASH_REMATCH holds the range.
[[ $range =~ ^([0-9]+)-([0-9]+)$ ]] || { echo "test-pve: cannot read PLUMB_PVE_VMID_RANGE from plumb.env"; exit 1; }
lo=${BASH_REMATCH[1]} hi=${BASH_REMATCH[2]}

# snapshot prints the guests (VMs and containers) and the disks of the
# range, one per line.
snapshot() {
	# shellcheck disable=SC2029 # lo, hi and disk are checked above
	"${ssh[@]}" "{ qm list; pct list; } | awk '\$1 ~ /^[0-9]+\$/ && \$1 >= $lo && \$1 <= $hi {print \"guest \" \$1}';
		pvesm list $disk | awk 'NR > 1 {print \$1}' | sed -n 's/^\(.*:\(vm\|base\)-\([0-9]*\)-.*\)$/\3 \1/p' |
			awk '\$1 >= $lo && \$1 <= $hi {print \"disk \" \$2}'" | sort
}

bin=$(mktemp)
remote=""
before=""
status=0
cleanup() {
	local rc=$?
	trap - EXIT INT TERM
	rm -f "$bin"
	if [[ -n $remote ]]; then
		"${ssh[@]}" "rm -rf $remote" || true
	fi
	"${ssh[@]}" "pct exec $lxc -- rm -rf /root/pvetest /root/pve-record" || true
	if [[ -n $before ]]; then
		local after left
		after=$(snapshot) || after=""
		left=$(comm -13 <(echo "$before") <(echo "$after"))
		if [[ -n $left ]]; then
			echo "test-pve: the run left on the host:"
			while IFS= read -r l; do echo "  $l"; done <<<"$left"
			rc=1
		fi
	fi
	exit $((rc != 0 ? rc : status))
}
trap cleanup EXIT
trap 'exit 130' INT TERM

before=$(snapshot)
CGO_ENABLED=0 go test -c -tags pve -o "$bin" ./internal/driver/proxmox
remote=$("${ssh[@]}" 'mktemp -d /root/plumb-test-pve.XXXXXX')
scp -q -o BatchMode=yes "$bin" "$PLUMB_TEST_PVE_SSH:$remote/pvetest"
# shellcheck disable=SC2029 # the values are checked above and expand here on purpose
"${ssh[@]}" "set -e
pct push $lxc $remote/pvetest /root/pvetest --perms 0700
pct exec $lxc -- rm -rf /root/pve-record
pct exec $lxc -- sh -c 'set -a; . /etc/plumb/plumb.env; : \${PLUMB_PVE_NAMESERVER:=192.0.2.53}; : \${PLUMB_PVE_SEARCHDOMAIN:=plumb.test}; set +a; $record PLUMB_PVE_IMAGE_URL=$url PLUMB_PVE_IMAGE_SHA256=$sha /root/pvetest -test.v -test.run \"$run\" -test.timeout 45m'" || status=$?
if [[ -n $record ]]; then
	mkdir -p pve-record
	"${ssh[@]}" "pct exec $lxc -- tar -C /root -czf - pve-record" | tar -xzf - -C . && echo "test-pve: answers in ./pve-record (check them before testdata/pve)"
fi
