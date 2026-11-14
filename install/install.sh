#!/usr/bin/env bash
# install.sh – prepare a Proxmox VE host for Plumb (T-013, D-007, D-081..D-084,
# T-052: key volume and plumb key init, D-177..D-179).
#
# Run as root on the Proxmox host, on a terminal (plumb key init shows the
# recovery key there once):
#   install.sh --plumb-binary ./plumb [options]        install or repair
#   install.sh --snippet-only --ca FILE --url URL      write only the vendor snippet
#   install.sh --remove                                remove what this script made
#   install.sh --remove --keep-guests --plumb-binary ./plumb [--backup-to DIR]
#                                                      replace the controller, keep the guests (D-290)
#   install.sh --plumb-binary ./plumb --restore FILE --lxc-ip CIDR --lxc-gw IP
#                                                      a new controller from a backup (D-290)
# Add --dry-run to print every pveum/pvesm/pveam/pct command instead of
# running it. The script downloads no third-party code: the controller
# binary comes from --plumb-binary, the LXC template from Proxmox's own
# template list (pveam).
#
# Upgrade: run the new install.sh with the new plumb binary (a repair run).
# It pushes the binary first, then changes the content of plumb-images to
# import,vztmpl, then restarts plumb.service (D-229): an older binary refuses
# that content at its start. When the new binary does not start, the run
# sets the content back to import. Before you put an older binary back by
# hand, run: pvesm set plumb-images --content import
#
# Test hooks (install/install_test.go; they only point the script at test
# files and change nothing for root on a real host):
#   PLUMB_INSTALL_ASSUME_ROOT=1   skip the root check (fake commands only)
#   PLUMB_INSTALL_PVE_CA=FILE     the host CA file instead of /etc/pve/pve-root-ca.pem
#   PLUMB_INSTALL_STATE=FILE      the state file instead of /etc/plumb/install.state
#   PLUMB_INSTALL_LXC_DIR=DIR     the lxc run directory instead of /var/lib/lxc
#   PLUMB_INSTALL_RUNNING_TRIES=N, PLUMB_INSTALL_RUNNING_WAIT=S
#                                 checks of the running binary after the restart (default 30 × 2 s)
#   PLUMB_INSTALL_STAY_WAIT=S     wait before the second check of the service (default 3 s)
#   PLUMB_INSTALL_RESOLV=FILE     the resolver file instead of /etc/resolv.conf
#   PLUMB_INSTALL_ASSUME_TTY=1    skip the terminal check before plumb key init
#                                 (plumb key init itself still refuses a non-terminal)
#   PLUMB_INSTALL_SBIN=DIR, PLUMB_INSTALL_UNIT_DIR=DIR, PLUMB_INSTALL_COPY_CONF=FILE,
#   PLUMB_INSTALL_PVE_MNT=DIR     where the backup copy timer goes (default /usr/local/sbin,
#                                 /etc/systemd/system, /etc/plumb/backup-copy.conf, /mnt/pve)
set -euo pipefail
umask 077

readonly VERSION_MIN_MAJOR=9 VERSION_MIN_MINOR=1
# Before these versions cicustom does not check the storage of a snippet,
# so no ACL keeps the token to the static snippet of D-025 (D-081).
readonly MIN_QEMU_SERVER=9.2.11 MIN_STORAGE_PERL=9.1.13

# --- settings (flags override them) ---
POOL=plumb
PVE_USER=plumb@pve
TOKEN_NAME=plumb
SNIPPET_STORAGE=plumb-snippets
SNIPPET_DIR=/var/lib/plumb-snippets
SNIPPET=plumb-vendor.yaml
DISK_STORAGE=local-lvm
IMAGE_STORAGE=plumb-images
IMAGE_DIR=/var/lib/plumb-images
TEMPLATE_STORAGE=local
BRIDGE=vmbr0
LXC_ID=""
LXC_HOSTNAME=plumb
LXC_IP=dhcp
LXC_GW=""
VMID_MIN=9000
VMID_MAX=9099
PLUMB_BINARY=""
CA_FILE=""
AGENT_URL=""
MODE=install
# Backups (D-288, D-289, D-302 … D-304). BACKUP_STORAGE "" keeps the
# storage of the state file; "none" removes the copy timer.
BACKUP_STORAGE=""
BACKUP_TIME=03:00
BACKUP_KEEP=7
BACKUP_COPY_TIME=""
BACKUP_COPY_KEEP=14
BACKUP_MAX_SIZE=1G
BACKUP_MAX_BYTES=""
# Slice 7 (D-401, D-405, D-406). A repair run sets the rights from these
# flags alone: a line that a run no longer names is removed.
EXTRA_BRIDGE_ARGS=()
EXTRA_VLAN_ARGS=()
EXTRA_NETS=() # "bridge" (untagged) or "bridge/tag": one PlumbBridge line each
GUEST_BACKUP_STORAGE=""
GUEST_BACKUP_CLEAR=0
BACKUP_STORAGE_SET=""
KEEP_GUESTS=0
BACKUP_TO=""
RESTORE_FILE=""
DRY_RUN=0
DRY_GONE_LXC="" # the half-made LXC that a dry run has destroyed (T-132)
YES=0
SNIPPET_DIR_SET=0
PVE_CA=${PLUMB_INSTALL_PVE_CA:-/etc/pve/pve-root-ca.pem}
RESOLV=${PLUMB_INSTALL_RESOLV:-/etc/resolv.conf}
NAMESERVER=""   # --nameserver, else from RESOLV (D-187)
SEARCHDOMAIN="" # --searchdomain, else from RESOLV or the host name
STATE_FILE=${PLUMB_INSTALL_STATE:-/etc/plumb/install.state}
LXC_DIR=${PLUMB_INSTALL_LXC_DIR:-/var/lib/lxc}

# The key volume (D-086, D-102, D-177): its own volume with backup=0, so the
# master key is never in a backup of the LXC. plumb key init never creates
# the directory; it must be the mount point.
readonly KEY_DIR=/var/lib/plumb/key KEY_FILE=/var/lib/plumb/key/master.key KEY_VOLUME_GB=1

usage() {
	cat <<'EOF'
Usage:
  install.sh --plumb-binary FILE [options]     install Plumb, or repair an install
  install.sh --snippet-only --ca FILE --url URL
  install.sh --remove [--yes]
  install.sh --remove --keep-guests --plumb-binary FILE [--backup-to DIR]
  install.sh --plumb-binary FILE --restore BACKUP --lxc-ip CIDR --lxc-gw IP [options]

Options:
  --plumb-binary FILE    the plumb binary (make build → bin/plumb)
  --disk-storage ID      storage for guest disks and the LXC (default local-lvm)
  --image-storage ID     storage for downloaded images; default: the new dir storage plumb-images.
                         Another storage must have exactly the content "import" or "import,vztmpl"
                         (D-081, D-229); install.sh does not change it
  --template-storage ID  storage for the LXC template (default local)
  --bridge NAME          bridge for guests and the LXC (default vmbr0)
  --extra-bridge NAME    one more bridge for untagged guest NICs (repeat it for more); one SDN.Use
                         line on /sdn/zones/localnetwork/NAME (D-401)
  --extra-vlan NAME:TAG[,TAG…]
                         VLAN tags on a VLAN-aware bridge for guest NICs (repeat it for more); one
                         SDN.Use line per tag on /sdn/zones/localnetwork/NAME/TAG, never the whole bridge
  --guest-backup-storage ID
                         storage for guest backups (vzdump, content "backup"): VM.Backup on the pool and
                         Datastore.AllocateSpace + Audit on ID (D-405). With these rights the token itself can
                         back up, restore over and delete the backups of every pool guest at the Proxmox API
  --guest-backup-clear   allow guest backups on a storage without encryption (not a PBS with a client key):
                         a backup holds the whole guest disk with its secrets, in clear (D-406)
  --lxc-id N             VMID of the Plumb LXC (default: next free id)
  --lxc-ip CIDR|dhcp     address of the LXC (default dhcp; a static address is recommended:
                         the vendor snippet holds it, D-179)
  --lxc-gw IP            gateway for a static LXC address
  --nameserver IP[,IP…]  DNS servers of new VMs, 1 to 3 (default: nameserver lines of /etc/resolv.conf, D-187)
  --searchdomain NAME    DNS search domain of new VMs (default: search of /etc/resolv.conf, else the host domain)
  --vmid-range MIN-MAX   VMIDs for Plumb guests (default 9000-9099)
  --pool NAME            pool of Plumb guests (default plumb)
  --snippet-dir DIR      directory of the plumb-snippets storage (default /var/lib/plumb-snippets)
  --image-dir DIR        directory of the plumb-images storage (default /var/lib/plumb-images)
  --backup-time HH:MM    daily backup of serve, UTC (default 03:00)
  --backup-keep N        backups that serve keeps, 1 … 100 (default 7)
  --backup-storage ID|none
                         copy the newest encrypted backup daily to this storage (dir, nfs, cifs,
                         cephfs); "none" removes the copy timer, the copies stay (D-289). The path of a
                         dir storage is read at install time: after a change of it, run a repair run
  --backup-copy-time HH:MM   time of the copy, UTC (default: backup time + 30 min)
  --backup-copy-keep N   copies kept on the storage, 1 … 999 (default 14)
  --backup-max-size SIZE the largest backup that is copied, e.g. 512M or 2G (default 1G)
  --keep-guests          with --remove: take a fresh backup, copy it off, check it with the
                         recovery key, then remove only the controller; the pool, its guests
                         and the Proxmox objects stay for --restore (D-290)
  --backup-to DIR        with --keep-guests: copy the backup to DIR on THIS host (not off-host)
  --restore FILE         install a new controller from a backup file into the kept pool
  --dry-run              print the commands that change the host; change nothing
  --yes                  do not ask before changes
  -h, --help             this text
EOF
}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

say() { printf '%s\n' "$*"; }

# TMP_FILES are removed on every exit, also on Ctrl-C (review T-013 #7).
TMP_FILES=()
cleanup_tmp() { [[ ${#TMP_FILES[@]} -eq 0 ]] || rm -f -- "${TMP_FILES[@]}"; }
# on_exit keeps the exit status of the run: bash exits with the status
# from before the trap.
on_exit() {
	local rc=$?
	if [[ $rc != 0 && ${CONTENT_ROLLBACK:-0} == 1 ]]; then
		rollback_image_content
	fi
	cleanup_tmp
}
trap on_exit EXIT
# Each signal exits with 128+n, so on_exit sees a failure. Without its own
# trap, SIGHUP (an SSH drop, the usual way a host run stops) runs the exit
# trap with $? = 0 and skips the rollback (review M1 of T-077).
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
# new_tmp NAME makes a temp file and sets the variable NAME to its path.
# It runs in the main shell, so the exit trap knows the file.
new_tmp() {
	local f
	f=$(mktemp)
	TMP_FILES+=("$f")
	printf -v "$1" '%s' "$f"
}

# run changes the host. In --dry-run it prints the command instead.
run() {
	if [[ $DRY_RUN == 1 ]]; then
		printf '+'
		printf ' %q' "$@"
		printf '\n'
		return 0
	fi
	if [[ $1 == pct ]]; then
		# A failed pct step names itself; set -e then stops the run. Only
		# the subcommand is printed: arguments can be long or private.
		local rc=0
		"$@" || rc=$?
		[[ $rc == 0 ]] || echo "install.sh: \"pct $2\" failed (exit $rc)" >&2
		return $rc
	fi
	"$@"
}

# query reads only. It runs in --dry-run too.
query() { "$@"; }

# pct runs the Proxmox pct; "pct exec" runs with no terminal on stdin,
# stdout or stderr (T-132, T-122 F7). With a terminal on any of them,
# lxc-attach gives the command a pty in the container, and a run on an SSH
# terminal saw a finished step end with SIGHUP (exit 129) right after the
# first start of the LXC. The output still reaches the terminal: stdout
# through a pipe, stderr after the command. The key prompts use lxc-attach
# with the real terminal (D-180), never this.
pct() {
	if [[ ${1:-} != exec ]]; then
		command pct "$@"
		return
	fi
	local rc=0 err
	err=$(mktemp) || return 1
	command pct "$@" </dev/null 2>"$err" | cat || rc=$?
	cat -- "$err" >&2 || true
	rm -f -- "$err"
	return "$rc"
}

# confirm asks before a change. --yes answers it.
confirm() {
	local prompt=$1 want=${2:-y} answer
	if [[ $YES == 1 ]]; then
		return 0
	fi
	if [[ $DRY_RUN == 1 ]]; then
		say "(dry run: would ask: $prompt)"
		return 0
	fi
	[[ -t 0 ]] || die "no terminal to confirm \"$prompt\"; use --yes"
	read -r -p "$prompt [type $want] " answer
	[[ $answer == "$want" ]]
}

# --- arguments ---

parse_args() {
	while [[ $# -gt 0 ]]; do
		case $1 in
		--plumb-binary) PLUMB_BINARY=${2:?}; shift ;;
		--disk-storage) DISK_STORAGE=${2:?}; shift ;;
		--image-storage) IMAGE_STORAGE=${2:?}; shift ;;
		--template-storage) TEMPLATE_STORAGE=${2:?}; shift ;;
		--bridge) BRIDGE=${2:?}; shift ;;
		--extra-bridge) EXTRA_BRIDGE_ARGS+=("${2:?}"); shift ;;
		--extra-vlan) EXTRA_VLAN_ARGS+=("${2:?}"); shift ;;
		--guest-backup-storage) GUEST_BACKUP_STORAGE=${2:?}; shift ;;
		--guest-backup-clear) GUEST_BACKUP_CLEAR=1 ;;
		--lxc-id) LXC_ID=${2:?}; shift ;;
		--lxc-ip) LXC_IP=${2:?}; shift ;;
		--lxc-gw) LXC_GW=${2:?}; shift ;;
		--nameserver) NAMESERVER=${2:?}; shift ;;
		--searchdomain) SEARCHDOMAIN=${2:?}; shift ;;
		--vmid-range)
			VMID_MIN=${2%-*}
			VMID_MAX=${2#*-}
			shift
			;;
		--pool) POOL=${2:?}; shift ;;
		--snippet-dir)
			SNIPPET_DIR=${2:?}
			SNIPPET_DIR_SET=1
			shift
			;;
		--image-dir) IMAGE_DIR=${2:?}; shift ;;
		--snippet-only) MODE=snippet ;;
		--ca) CA_FILE=${2:?}; shift ;;
		--url) AGENT_URL=${2:?}; shift ;;
		--remove) MODE=remove ;;
		--keep-guests) KEEP_GUESTS=1 ;;
		--backup-to) BACKUP_TO=${2:?}; shift ;;
		--restore) RESTORE_FILE=${2:?}; shift ;;
		--backup-storage) BACKUP_STORAGE=${2:?}; shift ;;
		--backup-time) BACKUP_TIME=${2:?}; shift ;;
		--backup-keep) BACKUP_KEEP=${2:?}; shift ;;
		--backup-copy-time) BACKUP_COPY_TIME=${2:?}; shift ;;
		--backup-copy-keep) BACKUP_COPY_KEEP=${2:?}; shift ;;
		--backup-max-size) BACKUP_MAX_SIZE=${2:?}; shift ;;
		--dry-run) DRY_RUN=1 ;;
		--yes) YES=1 ;;
		-h | --help)
			usage
			exit 0
			;;
		*) die "unknown option $1 (see --help)" ;;
		esac
		shift
	done
	# Names go into commands and files: allow only plain characters.
	local name
	for name in "$POOL" "$DISK_STORAGE" "$IMAGE_STORAGE" "$TEMPLATE_STORAGE" "$SNIPPET_STORAGE" "$LXC_HOSTNAME"; do
		[[ $name =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "bad name: $name"
	done
	[[ $BRIDGE =~ ^[A-Za-z][A-Za-z0-9_.-]{0,14}$ ]] || die "bad bridge name: $BRIDGE"
	check_extra_args
	[[ $VMID_MIN =~ ^[0-9]+$ && $VMID_MAX =~ ^[0-9]+$ && $VMID_MIN -ge 100 && $VMID_MAX -ge $VMID_MIN ]] ||
		die "bad --vmid-range"
	[[ -z $LXC_ID || $LXC_ID =~ ^[1-9][0-9]{2,8}$ ]] || die "bad --lxc-id"
	[[ $LXC_IP == dhcp || $LXC_IP =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] || die "bad --lxc-ip (dhcp or CIDR)"
	[[ -z $LXC_GW || $LXC_GW =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || die "bad --lxc-gw"
	[[ $SNIPPET_DIR == /* && $SNIPPET_DIR != *..* ]] || die "bad --snippet-dir"
	[[ $IMAGE_DIR == /* && $IMAGE_DIR != *..* ]] || die "bad --image-dir"
	local t
	for t in "$BACKUP_TIME" "${BACKUP_COPY_TIME:-00:00}"; do
		[[ $t =~ ^([01][0-9]|2[0-3]):[0-5][0-9]$ ]] || die "bad time $t (HH:MM, UTC)"
	done
	if [[ -z $BACKUP_COPY_TIME ]]; then # backup time + 30 min
		local m=$((10#${BACKUP_TIME%:*} * 60 + 10#${BACKUP_TIME#*:} + 30))
		printf -v BACKUP_COPY_TIME '%02d:%02d' $((m / 60 % 24)) $((m % 60))
	fi
	# --backup-keep has the range of the controller (backup.MaxKeep, D-361):
	# a larger value would stop plumb serve at its start (T-198).
	if ! [[ $BACKUP_KEEP =~ ^[1-9][0-9]{0,2}$ ]] || ((BACKUP_KEEP > 100)); then
		die "bad --backup-keep $BACKUP_KEEP (1 … 100). Nothing was changed."
	fi
	# --backup-copy-keep is only read by the copy script on this host.
	[[ $BACKUP_COPY_KEEP =~ ^[1-9][0-9]{0,2}$ ]] || die "bad --backup-copy-keep $BACKUP_COPY_KEEP (1 … 999). Nothing was changed."
	[[ $BACKUP_MAX_SIZE =~ ^([1-9][0-9]{0,5})([MG])$ ]] || die "bad --backup-max-size (for example 512M or 2G)"
	BACKUP_MAX_BYTES=$((BASH_REMATCH[1] << 20))
	[[ ${BASH_REMATCH[2]} == G ]] && BACKUP_MAX_BYTES=$((BACKUP_MAX_BYTES << 10))
	[[ $KEEP_GUESTS == 0 || $MODE == remove ]] || die "--keep-guests works only with --remove"
	[[ -z $BACKUP_TO || $KEEP_GUESTS == 1 ]] || die "--backup-to works only with --remove --keep-guests"
	[[ -z $RESTORE_FILE || $MODE == install ]] || die "--restore is an install; use it without --remove or --snippet-only"
	[[ -z $RESTORE_FILE || $RESTORE_FILE != -* ]] || die "bad --restore"
}

# check_extra_args checks the flags of slice 7 and fills EXTRA_NETS
# (D-401, D-405, D-406). Names and tags go into pveum commands and into
# plumb.env, so only plain characters pass.
check_extra_args() {
	local b v tag tags
	if [[ $MODE != install ]] && { ((${#EXTRA_BRIDGE_ARGS[@]} + ${#EXTRA_VLAN_ARGS[@]} > 0)) ||
		[[ -n $GUEST_BACKUP_STORAGE || $GUEST_BACKUP_CLEAR == 1 ]]; }; then
		die "--extra-bridge, --extra-vlan and --guest-backup-* work only with an install or a repair run"
	fi
	for b in "${EXTRA_BRIDGE_ARGS[@]}"; do
		[[ $b =~ ^[A-Za-z][A-Za-z0-9_.-]{0,14}$ ]] || die "bad --extra-bridge $b (a bridge name like vmbr1)"
		[[ $b != "$BRIDGE" ]] || die "--extra-bridge $b is the bridge of the install (--bridge); it has its line already"
		add_extra_net "$b"
	done
	for v in "${EXTRA_VLAN_ARGS[@]}"; do
		[[ $v =~ ^([A-Za-z][A-Za-z0-9_.-]{0,14}):([0-9]+(,[0-9]+)*)$ ]] ||
			die "bad --extra-vlan $v (BRIDGE:TAG[,TAG…], for example vmbr1:20,30)"
		b=${BASH_REMATCH[1]}
		IFS=, read -ra tags <<<"${BASH_REMATCH[2]}"
		for tag in "${tags[@]}"; do
			if ! [[ $tag =~ ^[1-9][0-9]{0,3}$ ]] || ((tag > 4094)); then
				die "bad VLAN tag $tag in --extra-vlan $v (1 … 4094)"
			fi
			add_extra_net "$b/$tag"
		done
	done
	[[ -z $GUEST_BACKUP_STORAGE || $GUEST_BACKUP_STORAGE =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "bad --guest-backup-storage"
	[[ $GUEST_BACKUP_CLEAR == 0 || -n $GUEST_BACKUP_STORAGE ]] || die "--guest-backup-clear works only with --guest-backup-storage ID"
	return 0
}

# add_extra_net NET adds NET to EXTRA_NETS once.
add_extra_net() {
	local n
	for n in "${EXTRA_NETS[@]}"; do
		[[ $n != "$1" ]] || return 0
	done
	EXTRA_NETS+=("$1")
}

# --- checks ---

# check_extra_bridges checks that each bridge of --extra-bridge and
# --extra-vlan is a bridge of this node, and VLAN-aware for a tag (review L2
# of #330; T-214: PVE fails a tagged NIC on a bridge without VLAN filtering).
check_extra_bridges() {
	((${#EXTRA_NETS[@]} > 0)) || return 0
	local net rows bridges
	rows=$(query pvesh get "/nodes/$NODE/network" --output-format json 2>/dev/null) ||
		die "cannot read the network of node $NODE for --extra-bridge/--extra-vlan. Nothing was changed."
	# "iface type vlan_aware" per bridge; an OVS bridge carries tags.
	# shellcheck disable=SC2016 # perl code
	bridges=$(json_rows 'for my $e (@$d) { my $t = $e->{type} // ""; next unless $t eq "bridge" || $t eq "OVSBridge";
		print "$e->{iface} ", ($t eq "OVSBridge" || ($e->{bridge_vlan_aware} // 0) ? 1 : 0), "\n" }' <<<"$rows") ||
		die "cannot read the network of node $NODE for --extra-bridge/--extra-vlan. Nothing was changed."
	for net in "${EXTRA_NETS[@]}"; do
		# Exact names: a "." in a bridge name is no pattern (review n1 of #330).
		awk -v b="${net%/*}" '$1 == b { f = 1 } END { exit !f }' <<<"$bridges" ||
			die "bridge ${net%/*} (--extra-bridge/--extra-vlan) is not a bridge of node $NODE. Nothing was changed."
		if [[ $net == */* ]] && ! awk -v b="${net%/*}" '$1 == b && $2 == 1 { f = 1 } END { exit !f }' <<<"$bridges"; then
			die "bridge ${net%/*} is not VLAN-aware, so a NIC with tag ${net#*/} fails (--extra-vlan). Set \"VLAN aware\" on the bridge, or use --extra-bridge. Nothing was changed."
		fi
	done
}

check_host() {
	if [[ $DRY_RUN != 1 && ${PLUMB_INSTALL_ASSUME_ROOT:-0} != 1 && $EUID -ne 0 ]]; then
		die "run as root on the Proxmox host (or use --dry-run)"
	fi
	local tool
	for tool in pveversion pveum pvesm pvesh; do
		command -v "$tool" >/dev/null || die "$tool not found: this is not a Proxmox VE host"
	done
	local v major minor
	v=$(query pveversion)
	# pveversion: "pve-manager/9.1.2/abcdef (running kernel: …)"
	[[ $v =~ pve-manager/([0-9]+)\.([0-9]+) ]] || die "cannot read the Proxmox VE version from: $v"
	major=${BASH_REMATCH[1]}
	minor=${BASH_REMATCH[2]}
	if ((major < VERSION_MIN_MAJOR || (major == VERSION_MIN_MAJOR && minor < VERSION_MIN_MINOR))); then
		die "Plumb needs Proxmox VE ${VERSION_MIN_MAJOR}.${VERSION_MIN_MINOR} or newer; this host has ${major}.${minor} (D-032). Nothing was changed."
	fi
	local pkg min have
	for pkg in qemu-server:$MIN_QEMU_SERVER libpve-storage-perl:$MIN_STORAGE_PERL; do
		min=${pkg#*:}
		pkg=${pkg%%:*}
		# shellcheck disable=SC2016 # ${Version} is a dpkg-query field, not a shell variable
		have=$(query dpkg-query -W -f '${Version}' "$pkg" 2>/dev/null) || die "$pkg is not installed. Nothing was changed."
		query dpkg --compare-versions "$have" ge "$min" ||
			die "Plumb needs $pkg $min or newer (cicustom must check the snippet storage, D-081); this host has $have. Update Proxmox VE. Nothing was changed."
	done
}

# json_has FIELD VALUE: stdin is the JSON of a pveum/pvesh list.
json_has() { grep -Eq "\"$1\" *: *\"$2\""; }

# --- roles and rights (ARCHITECTURE §7, D-032, D-081, D-240) ---

# One role per ACL path, so no path gets a right it does not need.
# PlumbAudit: GET /nodes/<node>/disks/lvmthin (thin-pool metadata use for
# the disk gate) checks Sys.Audit on the fixed path / (PVE/API2/Disks/
# LVMThin.pm), so no narrower path works. Propagate 0: it does not reach
# /nodes, /vms or /storage (D-240).
# PlumbBridge has one more line per --extra-bridge and --extra-vlan tag
# (D-401); PlumbGuestBackupVM and PlumbGuestBackup have their lines only with
# --guest-backup-storage (D-405). A run makes only the roles that its lines
# use; --remove deletes every role of this list.
ROLE_NAMES=(PlumbGuests PlumbDisk PlumbImages PlumbSnippets PlumbBridge PlumbNode PlumbAudit PlumbGuestBackupVM PlumbGuestBackup)
declare -A ROLE_PRIVS=(
	[PlumbGuests]="VM.Allocate VM.Audit VM.Clone VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit"
	[PlumbDisk]="Datastore.AllocateSpace Datastore.Audit"
	[PlumbImages]="Datastore.AllocateTemplate Datastore.Audit"
	[PlumbSnippets]="Datastore.AllocateSpace"
	[PlumbBridge]="SDN.Use"
	[PlumbNode]="Sys.AccessNetwork"
	[PlumbAudit]="Sys.Audit"
	[PlumbGuestBackupVM]="VM.Backup"
	[PlumbGuestBackup]="Datastore.AllocateSpace Datastore.Audit"
)

role_path() {
	case $1 in
	PlumbGuests) echo "/pool/$POOL" ;;
	PlumbDisk) echo "/storage/$DISK_STORAGE" ;;
	PlumbImages) echo "/storage/$IMAGE_STORAGE" ;;
	PlumbSnippets) echo "/storage/$SNIPPET_STORAGE" ;;
	PlumbBridge) echo "/sdn/zones/localnetwork/$BRIDGE" ;;
	PlumbNode) echo "/nodes/$NODE" ;;
	PlumbAudit) echo "/" ;;
	PlumbGuestBackupVM) echo "/pool/$POOL" ;;
	PlumbGuestBackup) echo "/storage/$GUEST_BACKUP_STORAGE" ;;
	esac
}

# role_propagate: only the pool ACLs are inherited (pool members).
role_propagate() {
	case $1 in
	PlumbGuests | PlumbGuestBackupVM) echo 1 ;;
	*) echo 0 ;;
	esac
}

# acl_plan prints the planned ACL lines, "path role propagate" each: one
# per base role, one PlumbBridge line per extra bridge and per bridge/tag
# (propagate 0: a line on a bridge never gives its tags, D-401), and the
# two guest backup lines (D-405, M1 of #321).
acl_plan() {
	local role net
	for role in "${ROLE_NAMES[@]}"; do
		case $role in PlumbGuestBackupVM | PlumbGuestBackup) continue ;; esac
		echo "$(role_path "$role") $role $(role_propagate "$role")"
	done
	for net in "${EXTRA_NETS[@]}"; do
		echo "/sdn/zones/localnetwork/$net PlumbBridge 0"
	done
	if [[ -n $GUEST_BACKUP_STORAGE ]]; then
		for role in PlumbGuestBackupVM PlumbGuestBackup; do
			echo "$(role_path "$role") $role $(role_propagate "$role")"
		done
	fi
}

print_rights() {
	say "Plumb gets exactly these rights (token ${PVE_USER}!${TOKEN_NAME}, privilege separation):"
	local path role propagate
	while read -r path role propagate; do
		printf '  %-38s %-18s %s\n' "$path" "$role" "${ROLE_PRIVS[$role]}"
	done <<<"$(acl_plan)"
	say "Sys.Audit on / is not inherited (propagate 0). It reads the thin-pool metadata use for the disk gate. It also lets"
	say "Plumb read, never write, cluster-wide (D-240):"
	say "  - the task list of all users, root included (no task logs);"
	say "  - the use metrics (CPU, memory, disk, network, uptime, with history) of every guest, node and storage;"
	say "  - the IDs, names and volumes of the guests in backup jobs, and the backup jobs and their config;"
	say "  - the node disk lists and SMART data, the PCI list, /etc/hosts and the node config;"
	say "  - all datacenter options, cluster status and config, HA, metric-server config (not its token),"
	say "    cluster firewall options, rules, security groups, aliases and IP sets, realm sync jobs, Ceph status,"
	say "    the backup job IDs and comments in the notification values, the cluster name at login."
	say "  It reads no config of a guest outside pool $POOL, and no node status, syslog, network or task log."
	say "It gets no Sys.Modify, no Sys.Audit on /nodes or any other path, no Permissions.Modify, Pool.Allocate, console"
	say "or guest-agent right, and no other ACL."
	say "The Plumb LXC is not in pool $POOL, so Plumb cannot change itself or foreign guests."
	if ((${#EXTRA_NETS[@]} > 0)); then
		say "Each PlumbBridge line is one bridge (untagged NICs) or one bridge/VLAN tag; none is inherited, so a"
		say "line on a bridge does not give its tags (D-401). Guest NICs may use only these."
	fi
	if [[ -n $GUEST_BACKUP_STORAGE ]]; then
		say "VM.Backup on /pool/$POOL (inherited by the pool guests) and the storage line on $GUEST_BACKUP_STORAGE are for guest"
		say "backups (D-405). With them the token itself can back up, restore over (overwrite) and delete the backups"
		say "of every guest in pool $POOL directly at the Proxmox API; Plumb's human-only rules hold only inside Plumb."
		say "A guest backup holds the whole guest disk: the agent credential, the secret values of its desired state,"
		say "the SSH host keys and the app data (D-406)."
	fi
}

# --- the vendor snippet (D-025, D-082) ---

# snippet_yaml CA_PEM_FILE URL: the static vendor data. It holds no secret:
# the enrollment token is the SMBIOS serial of each VM.
snippet_yaml() {
	local ca=$1 url=$2 pem_json
	[[ $url =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$|^https://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?$ ]] ||
		die "bad agent URL: $url (https://host:port)"
	grep -q -- '-----BEGIN CERTIFICATE-----' "$ca" || die "$ca holds no PEM certificate"
	# Only the PEM character set: nothing can break the JSON or the YAML.
	if grep -qvE '^(-----(BEGIN|END) CERTIFICATE-----|[A-Za-z0-9+/=]*)\r?$' "$ca"; then
		die "$ca holds more than PEM certificates"
	fi
	# PEM is base64, dashes and spaces only: safe in JSON after joining lines.
	pem_json=$(awk 'BEGIN{ORS="\\n"} {sub(/\r$/, ""); print}' "$ca")
	cat <<EOF
#cloud-config
# Plumb vendor data, written once by install.sh (D-025). No secret: each VM
# gets its one-time enrollment token as its SMBIOS serial.
# A new cloud-init instance (a cloud-init field changed outside Plumb, or
# the host DNS) keeps the SSH host keys (D-185).
ssh_deletekeys: false
write_files:
  - path: /etc/plumb-agent/ca.pem
    owner: root:root
    permissions: '0644'
    content: |
$(sed 's/^/      /' "$ca")
  - path: /etc/plumb-agent/agent.json
    owner: root:root
    permissions: '0644'
    content: |
      {"url": "$url", "ca": "$pem_json"}
  - path: /etc/systemd/system/plumb-agent.service
    owner: root:root
    permissions: '0644'
    content: |
      [Unit]
      Description=Plumb agent
      After=network-online.target
      Wants=network-online.target

      [Service]
      ExecStart=/usr/local/bin/plumb-agent run
      Restart=always
      RestartSec=10

      [Install]
      WantedBy=multi-user.target
  - path: /usr/local/sbin/plumb-agent-install
    owner: root:root
    permissions: '0755'
    content: |
      #!/bin/sh
      # Install the Plumb agent from the controller. Idempotent: it does
      # nothing when the agent runs. Only the pinned Plumb CA is trusted:
      # --capath/--ca-directory replace the system CA store with a directory
      # that holds no hashed CA names (review S1 of T-076).
      set -eu
      if systemctl is-active --quiet plumb-agent.service; then exit 0; fi
      url='$url/agent/v1/binary'
      ca=/etc/plumb-agent/ca.pem
      tmp=\$(mktemp)
      new=/usr/local/bin/.plumb-agent.new
      trap 'rm -f "\$tmp" "\$new"' EXIT
      fetch() {
        if command -v curl >/dev/null 2>&1; then
          curl -fsS --proto =https --connect-timeout 10 --max-time 300 --cacert "\$ca" --capath /etc/plumb-agent -o "\$tmp" "\$url"
        elif command -v wget >/dev/null 2>&1; then
          wget -q --https-only --timeout=60 --tries=1 --ca-certificate="\$ca" --ca-directory=/etc/plumb-agent -O "\$tmp" "\$url"
        elif command -v python3 >/dev/null 2>&1; then
          python3 -c 'import ssl,sys,urllib.request; c=ssl.create_default_context(cafile=sys.argv[1]); open(sys.argv[3],"wb").write(urllib.request.urlopen(sys.argv[2],context=c,timeout=60).read())' "\$ca" "\$url" "\$tmp"
        else
          echo "plumb-agent-install: no curl, wget or python3" >&2; return 1
        fi
      }
      i=0
      until fetch; do
        i=\$((i + 1))
        if [ "\$i" -ge 60 ]; then echo "plumb-agent-install: the controller did not answer" >&2; exit 1; fi
        sleep 10
      done
      # A temp name and a rename: a crash or a full disk never leaves a half
      # binary under the real name (review S4 of T-038).
      install -m 0755 "\$tmp" "\$new"
      mv -f "\$new" /usr/local/bin/plumb-agent
      systemctl daemon-reload
      systemctl enable --now plumb-agent.service
runcmd:
  - [/usr/local/sbin/plumb-agent-install]
EOF
}

# ensure_snippet_storage: a dir storage with content "snippets" only. The
# directory and the snippet belong to root; the token can only read
# (Datastore.Audit), never write (D-032).
ensure_snippet_storage() {
	run install -d -o root -g root -m 0755 "$SNIPPET_DIR" "$SNIPPET_DIR/snippets"
	if query pvesm status --storage "$SNIPPET_STORAGE" >/dev/null 2>&1; then
		: # exists with the content "snippets" only (check_storages)
	else
		run pvesm add dir "$SNIPPET_STORAGE" --path "$SNIPPET_DIR" --content snippets --shared 0
	fi
	SNIPPETS_DONE=1
}

write_snippet() {
	local ca=$1 url=$2 tmp
	new_tmp tmp
	snippet_yaml "$ca" "$url" >"$tmp"
	if [[ $DRY_RUN == 1 ]]; then
		say "--- snippet $SNIPPET_DIR/snippets/$SNIPPET ---"
		cat "$tmp"
		say "--- end of snippet ---"
	fi
	run install -o root -g root -m 0644 "$tmp" "$SNIPPET_DIR/snippets/$SNIPPET"
	rm -f "$tmp"
}

# --- install ---

host_ip() {
	local line
	line=$({ query ip -4 -o addr show dev "$BRIDGE" || true; } | head -n1)
	[[ $line =~ inet\ ([0-9.]+)/ ]] || die "no IPv4 address on $BRIDGE"
	echo "${BASH_REMATCH[1]}"
}

# storage_content ID prints the content types of a storage, or fails.
storage_content() {
	local cfg
	cfg=$(query pvesh get "/storage/$1" --output-format json 2>/dev/null) || return 1
	[[ $cfg =~ \"content\"\ *:\ *\"([^\"]*)\" ]] || return 1
	echo "${BASH_REMATCH[1]}"
}

# content_set LIST prints a content list as a sorted set: pvesm set
# reorders the lists (host G0), so they are compared as sets (D-229).
content_set() { awk -v RS=, 'NF { print $1 }' <<<"$1" | sort -u | paste -sd, -; }

# The image storage has exactly {import} (an install from before D-229)
# or exactly {import, vztmpl} (D-081, D-229).
image_content_ok() {
	local s
	s=$(content_set "$1")
	[[ $s == import || $s == import,vztmpl ]]
}

# vztmpl_note STORAGE C says that LXC Images need "vztmpl" on a storage
# that install.sh does not change.
vztmpl_note() {
	[[ $(content_set "$2") == import ]] || return 0
	say "NOTE: storage $1 has the content \"import\" only. VM Images work; LXC Images need \"import,vztmpl\"."
	say "install.sh does not change a storage that it did not make. After the new plumb runs: pvesm set $1 --content import,vztmpl"
}

# check_storages refuses, before any change, a storage setup that would
# let the token reach another snippet or overwrite foreign files (D-081).
check_storages() {
	local c
	c=$(storage_content "$DISK_STORAGE") || die "storage $DISK_STORAGE does not exist"
	[[ ,$c, != *,snippets,* ]] ||
		die "disk storage $DISK_STORAGE has the content \"snippets\": the token could point cicustom at any snippet there (D-081). Use a disk storage without snippets. Nothing was changed."
	if [[ $IMAGE_STORAGE != plumb-images ]]; then
		c=$(storage_content "$IMAGE_STORAGE") || die "storage $IMAGE_STORAGE does not exist"
		image_content_ok "$c" ||
			die "image storage $IMAGE_STORAGE has the content \"$c\"; it must have exactly \"import\" or \"import,vztmpl\" (D-081, D-229). Leave --image-storage out to get plumb-images. Nothing was changed."
		vztmpl_note "$IMAGE_STORAGE" "$c"
	elif c=$(storage_content plumb-images); then
		image_content_ok "$c" || die "storage plumb-images exists with the content \"$c\", not \"import\" or \"import,vztmpl\". Nothing was changed."
	fi
	if c=$(storage_content "$SNIPPET_STORAGE"); then
		[[ $c == snippets ]] || die "storage $SNIPPET_STORAGE exists with the content \"$c\", not \"snippets\". Nothing was changed."
	fi
}

# ensure_image_storage makes the dir storage plumb-images (content import
# and vztmpl, D-229; root-owned). It never changes a storage that it did
# not make; the content of ours changes in set_image_content.
ensure_image_storage() {
	[[ $IMAGE_STORAGE == plumb-images ]] || return 0
	if [[ $IMAGES_PLAN == make ]]; then
		run install -d -o root -g root -m 0755 "$IMAGE_DIR"
		run pvesm add dir plumb-images --path "$IMAGE_DIR" --content import,vztmpl --shared 0
		IMAGES_MADE=1
	elif [[ $IMAGES_PLAN == ours || $IMAGES_PLAN == adopt ]]; then
		IMAGES_MADE=1
	fi
}

# check_images decides, before any change, what happens with plumb-images
# (IMAGES_PLAN): make it, keep ours (state entry), adopt it (an earlier run
# made it and stopped before its state file: test host, T-013 step 3), or
# use it as foreign (--remove keeps it). It prints the decision. A storage
# that is used or adopted must have a safe directory (review S3).
IMAGES_PLAN=""
check_images() {
	[[ $IMAGE_STORAGE == plumb-images ]] || return 0
	if ! storage_content plumb-images >/dev/null; then
		IMAGES_PLAN="make"
		return 0
	fi
	local cfg path
	cfg=$(query pvesh get /storage/plumb-images --output-format json 2>/dev/null) || die "cannot read storage plumb-images"
	[[ $cfg =~ \"path\"\ *:\ *\"([^\"]*)\" ]] || die "storage plumb-images has no path. Nothing was changed."
	path=${BASH_REMATCH[1]}
	check_image_dir "$path"
	if [[ -n $(state_get image_dir) ]]; then
		IMAGES_PLAN=ours
	elif images_like_ours "$cfg"; then
		IMAGES_PLAN=adopt
		say "Storage plumb-images ($IMAGE_DIR) exists without a state entry; it is exactly what install.sh makes, so it is adopted (--remove removes it)."
	else
		IMAGES_PLAN=foreign
		say "Storage plumb-images exists, but not as install.sh makes it; it is used and --remove keeps it."
	fi
	local c
	c=$(storage_content plumb-images) || die "cannot read storage plumb-images"
	if [[ $IMAGES_PLAN == foreign ]]; then
		vztmpl_note plumb-images "$c"
	elif [[ $(content_set "$c") == import ]]; then
		# An install from before D-229 (upgrade, D-084: shown, then asked).
		IMAGES_SET_CONTENT=1
		say "Change: storage plumb-images content import → import,vztmpl (LXC templates, D-229)."
		say "  Order: the new plumb binary first, then the content, then the restart of plumb.service;"
		say "  an older plumb refuses the new content at its start (exit 78)."
		say "  pvesm set rewrites /etc/pve/storage.cfg and reorders the content lists of other storages"
		say "  (for example local, local-lvm): same content, other order."
	fi
}

# set_image_content changes the content of plumb-images to import,vztmpl
# (D-229). It runs after the new binary is in place and before the
# restart. From here until the new binary runs, a failed run sets the
# content back to import (on_exit, review S9 of T-038).
IMAGES_SET_CONTENT=0
CONTENT_ROLLBACK=0
set_image_content() {
	[[ $IMAGES_SET_CONTENT == 1 ]] || return 0
	[[ $DRY_RUN == 1 ]] || CONTENT_ROLLBACK=1
	run pvesm set plumb-images --content import,vztmpl
	IMAGES_SET_CONTENT=0
}

# rollback_image_content sets the content of plumb-images back to import
# and says what it did. It runs from the exit trap, also after SIGHUP: the
# pvesm set comes first, because the terminal can be gone and a write to
# it can fail; no write stops the trap.
rollback_image_content() {
	CONTENT_ROLLBACK=0
	trap '' PIPE
	local ok=1
	run pvesm set plumb-images --content import || ok=0
	{
		echo "install.sh: the run stopped before the new plumb was checked; the content of plumb-images is set back to \"import\" (D-229)."
		if [[ $ok == 1 ]]; then
			echo "install.sh: plumb-images has the content \"import\" again. VM Images work; LXC Images need a repair run (run install.sh again)."
			echo "install.sh: if plumb.service runs, restart it so that it reads the content again: pct exec $LXC_ID -- systemctl restart plumb.service"
		else
			echo "install.sh: cannot set the content of plumb-images back."
		fi
		echo "install.sh: before you put an older plumb binary back by hand, run: pvesm set plumb-images --content import"
	} >&2 2>/dev/null || true
}

# check_image_dir refuses an image directory that someone else could
# change: it must be a real directory (no symbolic link), owned by root,
# not writable by group or others.
check_image_dir() {
	local dir=$1 st
	[[ -d $dir && ! -L $dir ]] || die "the directory $dir of storage plumb-images is missing or a symbolic link. Nothing was changed."
	st=$(query stat -c '%U %a' -- "$dir") || die "cannot read $dir"
	[[ ${st%% *} == root ]] || die "$dir belongs to ${st%% *}, not root. Nothing was changed."
	(( (8#${st##* } & 8#022) == 0 )) || die "$dir is writable by group or others (mode ${st##* }). Nothing was changed."
}

# images_like_ours reports whether plumb-images is exactly the storage that
# ensure_image_storage makes: type dir, path IMAGE_DIR, content import (an
# install from before D-229) or import,vztmpl, not shared.
images_like_ours() {
	local cfg=$1
	# Only the keys that pvesm add dir … --content import --shared 0 gives
	# (review S3): another option (nodes, disable, is_mountpoint …) means
	# someone else set it up.
	# shellcheck disable=SC2016 # perl code
	json_rows 'my %ok = map { $_ => 1 } qw(content digest path shared storage type);
		for (keys %$d) { exit 1 unless $ok{$_} } exit 0' <<<"$cfg" || return 1
	[[ $cfg =~ \"type\"\ *:\ *\"dir\" ]] &&
		[[ $cfg =~ \"path\"\ *:\ *\"([^\"]*)\" && ${BASH_REMATCH[1]} == "$IMAGE_DIR" ]] &&
		[[ $cfg =~ \"content\"\ *:\ *\"([^\"]*)\" ]] && image_content_ok "${BASH_REMATCH[1]}" &&
		! [[ $cfg =~ \"shared\"\ *:\ *\"?1 ]]
}

# json_rows runs a perl filter over JSON on stdin (perl is on every PVE host).
json_rows() { perl -MJSON::PP -e 'my $d = decode_json(join "", <STDIN>); '"$1" "${@:2}"; }

# acl_entries prints "path ugid role type propagate" for every ACL of the Plumb user
# and token.
acl_entries() {
	# shellcheck disable=SC2016 # perl code
	query pveum acl list --output-format json |
		json_rows 'for my $e (@$d) { next unless $e->{ugid} eq $ARGV[0] || $e->{ugid} eq $ARGV[1];
			print "$e->{path} $e->{ugid} $e->{roleid} $e->{type} ", ($e->{propagate} ? 1 : 0), "\n" }' "$PVE_USER" "$PVE_USER!$TOKEN_NAME"
}

# stray_acls prints the ACLs of the Plumb user and token that are not in
# the plan, for example the single role of D-032 from an older install
# (review T-013 #4).
stray_acls() {
	local acls
	# Read the list first: a failing pveum or perl stops the run here
	# instead of looking like "no old rights" (review T-013 n1).
	acls=$(acl_entries) || die "cannot read the ACLs of $PVE_USER; nothing was changed"
	stray_of "$acls"
}

# stray_of ACLS prints the entries of ACLS (acl_entries lines) that are
# not in the plan.
stray_of() {
	local path ugid role type propagate planned
	planned=$(acl_plan | awk '{ print $1, $2 }')
	while read -r path ugid role type propagate; do
		[[ -n $path ]] || continue
		grep -qxF "$path $role" <<<"$planned" || printf '%s %s %s %s\n' "$path" "$ugid" "$role" "$type"
	done <<<"$1"
}

# check_user_groups refuses an existing Plumb user that is in a group: the
# ACLs of the group would count for the user and the token too (review
# T-013 n2).
check_user_groups() {
	local groups
	# shellcheck disable=SC2016 # perl code
	groups=$(query pveum user list --output-format json |
		json_rows 'for my $e (@$d) { next unless $e->{userid} eq $ARGV[0]; my $g = $e->{groups} // ""; $g = join ",", @$g if ref $g; print $g }' "$PVE_USER") ||
		die "cannot read the users; nothing was changed"
	[[ -z $groups ]] || die "user $PVE_USER is in the groups \"$groups\"; their ACLs would add rights to Plumb (D-081). Remove the user from them. Nothing was changed."
}


# check_role fails when a role does not have exactly its planned rights
# (host item 6: --privs must replace, not append).
check_role() {
	[[ $DRY_RUN == 1 ]] && return 0
	role_exact "$1" || die "role $1 has the rights [$ROLE_HAVE], want [${ROLE_WANT% }]; fix it with pveum, then run again"
}

# role_exact reports whether a role has exactly its planned rights. It only
# reads, so it runs in --dry-run too. It sets ROLE_HAVE and ROLE_WANT.
ROLE_HAVE="" ROLE_WANT=""
role_exact() {
	# shellcheck disable=SC2016 # perl code
	ROLE_HAVE=$(query pveum role list --output-format json |
		json_rows 'for my $e (@$d) { next unless $e->{roleid} eq $ARGV[0]; my $p = $e->{privs}; $p = join ",", @$p if ref $p;
			print join(" ", sort split /[, ]+/, $p), "\n" }' "$1")
	ROLE_WANT=$(tr ' ' '\n' <<<"${ROLE_PRIVS[$1]}" | LC_ALL=C sort | tr '\n' ' ')
	[[ "$ROLE_HAVE " == "$ROLE_WANT" ]]
}

ensure_pool_user_roles() {
	if ! query pveum pool list --output-format json | json_has poolid "$POOL"; then
		run pveum pool add "$POOL" --comment "Plumb guests and templates"
	fi
	local roles role
	roles=$(query pveum role list --output-format json)
	for role in $(acl_plan | awk '!seen[$2]++ { print $2 }'); do
		if json_has roleid "$role" <<<"$roles"; then
			# An exact role is kept, so a repair run lists only what is missing.
			role_exact "$role" || run pveum role modify "$role" --privs "${ROLE_PRIVS[$role]// /,}" # replaces: exactly these
		else
			run pveum role add "$role" --privs "${ROLE_PRIVS[$role]// /,}"
		fi
		check_role "$role"
	done
	if ! query pveum user list --output-format json | json_has userid "$PVE_USER"; then
		run pveum user add "$PVE_USER" --comment "Plumb controller (API token only, no password)"
	fi
}

# ensure_acls gives user and token the planned ACLs and removes the others.
# It runs after ensure_token: Proxmox refuses an ACL for a token that does
# not exist yet ("no such token"; test host, T-013 step 3). A new token
# gets every planned ACL set here: PVE 9.2 gives it the lines of an older
# token with the same name that were not deleted (T-122 F4), and these
# lines are not listed before the token exists. So the list is read again
# now, and its stray entries replace the ones of the preview.
ensure_acls() {
	local acls new_token=0 path ugid role type propagate i steps=()
	[[ -n $TOKEN_SECRET ]] && new_token=1
	acls=$(acl_entries) || die "cannot read the ACLs of $PVE_USER"
	STRAY_ACLS=$(stray_of "$acls")
	# Narrow first (review M2 of #330): the old lines go before the new
	# ones come, so a run that stops half way leaves fewer rights, not more.
	while read -r path ugid role type; do
		[[ -n $path ]] && steps+=("remove $path $ugid $role $type")
	done <<<"$STRAY_ACLS"
	while read -r path role propagate <&3; do
		# An ACL that exists as planned is kept (repair run).
		grep -qxF "$path $PVE_USER $role user $propagate" <<<"$acls" ||
			steps+=("add $path $PVE_USER $role user $propagate")
		if [[ $new_token == 1 ]] || ! grep -qxF "$path $PVE_USER!$TOKEN_NAME $role token $propagate" <<<"$acls"; then
			steps+=("add $path $PVE_USER!$TOKEN_NAME $role token $propagate")
		fi
	done 3<<<"$(acl_plan)"
	for ((i = 0; i < ${#steps[@]}; i++)); do
		# shellcheck disable=SC2086 # one word per field: checked names
		acl_step ${steps[i]} || acl_failed "${steps[@]:i}"
	done
}

# acl_step OP PATH UGID ROLE TYPE [PROPAGATE] removes or adds one ACL line.
acl_step() {
	local flag=--users
	[[ $5 == token ]] && flag=--tokens
	if [[ $1 == remove ]]; then
		run pveum acl delete "$2" "$flag" "$3" --roles "$4"
	else
		run pveum acl modify "$2" "$flag" "$3" --roles "$4" --propagate "$6"
	fi
}

# acl_failed STEP… stops the run after a failed ACL step. It names the step
# and every step of this run that is not done, so the person knows which
# rights the token still has or still lacks (review M2 of #330).
acl_failed() {
	{
		echo "install.sh: the ACL step \"$1\" failed (see the pveum error above)."
		echo "These ACL steps of this run are not done; every other line is as planned:"
		printf '  %s\n' "$@"
		echo "A \"remove\" line above still gives its rights to Plumb; an \"add\" line is a right that Plumb lacks."
		echo "Run the same install.sh command again (a repair run)."
	} >&2
	exit 1
}

# remove_token deletes the ACLs of the Plumb token, then the token. PVE
# 9.2.26 keeps the ACL lines of a removed token in user.cfg (every pveum,
# qm and pct call then warns "ignore invalid acl token"), and a new token
# with the same name gets them again (T-122 F4). Proxmox refuses to delete
# an ACL of a token that does not exist, so the ACLs go first. The ACLs of
# the user stay.
remove_token() {
	local acls path ugid role type propagate
	acls=$(acl_entries) || die "cannot read the ACLs of $PVE_USER!$TOKEN_NAME; the token was not removed"
	while read -r path ugid role type propagate; do
		[[ $type == token && $ugid == "$PVE_USER!$TOKEN_NAME" ]] || continue
		run pveum acl delete "$path" --tokens "$ugid" --roles "$role"
	done <<<"$acls"
	run pveum user token remove "$PVE_USER" "$TOKEN_NAME"
}

# ensure_token sets TOKEN_SECRET when it makes a new token. An existing
# token is kept: its secret is already in the LXC.
TOKEN_SECRET=""
ensure_token() {
	if query pveum user token list "$PVE_USER" --output-format json 2>/dev/null | json_has tokenid "$TOKEN_NAME"; then
		if lxc_has_env; then
			say "Token $PVE_USER!$TOKEN_NAME exists and the Plumb LXC holds it; it is kept."
			return 0
		fi
		confirm "Token $PVE_USER!$TOKEN_NAME exists, but no Plumb LXC holds it. Replace the token?" yes ||
			die "stopped; the token was not changed"
		remove_token
	fi
	local out
	if [[ $DRY_RUN == 1 ]]; then
		run pveum user token add "$PVE_USER" "$TOKEN_NAME" --privsep 1 --comment "Plumb controller" --output-format json
		TOKEN_SECRET=dry-run-secret
		return 0
	fi
	out=$(pveum user token add "$PVE_USER" "$TOKEN_NAME" --privsep 1 --comment "Plumb controller" --output-format json)
	[[ $out =~ \"value\"\ *:\ *\"([0-9a-fA-F-]+)\" ]] || die "cannot read the new token (the token was created; run again to replace it)"
	TOKEN_SECRET=${BASH_REMATCH[1]}
}

# --- state on the host (no secret) ---

state_get() {
	[[ -f $STATE_FILE ]] || return 0
	sed -n "s/^$1=//p" "$STATE_FILE" | head -n1
}

# state_put writes the state file. It runs after each object that --remove
# must know, so a run that stops halfway leaves a state that names what it
# made (T-013 step 3). It writes nothing when the state did not change.
STATE_WRITTEN=""
state_put() {
	# image_dir is set only when this script made plumb-images, so
	# --remove never removes a storage that existed before (review n3).
	local images="" lxc content
	[[ $IMAGES_MADE == 1 ]] && images=$IMAGE_DIR
	# lxc_id names only a container that this script made or checked as its
	# own; an id from --lxc-id is not written before that.
	if [[ $LXC_OURS == 1 ]]; then
		lxc=$LXC_ID
	else
		lxc=$(state_get lxc_id)
	fi
	# snippet_dir is written only after ensure_snippet_storage ran: --remove
	# deletes the snippet there, so it must never name a directory that this
	# run did not set up (review S2, D-084).
	local snippets
	if [[ $SNIPPETS_DONE == 1 ]]; then
		snippets=$SNIPPET_DIR
	else
		snippets=$(state_get snippet_dir)
	fi
	# install_id names the install in every backup (D-304); backup_storage
	# the storage of the copy timer.
	local id=${INSTALL_ID:-$(state_get install_id)} copy
	if [[ -n $BACKUP_STORAGE_SET || $BACKUP_STORAGE == none ]]; then
		copy=$BACKUP_STORAGE_SET
	else
		copy=$(state_get backup_storage)
	fi
	content=$(printf 'lxc_id=%s\npool=%s\nsnippet_storage=%s\nsnippet_dir=%s\nimage_dir=%s\ninstall_id=%s\nbackup_storage=%s' \
		"$lxc" "$POOL" "$SNIPPET_STORAGE" "$snippets" "$images" "$id" "$copy")
	# A kept state (--remove --keep-guests) stays until a restore or an
	# install made a new LXC that runs; while a restore works, the mark
	# restoring=1 names its LXC (review M2 of #221).
	if [[ $(state_get kept_guests) == 1 && ($LXC_OURS != 1 || $RESTORING == 1) ]]; then
		content+=$(printf '\nkept_guests=1\nnode=%s\nold_address=%s\nold_gateway=%s' "$(state_get node)" "$(state_get old_address)" "$(state_get old_gateway)")
	fi
	[[ $RESTORING == 1 ]] && content+=$'\nrestoring=1'
	
	if [[ $content == "$STATE_WRITTEN" ]] || { [[ -f $STATE_FILE ]] && [[ $content == "$(cat "$STATE_FILE")" ]]; }; then
		return 0
	fi
	run install -d -o root -g root -m 0700 "$(dirname "$STATE_FILE")"
	local tmp
	new_tmp tmp
	printf '%s\n' "$content" >"$tmp"
	run install -o root -g root -m 0600 "$tmp" "$STATE_FILE"
	rm -f "$tmp"
	STATE_WRITTEN=$content
}

# --- DNS of new VMs (D-187) ---

# check_dns sets NAMESERVER and SEARCHDOMAIN from the options, else from
# the host: the nameserver lines of RESOLV (loopback addresses left out: a
# guest cannot use the host's local resolver) and its search line, else the
# domain of the host name. It stops before any change without them.
check_dns() {
	local ns=() key val _ word
	if [[ -z $NAMESERVER && -r $RESOLV ]]; then
		while read -r key val _; do
			[[ $key == nameserver && -n $val ]] || continue
			[[ $val == 127.* || $val == ::1 || $val == 0.0.0.0 || $val == :: ]] && continue
			ns+=("$val")
		done <"$RESOLV"
		NAMESERVER=$(IFS=,; echo "${ns[*]:0:3}")
	fi
	if [[ -z $SEARCHDOMAIN && -r $RESOLV ]]; then
		word=$(awk '$1 == "search" || $1 == "domain" {print $2; exit}' "$RESOLV")
		[[ $word == . ]] && word=""
		SEARCHDOMAIN=$word
	fi
	if [[ -z $SEARCHDOMAIN ]]; then
		SEARCHDOMAIN=$(query hostname -d 2>/dev/null || true)
	fi
	[[ -n $NAMESERVER ]] || die "no DNS server for new VMs: $RESOLV has no usable nameserver line (loopback does not count); use --nameserver IP. Nothing was changed."
	[[ -n $SEARCHDOMAIN ]] || die "no DNS search domain for new VMs: $RESOLV has no search line and the host name has no domain; use --searchdomain NAME. Nothing was changed."
	# The whole value first: a newline (or any other character) must never
	# reach plumb.env, where it would add a line (review S1).
	[[ $NAMESERVER =~ ^[0-9A-Fa-f.:]+(,[0-9A-Fa-f.:]+){0,2}$ ]] ||
		die "bad name servers \"$NAMESERVER\": 1 to 3 IP addresses, comma-separated. Nothing was changed."
	local a
	IFS=, read -r -a ns <<<"$NAMESERVER"
	for a in "${ns[@]}"; do
		valid_ip "$a" || die "bad name server $a. Nothing was changed."
	done
	[[ $SEARCHDOMAIN =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$ ]] ||
		die "bad search domain $SEARCHDOMAIN. Nothing was changed."
	say "New VMs get the DNS servers $NAMESERVER and the search domain $SEARCHDOMAIN (fixed at create, D-187)."
}

# valid_ip reports whether $1 is one IPv4 address (octets up to 255) or one
# IPv6 address (hex groups, at most one "::", no loopback or unspecified).
valid_ip() {
	local a=$1 o groups
	if [[ $a =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]]; then
		for o in "${BASH_REMATCH[@]:1}"; do
			((10#$o <= 255)) || return 1
		done
		[[ $a != 0.0.0.0 && $a != 127.* ]]
		return
	fi
	[[ $a =~ ^[0-9A-Fa-f:]+$ && $a == *:*:* && $a != *:::* && $a != ::1 && $a != :: ]] || return 1
	[[ ${a//[^:]/} != *:*:*:*:*:*:*:*:* ]] || return 1 # at most 7 colons
	local rest=${a/::/:}
	[[ $rest != *::* ]] || return 1 # one "::" only
	IFS=: read -r -a groups <<<"$rest"
	for o in "${groups[@]}"; do
		[[ -z $o || ${#o} -le 4 ]] || return 1
	done
	if [[ $a != *::* ]]; then
		[[ ${a//[^:]/} == ::::::: ]] || return 1 # 8 groups without "::"
	fi
}

# add_env_dns adds the D-187 lines to an existing plumb.env that lacks
# them (an install before D-187). It needs no secret.
add_env_dns() {
	local k v
	for k in PLUMB_PVE_NAMESERVER PLUMB_PVE_SEARCHDOMAIN; do
		v=$NAMESERVER
		[[ $k == PLUMB_PVE_SEARCHDOMAIN ]] && v=$SEARCHDOMAIN
		if [[ $DRY_RUN == 1 ]] || ! query pct exec "$LXC_ID" -- grep -q "^$k=" /etc/plumb/plumb.env </dev/null; then
			run pct exec "$LXC_ID" -- sh -c "test -f /etc/plumb/plumb.env && { grep -q '^$k=' /etc/plumb/plumb.env || echo '$k=$v' >>/etc/plumb/plumb.env; }" </dev/null
		fi
	done
}

# --- the Plumb LXC (D-006, D-083) ---

# LXC_MARKER is in the description of the LXC that this script made. An
# existing container is used or destroyed only when it carries the marker
# and the Plumb host name (review T-013 #2).
readonly LXC_MARKER=plumb-installer-lxc

# lxc_state ID prints "none", "ours" or "foreign".
lxc_state() {
	local cfg
	if [[ $DRY_RUN == 1 && -n $DRY_GONE_LXC && $1 == "$DRY_GONE_LXC" ]]; then
		echo none
		return 0
	fi
	if ! query pct status "$1" >/dev/null 2>&1; then
		echo none
		return 0
	fi
	cfg=$(query pct config "$1" 2>/dev/null || true)
	if grep -q "^description: .*$LXC_MARKER" <<<"$cfg" && grep -qx "hostname: $LXC_HOSTNAME" <<<"$cfg"; then
		echo ours
	else
		echo foreign
	fi
}

# lxc_has_env reports whether our LXC holds the token env file.
lxc_has_env() {
	[[ -n $LXC_ID && $(lxc_state "$LXC_ID") == ours ]] &&
		query pct exec "$LXC_ID" -- test -s /etc/plumb/plumb.env </dev/null >/dev/null 2>&1
}

# lxc_template sets TEMPLATE to the newest Debian 13 template for the host's
# architecture, and downloads it through Proxmox's own template list when it
# is missing. The list has one entry per architecture (amd64, arm64), and
# "arm64" sorts after "amd64", so the filter on the architecture is required.
TEMPLATE=""
lxc_template() {
	local name arch re
	arch=$(query dpkg --print-architecture)
	[[ $arch =~ ^[a-z0-9]+$ ]] || die "cannot read the host architecture: $arch"
	re="debian-13-standard_.*_${arch}[.]tar[.]"
	name=$({ query pveam list "$TEMPLATE_STORAGE" || true; } | awk -v re="$re" '$1 ~ re {sub(/.*vztmpl\//, "", $1); print $1}' | sort -V | tail -n1)
	if [[ -z $name ]]; then
		run pveam update
		name=$(query pveam available --section system | awk -v re="$re" '$2 ~ re {print $2}' | sort -V | tail -n1)
		[[ -n $name ]] || die "no debian-13-standard template for $arch in the Proxmox template list"
		run pveam download "$TEMPLATE_STORAGE" "$name"
	fi
	TEMPLATE="$TEMPLATE_STORAGE:vztmpl/$name"
}

# Proxmox leaves $LXC_DIR/<id> behind for an id without a container:
# pct destroy --purge keeps rules.seccomp and passthrough/ (its rmdir of the
# dir fails, pve-container 6.1), a failed pct create keeps an empty rootfs/
# (0700). PVE reuses the dir (pct start rewrites rules.seccomp and makes
# passthrough/), only this script checks it. stale_lxc_entry prints the
# first entry that is not in that known set (a regular file rules.seccomp,
# the empty dirs passthrough and rootfs), or the dir itself when it is a
# symbolic link, with the reason. It only reads.
stale_lxc_entry() {
	local dir=$LXC_DIR/$1 e name
	[[ -L $dir ]] && { echo "$dir is a symbolic link"; return 0; }
	[[ -d $dir ]] || return 0
	for e in "$dir"/* "$dir"/.[!.]* "$dir"/..?*; do
		[[ -e $e || -L $e ]] || continue
		name=${e##*/}
		if [[ -L $e ]]; then
			echo "$e is a symbolic link"
			return 0
		fi
		case $name in
		rules.seccomp) [[ -f $e ]] || { echo "$e is not a regular file"; return 0; } ;;
		passthrough | rootfs)
			[[ -d $e ]] || { echo "$e is not a directory"; return 0; }
			if [[ -n $(find "$e" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null) ]]; then
				echo "$e is not empty"
				return 0
			fi
			;;
		*) echo "$e is not a known leftover of Proxmox"; return 0 ;;
		esac
	done
	return 0
}

# check_stale_lxc_dir stops before any change when $LXC_DIR/<id> holds
# anything outside the known set (no container with that id).
check_stale_lxc_dir() {
	[[ -n $1 && $(lxc_state "$1") == none ]] || return 0
	local bad
	bad=$(stale_lxc_entry "$1")
	[[ -z $bad ]] || die "$bad, and container $1 does not exist; check it by hand. Nothing was changed."
}

# clean_stale_lxc_dir removes the known leftovers and the dir: only a
# regular file and empty dirs, never through a symbolic link, no rm -r.
# With "die" an unknown entry stops the run; with "keep" it is kept and named.
clean_stale_lxc_dir() {
	local id=$1 mode=$2 dir=$LXC_DIR/$1 bad
	if [[ $DRY_RUN == 1 && $id == "$DRY_GONE_LXC" ]]; then
		say "(dry run: the leftovers of LXC $id in $dir go with it)"
		return 0
	fi
	[[ -e $dir || -L $dir ]] || return 0
	# Only the dir of an id without a container is a leftover (review nit).
	if [[ $(lxc_state "$id") != none ]]; then
		[[ $mode == die ]] && die "container $id exists; $dir is not a leftover"
		say "Kept $dir: container $id exists."
		return 0
	fi
	bad=$(stale_lxc_entry "$id")
	if [[ -n $bad ]]; then
		[[ $mode == die ]] && die "$bad, and container $id does not exist; check it by hand"
		say "Kept $dir: $bad."
		return 0
	fi
	[[ -f $dir/rules.seccomp ]] && run rm -f -- "$dir/rules.seccomp"
	local d
	for d in passthrough rootfs; do
		[[ -d $dir/$d ]] && run rmdir -- "$dir/$d"
	done
	run rmdir -- "$dir"
}

ensure_lxc() {
	if [[ -z $LXC_ID ]]; then
		LXC_ID=$(state_get lxc_id)
	fi
	if [[ -n $LXC_ID ]]; then
		case $(lxc_state "$LXC_ID") in
		ours)
			say "LXC $LXC_ID exists; it is kept."
			LXC_NEW=0
			LXC_OURS=1
			# A run that stopped after pct create left it stopped (review S1).
			if [[ $(query pct status "$LXC_ID" 2>/dev/null) == *stopped* ]]; then
				(umask 022 && run pct start "$LXC_ID")
				wait_booted
			fi
			return 0
			;;
		foreign)
			die "container $LXC_ID exists but was not made by this script (no $LXC_MARKER marker or host name $LXC_HOSTNAME); nothing is written into it"
			;;
		esac
	fi
	if [[ -z $LXC_ID ]]; then
		LXC_ID=$(query pvesh get /cluster/nextid)
	fi
	local net="name=eth0,bridge=$BRIDGE,ip=$LXC_IP"
	[[ -n $LXC_GW ]] && net+=",gw=$LXC_GW"
	lxc_template
	clean_stale_lxc_dir "$LXC_ID" die
	# Unprivileged, no host mount, not in the Plumb pool (ARCHITECTURE §7).
	# mp0 is a new volume on the disk storage (not a host path) with
	# backup=0: the key volume (D-177).
	# umask 022: the uid-mapped root of an unprivileged container must enter
	# the rootfs directory that pct makes; with the umask 077 of this script
	# the extraction fails ("rootfs: Permission denied", test host, T-013).
	(umask 022 && run pct create "$LXC_ID" "$TEMPLATE" --hostname "$LXC_HOSTNAME" --unprivileged 1 --features nesting=1 \
		--cores 2 --memory 1024 --swap 512 --rootfs "$DISK_STORAGE:8" \
		--mp0 "$DISK_STORAGE:$KEY_VOLUME_GB,mp=$KEY_DIR,backup=0" --net0 "$net" --onboot 1 --ostype debian \
		--description "$LXC_MARKER Plumb controller, made by install.sh")
	# The state names the LXC before anything else can fail, so a failed
	# start never leads to a second LXC on the next run (review S1).
	LXC_NEW=1
	LXC_OURS=1
	state_put
	(umask 022 && run pct start "$LXC_ID")
	wait_booted
}

# wait_booted waits until systemd in the LXC has finished its boot (T-132):
# the steps after a start must not race the boot of the container (the
# gettys on its terminals, logind). Bounded inside the LXC by timeout.
readonly BOOT_WAIT=120
wait_booted() {
	[[ $DRY_RUN != 1 ]] || return 0
	local state
	state=$(query pct exec "$LXC_ID" -- timeout "$BOOT_WAIT" systemctl is-system-running --wait 2>/dev/null) || true
	case $state in
	running | degraded) return 0 ;;
	esac
	die "LXC $LXC_ID did not finish its boot within ${BOOT_WAIT}s (systemctl is-system-running: ${state:-no answer}); see: pct exec $LXC_ID -- systemctl --failed"
}

lxc_ip() {
	if [[ $LXC_IP != dhcp ]]; then
		echo "${LXC_IP%/*}"
		return 0
	fi
	if [[ $DRY_RUN == 1 ]]; then
		echo "LXC-IP"
		return 0
	fi
	local ip
	for _ in $(seq 1 60); do
		ip=$({ query pct exec "$LXC_ID" -- hostname -I </dev/null 2>/dev/null || true; } | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -n1 || true)
		[[ -n $ip ]] && {
			echo "$ip"
			return 0
		}
		sleep 2
	done
	die "the LXC got no address within 120 s"
}

# push_env writes /etc/plumb/plumb.env into the LXC (mode 0600, owner
# plumb). It is the only place of the token secret; it never reaches the
# terminal (D-083).
push_env() {
	local host=$1 ip=$2 tmp
	new_tmp tmp
	cat >"$tmp" <<EOF
# Plumb controller settings, written by install.sh. Mode 0600.
PLUMB_PVE_URL=https://$host:8006
PLUMB_PVE_TOKEN_ID=$PVE_USER!$TOKEN_NAME
PLUMB_PVE_SECRET=$TOKEN_SECRET
PLUMB_PVE_CA=/etc/plumb/pve-root-ca.pem
PLUMB_PVE_NODE=$NODE
PLUMB_PVE_POOL=$POOL
PLUMB_PVE_DISK=$DISK_STORAGE
PLUMB_PVE_IMAGES=$IMAGE_STORAGE
PLUMB_PVE_SNIPPETS=$SNIPPET_STORAGE
PLUMB_PVE_SNIPPET=$SNIPPET
PLUMB_PVE_BRIDGE=$BRIDGE
PLUMB_PVE_VMID_RANGE=$VMID_MIN-$VMID_MAX
PLUMB_PVE_NAMESERVER=$NAMESERVER
PLUMB_PVE_SEARCHDOMAIN=$SEARCHDOMAIN
PLUMB_HOSTS=$ip,$LXC_HOSTNAME
$(env_backup_lines)
$(env_slice7_lines)
EOF
	if ! (push_checked "$tmp" /etc/plumb/plumb.env 0600); then
		rm -f "$tmp"
		die "cannot write the settings into the LXC"
	fi
	rm -f "$tmp"
	run pct exec "$LXC_ID" -- chown plumb:plumb /etc/plumb/plumb.env </dev/null
}

# The unit keeps ${PLUMB_HOSTS} for systemd to expand from the env file.
# shellcheck disable=SC2016
readonly UNIT='[Unit]
Description=Plumb controller
After=network-online.target
Wants=network-online.target

[Service]
User=plumb
Group=plumb
EnvironmentFile=/etc/plumb/plumb.env
ExecStart=/usr/local/bin/plumb serve --data /var/lib/plumb --hosts ${PLUMB_HOSTS}
Restart=on-failure
RestartSec=5
# A stop waits for open requests (5 s) and for the clean-up of Runs (the
# engine cuts it off after 2 min, plan.DefaultShutdownGrace); a SIGKILL
# before that would leave more for Recover.
TimeoutStopSec=180
# plumb serve exits 78 for a start failure that a restart cannot fix (D-183).
RestartPreventExitStatus=78
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/var/lib/plumb

[Install]
WantedBy=multi-user.target'

setup_lxc() {
	local host=$1 ip=$2 tmp
	run pct exec "$LXC_ID" -- sh -c 'id plumb >/dev/null 2>&1 || useradd --system --home-dir /var/lib/plumb --shell /usr/sbin/nologin plumb' </dev/null
	run pct exec "$LXC_ID" -- install -d -o plumb -g plumb -m 0700 /etc/plumb /var/lib/plumb </dev/null
	# The key directory is the mount point of the key volume (D-102).
	if [[ $DRY_RUN != 1 ]]; then
		query pct exec "$LXC_ID" -- mountpoint -q "$KEY_DIR" </dev/null ||
			die "$KEY_DIR in LXC $LXC_ID is not a mount point; the key volume is missing"
	fi
	run pct exec "$LXC_ID" -- chown plumb:plumb "$KEY_DIR" </dev/null
	run pct exec "$LXC_ID" -- chmod 0700 "$KEY_DIR" </dev/null
	# The binary goes to a temp name and is renamed over the old one: a
	# rename works while plumb runs; a push onto the running file fails
	# with "Text file busy", and pct push still exits 0 (test host, T-056).
	push_checked "$PLUMB_BINARY" /usr/local/bin/plumb 0755
	push_checked "$PVE_CA" /etc/plumb/pve-root-ca.pem 0644
	if [[ -n $TOKEN_SECRET ]]; then
		push_env "$host" "$ip"
	else
		add_env_dns
		# shellcheck disable=SC2046 # one word per line: names, numbers, times, a UUID
		set_env_lines $(env_backup_lines) $(env_slice7_lines)
	fi
	new_tmp tmp
	printf '%s\n' "$UNIT" >"$tmp"
	if ! (push_checked "$tmp" /etc/systemd/system/plumb.service 0644); then
		rm -f "$tmp"
		die "cannot write the unit into the LXC"
	fi
	rm -f "$tmp"
	run pct exec "$LXC_ID" -- systemctl daemon-reload </dev/null
	run pct exec "$LXC_ID" -- systemctl enable plumb.service </dev/null
	if [[ -n $RESTORE_FILE ]]; then
		restore_in_lxc
	else
		key_init
	fi
	# D-229 order: the new binary is in place (above), then the content,
	# then the restart.
	set_image_content
	run pct exec "$LXC_ID" -- systemctl restart plumb.service </dev/null # a repair run brings a new binary
	check_running_binary
	CONTENT_ROLLBACK=0
}

# push_checked SRC DEST MODE copies SRC to DEST in the LXC (owner root):
# first to a temp name in the same directory, then a sha256 check inside
# the LXC, then a rename. pct push can print an error and still exit 0
# (test host, T-056), so only the checksum shows that the copy is there.
# On any failure DEST stays as it was and the run stops.
push_checked() {
	local src=$1 dest=$2 mode=$3 tmp want have
	tmp="${dest%/*}/.${dest##*/}.plumb-new"
	if ! run pct push "$LXC_ID" "$src" "$tmp" --perms "$mode" --user 0 --group 0; then
		pct exec "$LXC_ID" -- rm -f "$tmp" </dev/null >/dev/null 2>&1 || true
		die "cannot copy $dest into LXC $LXC_ID; $dest and the running service were not changed"
	fi
	if [[ $DRY_RUN != 1 ]]; then
		want=$(sha256sum <"$src") || die "cannot read $src"
		want=${want%% *}
		have=$(query pct exec "$LXC_ID" -- sha256sum "$tmp" </dev/null 2>/dev/null) || have=""
		have=${have%% *}
		if [[ $have != "$want" ]]; then
			pct exec "$LXC_ID" -- rm -f "$tmp" </dev/null >/dev/null 2>&1 || true
			die "the copy of $dest in LXC $LXC_ID is missing or wrong (pct push failed); $dest and the running service were not changed"
		fi
	fi
	if ! run pct exec "$LXC_ID" -- mv -f "$tmp" "$dest" </dev/null; then
		# The mv may have run although pct exec failed (T-122 F7): check
		# the result before saying anything about it.
		have=$(query pct exec "$LXC_ID" -- sha256sum "$dest" </dev/null 2>/dev/null) || have=""
		pct exec "$LXC_ID" -- rm -f "$tmp" </dev/null >/dev/null 2>&1 || true
		if [[ ${have%% *} == "$want" ]]; then
			say "NOTE: pct exec reported a failure, but $dest is in place (checksum checked); the run goes on."
			return 0
		fi
		die "cannot put $dest in place in LXC $LXC_ID; it was not changed"
	fi
}

# service_prop NAME prints one property of plumb.service in the LXC.
service_prop() {
	query pct exec "$LXC_ID" -- systemctl show -p "$1" --value plumb.service </dev/null 2>/dev/null || true
}

# check_service_stays PID fails when the new binary does not stay up: after
# a few seconds the unit must be active with the same MainPID and the same
# NRestarts (a crash loop restarts it every 5 s). The failure shows the last
# journal lines; a "Password: …" value (first start, D-086) and any
# PLUMB_PVE_SECRET value are masked.
check_service_stays() {
	local pid=$1 restarts active now
	restarts=$(service_prop NRestarts)
	sleep "${PLUMB_INSTALL_STAY_WAIT:-3}"
	active=$(query pct exec "$LXC_ID" -- systemctl is-active plumb.service </dev/null 2>/dev/null) || true
	now=$(service_prop MainPID)
	if [[ $active == active && $now == "$pid" && $(service_prop NRestarts) == "$restarts" ]]; then
		return 0
	fi
	echo "install.sh: plumb.service does not stay up with the new binary (state ${active:-unknown}); last lines of its journal:" >&2
	{ query pct exec "$LXC_ID" -- journalctl -u plumb.service -n 20 -o cat --no-pager </dev/null 2>/dev/null || true; } |
		sed -E 's/(Password:).*/\1 <hidden>/; s/(PLUMB_PVE_SECRET=).*/\1<hidden>/' | sed 's/^/  /' >&2
	die "plumb.service in LXC $LXC_ID does not stay up with the new binary (see: pct exec $LXC_ID -- journalctl -u plumb.service)"
}

# check_running_binary fails when plumb.service does not run the binary of
# --plumb-binary after the restart (sha256 of /proc/<MainPID>/exe).
check_running_binary() {
	[[ $DRY_RUN == 1 ]] && return 0
	local want have pid i
	want=$(sha256sum <"$PLUMB_BINARY") || die "cannot read $PLUMB_BINARY"
	want=${want%% *}
	for ((i = 0; i < ${PLUMB_INSTALL_RUNNING_TRIES:-30}; i++)); do
		pid=$(query pct exec "$LXC_ID" -- systemctl show -p MainPID --value plumb.service </dev/null 2>/dev/null) || pid=""
		if [[ $pid =~ ^[1-9][0-9]*$ ]]; then
			have=$(query pct exec "$LXC_ID" -- sha256sum "/proc/$pid/exe" </dev/null 2>/dev/null) || have=""
			if [[ ${have%% *} == "$want" ]]; then
				check_service_stays "$pid"
				return 0
			fi
		fi
		sleep "${PLUMB_INSTALL_RUNNING_WAIT:-2}"
	done
	die "plumb.service in LXC $LXC_ID does not run the new binary after the restart (see: pct exec $LXC_ID -- systemctl status plumb.service)"
}

# lxc_has_key reports whether the LXC holds the master key file.
lxc_has_key() {
	[[ ! ($DRY_RUN == 1 && $1 == "$DRY_GONE_LXC") ]] || return 1
	query pct exec "$1" -- test -s "$KEY_FILE" </dev/null >/dev/null 2>&1
}

# lxc_key_volume ID prints the config line of the key volume ("mpN: …").
lxc_key_volume() {
	{ query pct config "$1" 2>/dev/null || true; } | grep -E "^mp[0-9]+: [^,]+,(.*,)?mp=$KEY_DIR(,|\$)" | head -n1 || true
}

# check_key_preflight runs before any change. It refuses an existing Plumb
# LXC without the key volume, and it needs a terminal when plumb key init
# will run (D-178).
NEED_KEY_INIT=1
check_key_preflight() {
	[[ -z $RESTORE_FILE ]] || return 0 # check_restore: a new LXC, plumb key restore
	local id=${LXC_ID:-$(state_get lxc_id)} vol
	if [[ -n $id && $(lxc_state "$id") == ours ]]; then
		vol=$(lxc_key_volume "$id")
		[[ -n $vol ]] || die "the Plumb LXC $id has no key volume at $KEY_DIR (made by an older install.sh); run install.sh --remove and install again. Nothing was changed."
		[[ $vol =~ (^|,)backup=0(,|$) ]] || die "the key volume of LXC $id is in the backups (no backup=0): $vol. Nothing was changed."
		if lxc_has_key "$id"; then
			NEED_KEY_INIT=0
		fi
	fi
	if [[ $NEED_KEY_INIT == 1 ]]; then
		say "It runs \"plumb key init\" in the LXC on this terminal: it shows the RECOVERY KEY once. Write it down;"
		say "install.sh does not store it, and without it a restored backup cannot read the stored secrets."
		if [[ $DRY_RUN != 1 && ${PLUMB_INSTALL_ASSUME_TTY:-0} != 1 ]] && ! [[ -t 0 && -t 1 ]]; then
			die "plumb key init shows the recovery key only on a terminal; run install.sh on a terminal (ssh -t), not through a pipe, file or script. Nothing was changed."
		fi
	fi
}

# key_init runs plumb key init as user plumb on this terminal, before the
# first start of plumb.service (D-086, D-178). lxc-attach passes the
# terminal; pct exec does not. The output is never captured: the recovery
# key goes only to the terminal.
key_init() {
	if lxc_has_key "$LXC_ID"; then
		say "The master key exists in LXC $LXC_ID; it is kept."
		return 0
	fi
	say ""
	say "plumb key init: the RECOVERY KEY follows. Write it down and keep it offline."
	run lxc-attach -n "$LXC_ID" --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key init --data /var/lib/plumb ||
		die "plumb key init failed; plumb.service was not started. Run install.sh again on a terminal."
	if [[ $DRY_RUN != 1 ]]; then
		lxc_has_key "$LXC_ID" || die "plumb key init wrote no $KEY_FILE; plumb.service was not started"
		if [[ $YES != 1 && -t 0 ]]; then
			read -r -p "Write the recovery key down, then press Enter" _ || true
		fi
	fi
	say ""
}

# fetch_plumb_ca copies the CA that plumb made at its first start.
fetch_plumb_ca() {
	local out=$1
	if [[ $DRY_RUN == 1 ]]; then
		run pct pull "$LXC_ID" /var/lib/plumb/link/ca.crt "$out"
		cat >"$out" <<'EOF'
-----BEGIN CERTIFICATE-----
DRYRUNPLACEHOLDERNOTAREALCERTIFICATE
-----END CERTIFICATE-----
EOF
		return 0
	fi
	for _ in $(seq 1 60); do
		if query pct exec "$LXC_ID" -- test -s /var/lib/plumb/link/ca.crt </dev/null; then
			pct pull "$LXC_ID" /var/lib/plumb/link/ca.crt "$out"
			return 0
		fi
		sleep 2
	done
	die "plumb did not start in the LXC (see: pct exec $LXC_ID -- journalctl -u plumb)"
}

# --- install id and backup settings (D-302 … D-304, T-114) ---

# ensure_install_id sets INSTALL_ID: the restored one, else the one of the
# state file (also of a kept state), else a new random UUID. The backup
# header names it; --restore compares it (D-290 N7).
INSTALL_ID=""
ensure_install_id() {
	[[ -n $INSTALL_ID ]] && return 0
	INSTALL_ID=$(state_get install_id)
	if [[ -z $INSTALL_ID ]]; then
		INSTALL_ID=$(cat /proc/sys/kernel/random/uuid)
	fi
	[[ $INSTALL_ID =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] ||
		die "$STATE_FILE has an install_id that is not a UUID; check it by hand. Nothing was changed."
}

# set_env_lines KEY=VALUE … puts each line into plumb.env of a repair run:
# the old line of the key goes, the new one is added. It writes nothing
# when every line is there. The values are
# checked names, numbers, times and UUIDs. serve reads only the keys it
# knows, so an older binary ignores them.
set_env_lines() {
	local kv script="test -f /etc/plumb/plumb.env || exit 0; cp -p /etc/plumb/plumb.env /etc/plumb/.plumb.env.new" same=1
	if [[ $DRY_RUN != 1 ]]; then
		for kv in "$@"; do
			query pct exec "$LXC_ID" -- grep -qx "$kv" /etc/plumb/plumb.env </dev/null || same=0
		done
		[[ $same == 0 ]] || return 0
	fi
	for kv in "$@"; do
		script+="; sed -i '/^${kv%%=*}=/d' /etc/plumb/.plumb.env.new; echo '$kv' >>/etc/plumb/.plumb.env.new"
	done
	script+="; mv -f /etc/plumb/.plumb.env.new /etc/plumb/plumb.env"
	run pct exec "$LXC_ID" -- sh -c "$script" </dev/null
}

# env_backup_lines are the plumb.env lines of the backup in serve (D-302).
env_backup_lines() {
	printf '%s\n' "PLUMB_INSTALL_ID=$INSTALL_ID" "PLUMB_BACKUP_TIME=$BACKUP_TIME" "PLUMB_BACKUP_KEEP=$BACKUP_KEEP"
}

# env_slice7_lines are the plumb.env lines of the extra networks and the
# guest backups (D-401, D-405, D-406). Each run writes all three, so a
# repair run without the flags clears them.
env_slice7_lines() {
	local IFS=,
	printf '%s\n' "PLUMB_PVE_EXTRA_NETS=${EXTRA_NETS[*]}" "PLUMB_PVE_GUEST_BACKUP_STORAGE=$GUEST_BACKUP_STORAGE" \
		"PLUMB_PVE_GUEST_BACKUP_CLEAR=$GUEST_BACKUP_CLEAR"
}

# --- guest backups (D-405, D-406, review M2 and L2 of #321) ---

# check_guest_backup checks --guest-backup-storage before any change and
# says how the backups are stored. It only reads.
check_guest_backup() {
	[[ -n $GUEST_BACKUP_STORAGE ]] || return 0
	local s=$GUEST_BACKUP_STORAGE c type key
	case $s in
	"$DISK_STORAGE" | "$SNIPPET_STORAGE" | "$IMAGE_STORAGE")
		die "--guest-backup-storage $s is a storage of Plumb itself; name a storage for guest backups only. Nothing was changed."
		;;
	esac
	# Not the storage of the controller's own backup copies (D-289): the
	# token could fill it, and the copies would stop (review L1 of #330).
	local copy=${BACKUP_STORAGE:-$(state_get backup_storage)}
	[[ $copy == none || $s != "$copy" ]] ||
		die "--guest-backup-storage $s is the storage of Plumb's own backup copies (--backup-storage); use another one. Nothing was changed."
	c=$(storage_content "$s") || die "storage $s (--guest-backup-storage) does not exist. Nothing was changed."
	[[ ,$c, == *,backup,* ]] ||
		die "storage $s has the content \"$c\" without \"backup\"; vzdump cannot write there. Add it (pvesm set $s --content $c,backup) or name another storage. Nothing was changed."
	# AllocateSpace with VM.Config.Disk would let the token put guest disks
	# there, outside the disk gate (review M1 of #330).
	if [[ ,$c, == *,images,* || ,$c, == *,rootdir,* ]]; then
		local rest
		rest=$(awk -v RS=, 'NF && $1 != "images" && $1 != "rootdir" { print $1 }' <<<"$c" | paste -sd, -)
		die "storage $s has the content \"$c\": the guest backup storage must hold only backups, no guest disks (images, rootdir). Fix: pvesm set $s --content $rest, or name another storage. Nothing was changed."
	fi
	# AllocateSpace on a storage with snippets would let cicustom point at
	# any snippet there (D-081), like on the disk storage.
	[[ ,$c, != *,snippets,* ]] ||
		die "storage $s has the content \"snippets\": the token could point cicustom at any snippet there (D-081). Use a storage without snippets. Nothing was changed."
	type=$(storage_field "$s" type) || type=""
	key=$(storage_field "$s" encryption-key) || key=""
	if [[ $type == pbs && -n $key ]]; then
		say "Guest backups go to $s: a Proxmox Backup Server with client-side encryption."
	elif [[ $GUEST_BACKUP_CLEAR == 1 ]]; then
		say "WARNING: storage $s (type ${type:-unknown}) has no client-side encryption, and --guest-backup-clear is set:"
		say "guest backups are stored IN CLEAR there, with every secret of the guest (D-406)."
	else
		say "NOTE: storage $s (type ${type:-unknown}) has no client-side encryption. Plumb refuses every guest backup"
		say "there until a repair run with --guest-backup-clear says that backups in clear are wanted (D-406)."
	fi
	warn_foreign_backups
}

# check_second_backup_storage refuses an install where another storage with
# a Plumb Datastore.AllocateSpace line has the content "backup": VM.Backup
# would let the token write a vzdump there too, so "only the named storage"
# would be no limit (D-405, M2 of #321). The controller shows the same at
# run time (settings view, other_backup_storages).
check_second_backup_storage() {
	local path role propagate id c rest bad=()
	while read -r path role propagate; do
		[[ $path == /storage/* && " ${ROLE_PRIVS[$role]} " == *" Datastore.AllocateSpace "* ]] || continue
		id=${path#/storage/}
		[[ $id != "$GUEST_BACKUP_STORAGE" ]] || continue
		c=$(storage_content "$id") || continue
		[[ ,$c, == *,backup,* ]] || continue
		rest=$(awk -v RS=, 'NF && $1 != "backup" { print $1 }' <<<"$c" | paste -sd, -)
		bad+=("$id: pvesm set $id --content ${rest:-<other content>}")
	done <<<"$(acl_plan)"
	((${#bad[@]} == 0)) && return 0
	say "These storages of Plumb have the content \"backup\". With VM.Backup the token could write guest backups there"
	say "too, not only to $GUEST_BACKUP_STORAGE (D-405). Remove \"backup\" from their content:"
	printf '  %s\n' "${bad[@]}"
	die "another storage of Plumb has the content \"backup\". Nothing was changed."
}

# warn_foreign_backups warns about backups on the guest backup storage of
# VMIDs in Plumb's range that do not carry this install's marker (L2 of
# #321): Plumb shows them as "older guest" or "unmarked" and never deletes
# or restores them, but a person could take them for Plumb's.
warn_foreign_backups() {
	local rows found
	# shellcheck disable=SC2016 # perl code
	if ! rows=$(query pvesh get "/nodes/$NODE/storage/$GUEST_BACKUP_STORAGE/content" --content backup --output-format json 2>/dev/null) ||
		! found=$(json_rows 'for my $e (@$d) { my $v = $e->{vmid}; next unless defined $v && $v =~ /^\d+$/;
			next if $v < $ARGV[0] || $v > $ARGV[1]; next if index($e->{notes} // "", "plumb:$ARGV[2]:") == 0;
			print "  $e->{volid}\n" }' "$VMID_MIN" "$VMID_MAX" "$INSTALL_ID" <<<"$rows"); then
		say "NOTE: cannot list the backups on $GUEST_BACKUP_STORAGE. Check by hand for backups of the VMIDs $VMID_MIN-$VMID_MAX."
		return 0
	fi
	[[ -n $found ]] || return 0
	say "WARNING: storage $GUEST_BACKUP_STORAGE holds backups of VMIDs in Plumb's range ($VMID_MIN-$VMID_MAX) that this install"
	say "did not make. Plumb never deletes or restores them; a new guest on such a VMID shows them as \"older guest\"."
	say "Remove them in Proxmox if they are not needed:"
	printf '%s\n' "$found"
}

# --- off-host copy of the backups (D-289, D-304, D-312) ---

# The copy script and its units on the host. COPY_SCRIPT is written by
# copy_script; plumb-backup-copy reads COPY_CONF.
SBIN_DIR=${PLUMB_INSTALL_SBIN:-/usr/local/sbin}
UNIT_DIR=${PLUMB_INSTALL_UNIT_DIR:-/etc/systemd/system}
COPY_CONF=${PLUMB_INSTALL_COPY_CONF:-/etc/plumb/backup-copy.conf}
PVE_MNT=${PLUMB_INSTALL_PVE_MNT:-/mnt/pve}
readonly COPY_NAME=plumb-backup-copy COPY_STATUS_DIR=/var/lib/plumb-copy

# storage_field ID FIELD prints one field of the storage config.
storage_field() {
	local json
	json=$(query pvesh get "/storage/$1" --output-format json 2>/dev/null) || return 1
	[[ -n $json ]] || return 1
	# shellcheck disable=SC2016 # perl code
	json_rows 'print $d->{$ARGV[0]} // ""' "$2" <<<"$json"
}

# check_backup_storage checks --backup-storage before any change: type
# dir, nfs, cifs or cephfs, active, and a share mounted (D-289 N3). It
# sets COPY_TYPE and COPY_PATH.
COPY_TYPE="" COPY_PATH=""
check_backup_storage() {
	[[ -z $BACKUP_STORAGE || $BACKUP_STORAGE == none || $BACKUP_STORAGE =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "bad --backup-storage"
	[[ -n $BACKUP_STORAGE && $BACKUP_STORAGE != none ]] || return 0
	case $BACKUP_STORAGE in
	"$DISK_STORAGE" | "$SNIPPET_STORAGE" | "$IMAGE_STORAGE")
		die "--backup-storage $BACKUP_STORAGE is a storage of Plumb itself; use another one. Nothing was changed."
		;;
	esac
	COPY_TYPE=$(storage_field "$BACKUP_STORAGE" type) || COPY_TYPE=""
	[[ -n $COPY_TYPE ]] || die "storage $BACKUP_STORAGE (--backup-storage) does not exist. Nothing was changed."
	case $COPY_TYPE in
	dir) COPY_PATH=$(storage_field "$BACKUP_STORAGE" path) ;;
	nfs | cifs | cephfs) COPY_PATH=$PVE_MNT/$BACKUP_STORAGE ;;
	*) die "storage $BACKUP_STORAGE has the type $COPY_TYPE; --backup-storage needs a storage with a path: dir, nfs, cifs or cephfs (a Proxmox Backup Server is not supported, D-289). Nothing was changed." ;;
	esac
	[[ $COPY_PATH == /* && $COPY_PATH != *..* ]] || die "storage $BACKUP_STORAGE has no usable path. Nothing was changed."
	query pvesm status --storage "$BACKUP_STORAGE" 2>/dev/null | awk -v s="$BACKUP_STORAGE" '$1 == s && $3 == "active" { ok = 1 } END { exit !ok }' ||
		die "storage $BACKUP_STORAGE is not active. Nothing was changed."
	if [[ $COPY_TYPE != dir ]] && ! query mountpoint -q "$COPY_PATH"; then
		die "storage $BACKUP_STORAGE is not mounted at $COPY_PATH; a copy would fill the root disk of this host. Nothing was changed."
	fi
}

# copy_conf LXC STORAGE TYPE PATH prints the config of plumb-backup-copy.
copy_conf() {
	printf 'lxc_id=%s\nstorage=%s\nstorage_type=%s\nstorage_path=%s\nkeep=%s\nmax_bytes=%s\npve_mnt=%s\n' \
		"$1" "$2" "$3" "$4" "$BACKUP_COPY_KEEP" "$BACKUP_MAX_BYTES" "$PVE_MNT"
}

# copy_units prints the service or the timer of the copy.
copy_unit() {
	if [[ $1 == service ]]; then
		printf '%s\n' "# Made by Plumb's install.sh (D-289). Do not edit." "[Unit]" "Description=Copy the newest Plumb backup off the Plumb LXC" \
			"After=pve-cluster.service" "" "[Service]" "Type=oneshot" "ExecStart=$SBIN_DIR/$COPY_NAME --conf $COPY_CONF"
	else
		printf '%s\n' "# Made by Plumb's install.sh (D-289). Do not edit." "[Unit]" "Description=Daily copy of the newest Plumb backup" "" \
			"[Timer]" "OnCalendar=*-*-* $BACKUP_COPY_TIME:00 UTC" "Persistent=true" "" "[Install]" "WantedBy=timers.target"
	fi
}

# put_host_file SRC DEST MODE [DIR_MODE] installs a root-owned file on the host.
put_host_file() {
	run install -d -o root -g root -m "${4:-0755}" "$(dirname "$2")"
	run install -o root -g root -m "$3" "$1" "$2"
}

# install_copy_timer installs the copy script, its config and its timer,
# and the root-owned status directory in the LXC (D-289 N2). A repair run
# writes them again. The token gets no right for it: the timer runs as
# root on the host.
install_copy_timer() {
	local storage=$1 type=$2 path=$3 tmp
	run pct exec "$LXC_ID" -- install -d -o root -g root -m 0755 "$COPY_STATUS_DIR" </dev/null
	new_tmp tmp
	copy_script >"$tmp"
	put_host_file "$tmp" "$SBIN_DIR/$COPY_NAME" 0755
	copy_conf "$LXC_ID" "$storage" "$type" "$path" >"$tmp"
	put_host_file "$tmp" "$COPY_CONF" 0600 0700
	copy_unit service >"$tmp"
	put_host_file "$tmp" "$UNIT_DIR/$COPY_NAME.service" 0644
	copy_unit timer >"$tmp"
	put_host_file "$tmp" "$UNIT_DIR/$COPY_NAME.timer" 0644
	rm -f "$tmp"
	run systemctl daemon-reload
	run systemctl enable --now "$COPY_NAME.timer"
	say "The copy timer copies the newest backup daily at $BACKUP_COPY_TIME UTC to storage $storage ($path/plumb-backups, keep $BACKUP_COPY_KEEP)."
}

# remove_copy_timer removes the timer, the script and the config. The
# copies stay on the storage.
remove_copy_timer() {
	if [[ -e $UNIT_DIR/$COPY_NAME.timer || -e $SBIN_DIR/$COPY_NAME || -e $COPY_CONF ]]; then
		run systemctl disable --now "$COPY_NAME.timer" || true
		run rm -f -- "$UNIT_DIR/$COPY_NAME.timer" "$UNIT_DIR/$COPY_NAME.service" "$SBIN_DIR/$COPY_NAME" "$COPY_CONF"
		run systemctl daemon-reload
	fi
}

# check_copy_setting runs before any change. A storage of the state file
# (a repair run without --backup-storage) that fails its checks only
# warns: the timer stays as it is, and its own run reports the failure.
check_copy_setting() {
	if [[ -z $BACKUP_STORAGE && -n $(state_get backup_storage) ]]; then
		BACKUP_STORAGE=$(state_get backup_storage)
		if [[ -n $RESTORE_FILE ]]; then
			say "Using kept backup storage $BACKUP_STORAGE for the restored controller."
			if ! (check_backup_storage); then
				die "Backup storage $BACKUP_STORAGE from the kept state failed its checks; give --backup-storage ID or give --backup-storage none. Nothing was changed."
			fi
			return 0
		fi
		if ! (check_backup_storage) 2>/dev/null; then
			say "WARNING: the backup copy storage $BACKUP_STORAGE fails its checks now; the copy timer is kept as it is."
			BACKUP_STORAGE=""
			return 0
		fi
	fi
	check_backup_storage
}

# setup_copy_timer is the --backup-storage part of an install or repair run.
setup_copy_timer() {
	if [[ $BACKUP_STORAGE == none ]]; then
		remove_copy_timer
		BACKUP_STORAGE_SET=""
		return 0
	fi
	[[ -n $BACKUP_STORAGE ]] || return 0
	install_copy_timer "$BACKUP_STORAGE" "$COPY_TYPE" "$COPY_PATH"
	BACKUP_STORAGE_SET=$BACKUP_STORAGE
}

# copy_once DIR|"" runs one copy now with a config of its own (for
# --remove --keep-guests) and sets COPY_RESULT to the path of the copy.
COPY_RESULT=""
copy_once() {
	local to=$1 script conf
	new_tmp script
	new_tmp conf
	copy_script >"$script"
	if [[ -n $to ]]; then
		copy_conf "$LXC_ID" "" dir "$to" >"$conf"
	else
		copy_conf "$LXC_ID" "$BACKUP_STORAGE" "$COPY_TYPE" "$COPY_PATH" >"$conf"
	fi
	if [[ $DRY_RUN == 1 ]]; then
		run bash "$SBIN_DIR/$COPY_NAME" --conf "$COPY_CONF" ${to:+--to "$to"}
		COPY_RESULT=${to:-$COPY_PATH}/plumb-backups/plumb-DRYRUN.pbk
		return 0
	fi
	COPY_RESULT=$(bash "$script" --conf "$conf" ${to:+--to "$to"}) || die "the backup could not be copied off the LXC (see above); nothing was removed"
	[[ -f $COPY_RESULT ]] || die "the copy did not report a file; nothing was removed"
}

# check_backup_to checks --backup-to DIR: absolute, an existing directory
# owned by root, not a link, and outside /var/lib/vz and the storages of
# Plumb (D-304). It is not an off-host copy.
check_backup_to() {
	local d=$BACKUP_TO owner
	[[ $d == /* && $d != *..* && $d != */ ]] || die "--backup-to needs an absolute directory without .. and without a trailing /"
	[[ -d $d && ! -L $d ]] || die "--backup-to $d is not an existing directory"
	owner=$(query stat -c '%U %a' -- "$d")
	[[ ${owner%% *} == root ]] || die "--backup-to $d is not owned by root"
	local p
	for p in /var/lib/vz "$SNIPPET_DIR" "$IMAGE_DIR" "$(state_get snippet_dir)" "$(state_get image_dir)"; do
		[[ -n $p ]] || continue
		[[ $d == "$p" || $d == "$p"/* ]] && die "--backup-to $d is inside $p; use a directory outside the storages"
	done
	return 0
}

# need_tty DOES stops before any change when the run has no terminal:
# the recovery key is typed there (D-178 rules).
need_tty() {
	if [[ $DRY_RUN != 1 && ${PLUMB_INSTALL_ASSUME_TTY:-0} != 1 ]] && ! [[ -t 0 && -t 1 ]]; then
		die "$1 asks for the recovery key on a terminal; run install.sh on a terminal (ssh -t). Nothing was changed."
	fi
}

# verify_on_host FILE runs plumb backup verify with the given binary on
# this terminal: it asks the recovery key and checks the fingerprint
# before any decrypt (exit 3), then every chunk (D-302).
verify_on_host() {
	local rc=0
	say "plumb backup verify: type the RECOVERY KEY of this Plumb when asked."
	run "$PLUMB_BINARY" backup verify "$1" || rc=$?
	case $rc in
	0) ;;
	3) die "the recovery key does not match the backup $1 (wrong recovery key); $2" ;;
	*) die "plumb backup verify failed for $1 (exit $rc); $2" ;;
	esac
}

# --- the backup header (D-302) ---

# read_header FILE sets H_INSTALL_ID, H_NODE, H_POOL and H_AGENT_URL from
# the clear header. It checks only its form: the content is proved by
# plumb backup verify.
H_INSTALL_ID="" H_NODE="" H_POOL="" H_AGENT_URL=""
read_header() {
	local out
	out=$(perl -MJSON::PP -e '
		open(my $f, "<:raw", $ARGV[0]) or exit 2;
		read($f, my $m, 12) == 12 or exit 3;
		substr($m, 0, 8) eq "PLUMBBK\0" or exit 3;
		my $n = unpack("N", substr($m, 8, 4));
		$n > 0 && $n <= 4096 or exit 3;
		read($f, my $h, $n) == $n or exit 3;
		my $d = eval { JSON::PP->new->decode($h) } or exit 3;
		ref $d eq "HASH" && ($d->{v} // 0) == 1 or exit 4;
		print join("\n", map { my $v = $d->{$_} // ""; ref $v ? "" : $v } qw(install_id node pool agent_url)), "\n";' "$1") ||
		die "$1 is not a Plumb backup file of format 1. Nothing was changed."
	{
		read -r H_INSTALL_ID
		read -r H_NODE
		read -r H_POOL
		read -r H_AGENT_URL
	} <<<"$out"
	[[ $H_INSTALL_ID =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || die "the backup header has no valid install_id. Nothing was changed."
	[[ $H_NODE =~ ^[A-Za-z0-9][A-Za-z0-9.-]{0,63}$ && $H_POOL =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "the backup header has no valid node or pool. Nothing was changed."
	[[ $H_AGENT_URL =~ ^https://([0-9]{1,3}(\.[0-9]{1,3}){3}):8444$ ]] || die "the backup header has no agent URL of the form https://IPv4:8444 ($H_AGENT_URL). Nothing was changed."
}

# pool_has_guests reports whether the pool exists and holds a guest or a
# template. It fails closed: a pool that cannot be read counts as full.
pool_has_guests() {
	query pveum pool list --output-format json | json_has poolid "$POOL" || return 1
	local members
	members=$(query pvesh get /pools --poolid "$POOL" --output-format json 2>/dev/null) || return 0
	json_has poolid "$POOL" <<<"$members" || return 0
	[[ $members =~ \"vmid\" ]]
}

# check_restore runs before any change of --restore FILE (D-290, D-304).
check_restore() {
	[[ -f $RESTORE_FILE ]] || die "--restore $RESTORE_FILE: no such file"
	[[ $LXC_IP != dhcp && -n $LXC_GW ]] ||
		die "--restore needs --lxc-ip CIDR and --lxc-gw IP: the agents pin the address of the old controller (D-179). Nothing was changed."
	read_header "$RESTORE_FILE"
	local ip=${LXC_IP%/*} want=${H_AGENT_URL#https://}
	want=${want%:8444}
	[[ $ip == "$want" ]] || die "--lxc-ip $ip differs from the address in the backup ($want): the agents reach only that one. Nothing was changed."
	[[ $NODE == "$H_NODE" ]] || die "the backup was made on node $H_NODE, this is node $NODE. Nothing was changed."
	[[ $POOL == "$H_POOL" ]] || die "the backup is of pool $H_POOL, this run has --pool $POOL. Nothing was changed."
	local id
	id=$(state_get lxc_id)
	if [[ -n $id && $(lxc_state "$id") != none ]]; then
		# A restore that failed in its LXC (review M2 of #221): that LXC
		# has no database; it goes after the confirm and the host verify.
		[[ $(state_get restoring) == 1 && $(lxc_state "$id") == ours ]] ||
			die "a Plumb LXC ($id) exists; replace it with --remove --keep-guests first. Nothing was changed."
		if query pct exec "$id" -- test -s /var/lib/plumb/plumb.db </dev/null >/dev/null 2>&1; then
			die "LXC $id of the last --restore has a restored database; run install.sh --plumb-binary FILE (a repair run) to start it. Nothing was changed."
		fi
		HALF_LXC=$id
		say "LXC $id is left from a --restore that did not finish; it has no database. It is destroyed with its key volume first."
	fi
	if [[ $(state_get kept_guests) == 1 ]]; then
		[[ $(state_get install_id) == "$H_INSTALL_ID" ]] ||
			die "the backup is of install $H_INSTALL_ID, the kept guests belong to install $(state_get install_id). Nothing was changed."
		[[ $(state_get pool) == "$H_POOL" && $(state_get node) == "$H_NODE" ]] ||
			die "the kept state names pool $(state_get pool) on node $(state_get node), the backup pool $H_POOL on node $H_NODE. Nothing was changed."
	elif pool_has_guests; then
		die "pool $POOL holds guests, and no kept state of --remove --keep-guests names them; nothing is restored into it. Nothing was changed."
	fi
	need_tty "--restore"
	INSTALL_ID=$H_INSTALL_ID
}

# restore_in_lxc replaces plumb key init on a --restore: the backup file
# goes into the LXC, then plumb key restore and plumb restore run as user
# plumb on this terminal, before the first start of plumb.service. The
# recovery key and the new admin password stay on the terminal (D-178,
# D-303). The file goes outside the data directory: plumb restore refuses
# a data directory that holds anything but the key volume (D-318, T-130).
# The directory is root:plumb 0750: user plumb reads the file but cannot
# put a link where root pushes it.
readonly RESTORE_DIR=/var/lib/plumb-restore
readonly RESTORE_IN_LXC=$RESTORE_DIR/restore.pbk
RESTORING=0
HALF_LXC=""

# remove_half_lxc destroys the LXC of a --restore that did not finish:
# the LXC and its key volume (it holds the key that was typed in again).
remove_half_lxc() {
	local id=$HALF_LXC keyvol
	[[ -n $id ]] || return 0
	keyvol=$(lxc_key_volume "$id")
	keyvol=${keyvol#*: }
	keyvol=${keyvol%%,*}
	confirm "Destroy LXC $id of the last --restore${keyvol:+ and its key volume $keyvol}?" yes || die "stopped; nothing was changed"
	run pct stop "$id" || true
	run pct destroy "$id" --purge 1
	if [[ -n $keyvol ]] && { query pvesm list "${keyvol%%:*}" --vmid "$id" 2>/dev/null || true; } | grep -q "^$keyvol "; then
		run pvesm free "$keyvol"
	fi
	if [[ $DRY_RUN != 1 ]]; then
		clean_stale_lxc_dir "$id" keep
	else
		# The rest of the dry run sees it gone, as the real run does
		# (T-132, T-122 F6).
		DRY_GONE_LXC=$id
	fi
}
# drop_restore_file removes the backup file and its directory from the
# LXC, after the restore and after a failed one.
drop_restore_file() {
	run pct exec "$LXC_ID" -- rm -rf -- "$RESTORE_DIR" </dev/null || true
}
restore_in_lxc() {
	lxc_has_key "$LXC_ID" && die "LXC $LXC_ID has a master key already; a restore needs a new LXC"
	run pct exec "$LXC_ID" -- install -d -o root -g plumb -m 0750 "$RESTORE_DIR" </dev/null
	push_checked "$RESTORE_FILE" "$RESTORE_IN_LXC" 0600
	run pct exec "$LXC_ID" -- chown plumb:plumb "$RESTORE_IN_LXC" </dev/null
	say ""
	say "plumb key restore: type the RECOVERY KEY of the backup."
	if ! run lxc-attach -n "$LXC_ID" --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key restore --data /var/lib/plumb; then
		drop_restore_file
		die "plumb key restore failed; plumb.service was not started. Run the same install.sh --restore again: it removes this LXC first."
	fi
	say "plumb restore: it shows the NEW password of the user admin once."
	if ! run lxc-attach -n "$LXC_ID" --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb restore --data /var/lib/plumb --from "$RESTORE_IN_LXC"; then
		drop_restore_file
		die "plumb restore failed; plumb.service was not started. Run the same install.sh --restore again: it removes this LXC first."
	fi
	drop_restore_file
	if [[ $DRY_RUN != 1 && $YES != 1 && -t 0 ]]; then
		read -r -p "Write the admin password down, then press Enter" _ || true
	fi
}

# copy_script prints plumb-backup-copy (D-312).
copy_script() {
	cat <<'COPY_EOF'
#!/usr/bin/env bash
# plumb-backup-copy – made by Plumb's install.sh (D-289, D-304, D-312). Do not edit.
# Copies the newest encrypted Plumb backup out of the Plumb LXC to a
# storage of this host. It runs as root on the host. Everything that comes
# from the LXC is data that a compromised user "plumb" may control: the
# pointer is read with a size limit, the name must match one strict form,
# the file is read without following a link, with a size limit, and must
# start with the backup magic; the host builds the name of the copy itself.
# It never needs the recovery key and never decrypts anything.
#   plumb-backup-copy [--conf FILE] [--to DIR]
# --to DIR copies to DIR instead of the storage (install.sh --backup-to).
# It prints the path of the copy on success.
set -euo pipefail
umask 077
export LC_ALL=C

conf=/etc/plumb/backup-copy.conf
to=""
while [[ $# -gt 0 ]]; do
	case $1 in
	--conf) conf=${2:?}; shift ;;
	--to) to=${2:?}; shift ;;
	*) echo "plumb-backup-copy: unknown option $1" >&2; exit 2 ;;
	esac
	shift
done

# get KEY: one value of the config file (written by install.sh, root 0600).
get() { sed -n "s/^$1=//p" "$conf" | head -n1; }
lxc=$(get lxc_id)
storage=$(get storage)
stype=$(get storage_type)
base=$(get storage_path)
keep=$(get keep)
max=$(get max_bytes)
mnt=$(get pve_mnt)
[[ $lxc =~ ^[1-9][0-9]*$ && $keep =~ ^[1-9][0-9]*$ && $max =~ ^[1-9][0-9]*$ ]] || {
	echo "plumb-backup-copy: bad config $conf" >&2
	exit 2
}

sid=$storage # the storage id in the status; "" for --to
[[ -z $to ]] || sid=""
base=${to:-$base}
readonly data=/var/lib/plumb/backups status_path=/var/lib/plumb-copy/status.json
name="" sha=""
tmpfiles=()
trap 'rm -f -- "${tmpfiles[@]}"' EXIT

# status RESULT MESSAGE pushes status.json into the LXC (0644 root, in a
# root-owned directory: user plumb cannot plant a link there, D-289 N2).
status() {
	local result=$1 msg=$2 f
	msg=${msg//[^A-Za-z0-9 .,:;()\/_+=-]/ }
	msg=${msg:0:512}
	f=$(mktemp)
	tmpfiles+=("$f")
	printf '{"v":1,"at":"%s","result":"%s","name":"%s","sha256":"%s","storage":"%s","message":"%s"}\n' \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$result" "$name" "$sha" "$sid" "$msg" >"$f"
	pct push "$lxc" "$f" "$status_path" --perms 0644 --user 0 --group 0 >/dev/null 2>&1 || true
}

fail() {
	echo "plumb-backup-copy: $*" >&2
	status failed "$*"
	exit 1
}

# read_lxc PATH LIMIT OUT: the file PATH of the LXC, at most LIMIT bytes,
# never through a link (dd iflag=nofollow runs as root in the LXC).
read_lxc() {
	local rc
	set +o pipefail
	pct exec "$lxc" -- dd if="$1" iflag=nofollow bs=1M status=none </dev/null 2>/dev/null | head -c "$2" >"$3"
	rc=${PIPESTATUS[0]}
	set -o pipefail
	return "$rc"
}

# 1. Where the copy goes. A storage must be active, and a share mounted:
# else the copy would fill the root disk of the host (D-289 N3).
if [[ -z $to ]]; then
	pvesm status --storage "$storage" 2>/dev/null | awk -v s="$storage" '$1 == s && $3 == "active" { ok = 1 } END { exit !ok }' ||
		fail "storage $storage is not active"
	case $stype in
	dir) ;;
	nfs | cifs | cephfs)
		base=$mnt/$storage
		mountpoint -q "$base" || fail "storage $storage is not mounted at $base"
		;;
	*) fail "storage $storage has the type $stype; only dir, nfs, cifs and cephfs" ;;
	esac
fi
dest=$base/plumb-backups
[[ ! -L $dest ]] || fail "$dest is a link"
[[ -d $base ]] || fail "$base does not exist"
[[ -d $dest ]] || mkdir -m 0700 "$dest" || fail "cannot make $dest"
chmod 0700 "$dest"

# 2. The pointer: at most 4 KiB, JSON, a strict name, a size under the cap.
ptr=$(mktemp)
tmpfiles+=("$ptr")
read_lxc "$data/latest" 4097 "$ptr" || fail "cannot read the backup pointer in LXC $lxc (no backup yet?)"
(($(stat -c %s "$ptr") <= 4096)) || fail "the backup pointer is larger than 4 KiB"
fields=$(perl -MJSON::PP -e '
	local $/; my $d = eval { decode_json(<STDIN>) };
	ref $d eq "HASH" or exit 1;
	for (qw(name size sha256)) { defined $d->{$_} && !ref $d->{$_} or exit 1; print "$d->{$_}\n" }' <"$ptr") ||
	fail "the backup pointer is not valid JSON"
{
	read -r name
	read -r size
	read -r sha
} <<<"$fields"
re='^plumb-([0-9]{8}T[0-9]{6}Z)(-[0-9]{1,3})?\.pbk$'
if ! [[ $name =~ $re ]]; then
	name=""
	fail "the backup pointer names no valid backup file"
fi
# The host builds the name from the checked parts (D-289 N1).
ts=${BASH_REMATCH[1]}
final="plumb-${ts}${BASH_REMATCH[2]}.pbk"
# A time far ahead would name a copy that no later backup replaces
# (review M1 of #221): at most 1 day ahead of this host's clock.
when=$(date -u -d "${ts:0:4}-${ts:4:2}-${ts:6:2} ${ts:9:2}:${ts:11:2}:${ts:13:2}" +%s 2>/dev/null) || fail "the backup pointer has no valid time"
((when <= $(date -u +%s) + 86400)) || fail "the backup pointer names a time more than 1 day ahead of this host"
[[ $sha =~ ^[0-9a-f]{64}$ ]] || fail "the backup pointer has no valid sha256"
[[ $size =~ ^[1-9][0-9]{0,15}$ ]] || fail "the backup pointer has no valid size"
((size <= max)) || fail "the backup has $size bytes, more than the limit of $max bytes (--backup-max-size)"

# 3. Copied already: nothing to do but the retention. A copy is never
# replaced: the same name with other content fails (review M1 of #221).
if [[ -e $dest/$final ]]; then
	[[ $(sha256sum <"$dest/$final") == "$sha  -" ]] || fail "refuses to overwrite $final: the copy on the storage has other content"
else
	# 4. The file: temp name in the target directory, checks, rename.
	tmp=$(mktemp -p "$dest" .tmp-XXXXXXXX)
	tmpfiles+=("$tmp")
	read_lxc "$data/$name" $((size + 1)) "$tmp" || fail "cannot read $name in LXC $lxc"
	[[ $(stat -c %s "$tmp") == "$size" ]] || fail "$name has another size than the pointer says"
	printf 'PLUMBBK\0' | cmp -s -n 8 - "$tmp" || fail "$name is not a Plumb backup file"
	# This proves only the transfer: pointer and file come from the same
	# LXC. The GCM tags prove the content at the restore (D-288).
	[[ $(sha256sum <"$tmp") == "$sha  -" ]] || fail "$name has another sha256 than the pointer says"
	chmod 0600 "$tmp"
	touch -- "$tmp" # the retention orders by this time of the host
	mv -n -- "$tmp" "$dest/$final" || true
	[[ ! -e $tmp ]] || fail "refuses to overwrite $final"
	sync -f "$dest/$final" 2>/dev/null || true
fi

# 5. Retention by the time this host made each copy, never by the name
# (review M1 of #221); only names of the strict form go.
mapfile -t all < <(find "$dest" -maxdepth 1 -type f -name 'plumb-*.pbk' -printf '%T@ %f\n' | sort -rn | cut -d' ' -f2 | grep -E "$re")
for old in "${all[@]:keep}"; do
	rm -f -- "${dest:?}/$old"
done

status ok "copied to ${to:-storage $storage}"
echo "$dest/$final"
COPY_EOF
}

do_install() {
	[[ -n $PLUMB_BINARY && -f $PLUMB_BINARY ]] || die "--plumb-binary FILE is required (make build → bin/plumb)"
	[[ -f $PVE_CA ]] || die "$PVE_CA not found"
	NODE=$(query hostname -s)
	local host
	host=$(host_ip)

	say "Plumb install on node $NODE ($host)."
	print_rights
	say "It creates or keeps: pool $POOL, user $PVE_USER, token $PVE_USER!$TOKEN_NAME, storage $SNIPPET_STORAGE ($SNIPPET_DIR),"
	say "the vendor snippet $SNIPPET, the image storage $IMAGE_STORAGE, and an unprivileged Debian 13 LXC \"$LXC_HOSTNAME\" without host mounts."
	say "The LXC gets a key volume ($DISK_STORAGE, ${KEY_VOLUME_GB} GB, $KEY_DIR, backup=0) for the master key."
	if [[ $LXC_IP == dhcp ]]; then
		say "WARNING: the LXC gets its address by DHCP. The vendor snippet holds this address: when the lease"
		say "changes, no agent reaches Plumb any more. Use a static address: --lxc-ip CIDR --lxc-gw IP (D-179)."
	fi
	check_dns
	check_extra_bridges
	# Before check_storages, so its own text names the reason (M2 of #321).
	[[ -z $GUEST_BACKUP_STORAGE ]] || check_second_backup_storage
	check_storages
	check_images
	check_user_groups
	local own=${LXC_ID:-$(state_get lxc_id)}
	if [[ -n $RESTORE_FILE ]]; then
		check_restore
		say "It RESTORES the backup $RESTORE_FILE (install $H_INSTALL_ID, agents at $H_AGENT_URL) into a new LXC:"
		say "plumb backup verify on this host, then plumb key restore and plumb restore in the LXC; each asks on this"
		say "terminal. The restored Plumb changes nothing until you confirm in its UI that the old controller is off."
		say "Give the --extra-bridge, --extra-vlan and --guest-backup-* flags of the old install again: a run without"
		say "them gives no rights for those networks and turns guest backups off (D-401, D-405)."
	elif [[ -z $own || $(lxc_state "$own") != ours ]] && pool_has_guests; then
		die "pool $POOL holds guests or templates, and this host has no Plumb LXC: a plain install does not take them. Use --restore BACKUP (after --remove --keep-guests). Nothing was changed."
	fi
	ensure_install_id
	check_guest_backup
	check_copy_setting
	check_key_preflight
	# The id that ensure_lxc will use when it creates the LXC.
	local new_id=${LXC_ID:-$(state_get lxc_id)}
	[[ -n $new_id ]] || new_id=$(query pvesh get /cluster/nextid)
	check_stale_lxc_dir "$new_id"
	STRAY_ACLS=$(stray_acls)
	if [[ -n $STRAY_ACLS ]]; then
		say "These older rights of Plumb are not in the plan and are removed:"
		printf '  %s\n' "${STRAY_ACLS//$'\n'/$'\n'  }"
	fi
	confirm "Continue?" || die "stopped; nothing was changed"
	if [[ -n $RESTORE_FILE ]]; then
		verify_on_host "$RESTORE_FILE" "nothing was changed"
		remove_half_lxc
		RESTORING=1
	fi

	ensure_image_storage
	state_put # --remove must know plumb-images even if a later step fails
	ensure_pool_user_roles
	ensure_snippet_storage
	state_put
	[[ -n $LXC_ID ]] || LXC_ID=$(state_get lxc_id)
	ensure_token
	ensure_acls
	ensure_lxc
	state_put
	local ip ca
	ip=$(lxc_ip)
	setup_lxc "$host" "$ip"
	RESTORING=0 # serve runs with the restored database
	state_put
	new_tmp ca
	fetch_plumb_ca "$ca"
	write_snippet "$ca" "https://$ip:8444"
	rm -f "$ca"
	setup_copy_timer
	state_put

	say ""
	say "Plumb runs: https://$ip:8443 (certificate from the Plumb CA)."
	if [[ -n $RESTORE_FILE ]]; then
		say "Restored from $RESTORE_FILE. Log in as admin with the password that plumb restore showed. Plumb is in"
		say "restored mode: it observes, and changes nothing until you confirm that the old controller is off (D-290)."
		if [[ -z $BACKUP_STORAGE_SET ]]; then
			say "No off-host backup copy is configured. Add one with a repair run: install.sh --plumb-binary FILE --backup-storage ID"
		fi
		return 0
	fi
	if [[ $DRY_RUN == 1 ]]; then
		say "Initial login: user admin; the password is printed once by the first start."
		return 0
	fi
	local pw
	pw=$({ query pct exec "$LXC_ID" -- journalctl -u plumb.service -o cat --no-pager </dev/null 2>/dev/null || true; } | sed -n 's/^First start\. User: admin  Password: //p' | head -n1)
	if [[ -n $pw && $LXC_NEW == 1 ]]; then
		say "Initial login: user admin, password $pw (change it at the first login)."
	else
		say "Initial login: user admin; see: pct exec $LXC_ID -- journalctl -u plumb.service | grep Password"
	fi
}

do_snippet_only() {
	[[ -n $CA_FILE && -f $CA_FILE ]] || die "--ca FILE is required"
	[[ -n $AGENT_URL ]] || die "--url URL is required"
	say "Writing only the snippet $SNIPPET_DIR/snippets/$SNIPPET for $AGENT_URL."
	confirm "Continue?" || die "stopped; nothing was changed"
	ensure_snippet_storage
	write_snippet "$CA_FILE" "$AGENT_URL"
}

do_remove() {
	NODE=$(query hostname -s)
	local lxc dir
	lxc=$(state_get lxc_id)
	# Only what the state file names is removed; a --snippet-dir from the
	# command line is never deleted (review T-013 #1).
	dir=$(state_get snippet_dir)
	if [[ $SNIPPET_DIR_SET == 1 && $SNIPPET_DIR != "$dir" ]]; then
		die "--snippet-dir $SNIPPET_DIR is not the directory of the install (${dir:-none}); nothing was removed"
	fi
	if [[ -n $lxc && $(lxc_state "$lxc") == foreign ]]; then
		die "container $lxc of the state file is not the Plumb LXC (no $LXC_MARKER marker); nothing was removed"
	fi
	# The pool guard fails closed: a pool whose members cannot be read is
	# not removed (review T-013 #3).
	if query pveum pool list --output-format json | json_has poolid "$POOL"; then
		local members
		members=$(query pvesh get /pools --poolid "$POOL" --output-format json 2>/dev/null) ||
			die "cannot read the members of pool $POOL; nothing was removed"
		json_has poolid "$POOL" <<<"$members" || die "cannot read the members of pool $POOL; nothing was removed"
		if [[ $members =~ \"vmid\" ]]; then
			die "pool $POOL still has guests or templates; remove them through Plumb first. Nothing was removed."
		fi
	fi
	local keyvol=""
	[[ -n $lxc && $(lxc_state "$lxc") == ours ]] && keyvol=$(lxc_key_volume "$lxc")
	keyvol=${keyvol#*: }
	keyvol=${keyvol%%,*}
	say "This removes:"
	[[ -n $lxc ]] && say "  the Plumb LXC $lxc with its disk (all Plumb data) and its key volume${keyvol:+ $keyvol} (the master key)"
	if [[ -n $lxc ]]; then
		say "  A backup of the Plumb database stays readable only with the recovery key of \"plumb key init\";"
		say "  without it, the stored secrets in any backup are lost for good."
	fi
	say "  token $PVE_USER!$TOKEN_NAME, user $PVE_USER (with its ACLs), roles ${ROLE_NAMES[*]}"
	say "  pool $POOL, storage $SNIPPET_STORAGE${dir:+, the snippet $dir/snippets/$SNIPPET and its empty directories}, $STATE_FILE"
	say "  the backup copy timer, if any (the copies on the storage stay)"
	confirm "Remove all of it?" remove || die "stopped; nothing was removed"
	remove_copy_timer

	if [[ -n $lxc && $(lxc_state "$lxc") == ours ]]; then
		run pct stop "$lxc"
		run pct destroy "$lxc" --purge 1
		# pct destroy removes the mount point volumes too; make sure of it.
		if [[ -n $keyvol ]] && { query pvesm list "${keyvol%%:*}" --vmid "$lxc" 2>/dev/null || true; } | grep -q "^$keyvol "; then
			run pvesm free "$keyvol"
		fi
		# pct destroy leaves rules.seccomp and passthrough/ (see stale_lxc_entry).
		if [[ $DRY_RUN == 1 ]]; then
			say "(dry run: would remove the Proxmox leftovers rules.seccomp, passthrough/, rootfs/ and $LXC_DIR/$lxc)"
		else
			clean_stale_lxc_dir "$lxc" keep
		fi
	fi
	if query pveum user list --output-format json | json_has userid "$PVE_USER"; then
		if query pveum user token list "$PVE_USER" --output-format json 2>/dev/null | json_has tokenid "$TOKEN_NAME"; then
			remove_token
		fi
		run pveum user delete "$PVE_USER"
	fi
	local roles role
	roles=$(query pveum role list --output-format json)
	for role in "${ROLE_NAMES[@]}"; do
		if json_has roleid "$role" <<<"$roles"; then
			run pveum role delete "$role"
		fi
	done
	if query pveum pool list --output-format json | json_has poolid "$POOL"; then
		run pveum pool delete "$POOL"
	fi
	if query pvesm status --storage "$SNIPPET_STORAGE" >/dev/null 2>&1; then
		run pvesm remove "$SNIPPET_STORAGE"
	fi
	if [[ -n $dir ]]; then
		run rm -f -- "$dir/snippets/$SNIPPET"
		# rmdir fails when a directory holds anything else: then it stays.
		run rmdir -- "$dir/snippets" "$dir" 2>/dev/null || say "Kept $dir: it holds files that this script did not make."
	fi
	local images
	images=$(state_get image_dir)
	if [[ -z $images ]] && query pvesm status --storage plumb-images >/dev/null 2>&1; then
		say "Kept storage plumb-images: the state file has no entry for it, so install.sh did not make it (or did not record it)."
	fi
	if [[ -n $images ]] && query pvesm status --storage plumb-images >/dev/null 2>&1; then
		run pvesm remove plumb-images
		local sub
		# The subdirectories of the content types (pvesm makes them).
		for sub in import template/cache template; do
			[[ -d $images/$sub ]] && { run rmdir -- "$images/$sub" 2>/dev/null || true; }
		done
		run rmdir -- "$images" 2>/dev/null || say "Kept $images: it holds the downloaded images and templates (import/, template/cache/); remove them by hand if you want."
	fi
	run rm -f -- "$STATE_FILE"
	# The dir that install.sh made for the state file, when it is empty.
	run rmdir -- "$(dirname "$STATE_FILE")" 2>/dev/null || true
	say "Plumb is removed."
}

# check_state_file stops when the state file names an LXC id that is not a
# number: the id goes into pct commands and paths (review nit).
check_state_file() {
	local id
	id=$(state_get lxc_id)
	[[ -z $id || $id =~ ^[0-9]+$ ]] || die "$STATE_FILE has an lxc_id that is not a number; check it by hand. Nothing was changed."
}

# do_remove_keep replaces the controller and keeps the guests (D-290,
# D-304, D-313): first a fresh backup, copied off and checked with the
# recovery key; any failure stops before a change. Then only the LXC, its
# key volume, the copy timer and the token (with its ACLs, deleted first:
# remove_token) go.
do_remove_keep() {
	NODE=$(query hostname -s)
	local lxc keyvol addr kept_backup_storage restore_copy_arg
	kept_backup_storage=$(state_get backup_storage)
	restore_copy_arg=${kept_backup_storage:+ --backup-storage $kept_backup_storage}
	lxc=$(state_get lxc_id)
	[[ -n $lxc && $(lxc_state "$lxc") == ours ]] || die "no Plumb LXC of this script on this host; there is no controller to replace. Nothing was removed."
	LXC_ID=$lxc
	[[ -n $PLUMB_BINARY && -f $PLUMB_BINARY ]] || die "--keep-guests needs --plumb-binary FILE: it checks the backup on this host. Nothing was removed."
	if [[ -n $BACKUP_TO ]]; then
		check_backup_to
	else
		BACKUP_STORAGE=$(state_get backup_storage)
		[[ -n $BACKUP_STORAGE ]] || die "--keep-guests needs a copy off the LXC: install with --backup-storage ID first, or give --backup-to DIR. Nothing was removed."
		check_backup_storage
	fi
	need_tty "--remove --keep-guests"
	ensure_install_id
	# The address with its prefix and the gateway, for the exact --restore
	# line (review L2 of #221). A DHCP LXC: the address it has now.
	local net0 gw
	net0=$({ query pct config "$lxc" 2>/dev/null || true; } | sed -n 's/^net0: //p' | head -n1)
	addr=$(sed -n 's/.*\(^\|,\)ip=\([0-9.]*\/[0-9]*\)\(,.*\|$\)/\2/p' <<<"$net0")
	gw=$(sed -n 's/.*\(^\|,\)gw=\([0-9.]*\)\(,.*\|$\)/\2/p' <<<"$net0")
	if [[ -z $addr ]]; then
		addr=$({ query pct exec "$lxc" -- hostname -I </dev/null 2>/dev/null || true; } | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -n1 || true)
		[[ -z $addr ]] || addr+=/24
		say "NOTE: LXC $lxc has a DHCP address; the restore needs it as a static address ($addr is a guess of the prefix)."
	fi
	[[ $addr =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] || die "cannot read the address of LXC $lxc; the restore needs it. Nothing was removed."
	[[ -z $gw || $gw =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || gw=""
	keyvol=$(lxc_key_volume "$lxc")
	keyvol=${keyvol#*: }
	keyvol=${keyvol%%,*}

	say "This replaces the Plumb controller and keeps the guests:"
	say "  1. plumb backup now in LXC $lxc, then a copy off the LXC to ${BACKUP_TO:-storage $BACKUP_STORAGE}"
	if [[ -n $BACKUP_TO ]]; then
		say "     WARNING: $BACKUP_TO is a directory on THIS host, not an off-host copy. If the host disk dies,"
		say "     the copy dies with it."
	fi
	say "  2. plumb backup verify of the copy on this host: type the recovery key (a wrong key stops here)"
	say "  3. then it removes: the LXC $lxc with its disk${keyvol:+, the key volume $keyvol}, the backup copy timer,"
	say "     and the token $PVE_USER!$TOKEN_NAME with its ACLs"
	say "  It keeps: pool $POOL with its guests and templates, user $PVE_USER and its ACLs, the roles, the storages,"
	say "  $SNIPPET_STORAGE with the vendor snippet, and $STATE_FILE (install $INSTALL_ID, pool, node, address $addr${gw:+, gateway $gw})."
	say "  Then: install.sh --plumb-binary FILE --restore BACKUP --lxc-ip $addr --lxc-gw ${gw:-<gateway>}$restore_copy_arg"
	confirm "Replace the controller and keep the guests?" keep-guests || die "stopped; nothing was removed"

	run pct exec "$lxc" -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb backup now --data /var/lib/plumb </dev/null ||
		die "plumb backup now failed in LXC $lxc; nothing was removed"
	copy_once "$BACKUP_TO"
	verify_on_host "$COPY_RESULT" "nothing was removed"
	say "The backup is at $COPY_RESULT. Keep it with the recovery key."

	remove_copy_timer
	run pct stop "$lxc"
	run pct destroy "$lxc" --purge 1
	if [[ -n $keyvol ]] && { query pvesm list "${keyvol%%:*}" --vmid "$lxc" 2>/dev/null || true; } | grep -q "^$keyvol "; then
		run pvesm free "$keyvol"
	fi
	if [[ $DRY_RUN == 1 ]]; then
		say "(dry run: would remove the Proxmox leftovers of LXC $lxc in $LXC_DIR/$lxc)"
	else
		clean_stale_lxc_dir "$lxc" keep
	fi
	if query pveum user token list "$PVE_USER" --output-format json 2>/dev/null | json_has tokenid "$TOKEN_NAME"; then
		# The user keeps its own ACLs (D-304).
		remove_token
	fi
	# The kept state: no LXC, the install id, pool, node and old address.
	local tmp
	new_tmp tmp
	printf 'lxc_id=\npool=%s\nsnippet_storage=%s\nsnippet_dir=%s\nimage_dir=%s\ninstall_id=%s\nbackup_storage=%s\nkept_guests=1\nnode=%s\nold_address=%s\nold_gateway=%s\n' \
		"$POOL" "$SNIPPET_STORAGE" "$(state_get snippet_dir)" "$(state_get image_dir)" "$INSTALL_ID" "$kept_backup_storage" "$NODE" "$addr" "$gw" >"$tmp"
	run install -o root -g root -m 0600 "$tmp" "$STATE_FILE"
	rm -f "$tmp"
	say "The controller is removed; the guests run on. Restore with:"
	say "  install.sh --plumb-binary FILE --restore $COPY_RESULT --lxc-ip $addr --lxc-gw ${gw:-<gateway>}$restore_copy_arg"
}

main() {
	parse_args "$@"
	check_host
	check_state_file
	case $MODE in
	install) do_install ;;
	snippet) do_snippet_only ;;
	remove)
		if [[ $KEEP_GUESTS == 1 ]]; then
			do_remove_keep
		else
			do_remove
		fi
		;;
	esac
}

NODE=""
LXC_NEW=0
LXC_OURS=0
SNIPPETS_DONE=0
IMAGES_MADE=0
STRAY_ACLS=""
main "$@"
