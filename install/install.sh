#!/usr/bin/env bash
# install.sh – prepare a Proxmox VE host for Plumb (T-013, D-007, D-081..D-084,
# T-052: key volume and plumb key init, D-177..D-179).
#
# Run as root on the Proxmox host, on a terminal (plumb key init shows the
# recovery key there once):
#   install.sh --plumb-binary ./plumb [options]        install or repair
#   install.sh --snippet-only --ca FILE --url URL      write only the vendor snippet
#   install.sh --remove                                remove what this script made
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
DRY_RUN=0
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

Options:
  --plumb-binary FILE    the plumb binary (make build → bin/plumb)
  --disk-storage ID      storage for guest disks and the LXC (default local-lvm)
  --image-storage ID     storage for downloaded images; default: the new dir storage plumb-images.
                         Another storage must have exactly the content "import" or "import,vztmpl"
                         (D-081, D-229); install.sh does not change it
  --template-storage ID  storage for the LXC template (default local)
  --bridge NAME          bridge for guests and the LXC (default vmbr0)
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
		[[ $rc == 0 ]] || echo "install.sh: \"pct $2\" failed (exit $rc); the run stops here" >&2
		return $rc
	fi
	"$@"
}

# query reads only. It runs in --dry-run too.
query() { "$@"; }

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
	[[ $VMID_MIN =~ ^[0-9]+$ && $VMID_MAX =~ ^[0-9]+$ && $VMID_MIN -ge 100 && $VMID_MAX -ge $VMID_MIN ]] ||
		die "bad --vmid-range"
	[[ -z $LXC_ID || $LXC_ID =~ ^[1-9][0-9]{2,8}$ ]] || die "bad --lxc-id"
	[[ $LXC_IP == dhcp || $LXC_IP =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] || die "bad --lxc-ip (dhcp or CIDR)"
	[[ -z $LXC_GW || $LXC_GW =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || die "bad --lxc-gw"
	[[ $SNIPPET_DIR == /* && $SNIPPET_DIR != *..* ]] || die "bad --snippet-dir"
	[[ $IMAGE_DIR == /* && $IMAGE_DIR != *..* ]] || die "bad --image-dir"
}

# --- checks ---

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
ROLE_NAMES=(PlumbGuests PlumbDisk PlumbImages PlumbSnippets PlumbBridge PlumbNode PlumbAudit)
declare -A ROLE_PRIVS=(
	[PlumbGuests]="VM.Allocate VM.Audit VM.Clone VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit"
	[PlumbDisk]="Datastore.AllocateSpace Datastore.Audit"
	[PlumbImages]="Datastore.AllocateTemplate Datastore.Audit"
	[PlumbSnippets]="Datastore.AllocateSpace"
	[PlumbBridge]="SDN.Use"
	[PlumbNode]="Sys.AccessNetwork"
	[PlumbAudit]="Sys.Audit"
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
	esac
}

# role_propagate: only the pool ACL is inherited (pool members).
role_propagate() {
	if [[ $1 == PlumbGuests ]]; then echo 1; else echo 0; fi
}

print_rights() {
	say "Plumb gets exactly these rights (token ${PVE_USER}!${TOKEN_NAME}, privilege separation):"
	local role
	for role in "${ROLE_NAMES[@]}"; do
		printf '  %-38s %-14s %s\n' "$(role_path "$role")" "$role" "${ROLE_PRIVS[$role]}"
	done
	say "Sys.Audit on / is not inherited (propagate 0). It reads the thin-pool metadata use for the disk gate. It also lets"
	say "Plumb read, never write, cluster-wide (D-240):"
	say "  - the task list of all users, root included (no task logs);"
	say "  - the use metrics (CPU, memory, disk, network, uptime, with history) of every guest, node and storage;"
	say "  - the IDs, names and volumes of the guests in backup jobs, and the backup jobs and their config;"
	say "  - the node disk lists and SMART data, the PCI list, /etc/hosts and the node config;"
	say "  - all datacenter options, cluster status and config, HA, metric-server config (not its token),"
	say "    cluster firewall options, rules, security groups, aliases and IP sets, realm sync jobs, Ceph status."
	say "  It reads no config of a guest outside pool $POOL, and no node status, syslog, network or task log."
	say "It gets no Sys.Modify, no Sys.Audit on /nodes or any other path, no Permissions.Modify, Pool.Allocate, console"
	say "or guest-agent right, and no other ACL."
	say "The Plumb LXC is not in pool $POOL, so Plumb cannot change itself or foreign guests."
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
	local path ugid role type propagate planned r acls
	# Read the list first: a failing pveum or perl stops the run here
	# instead of looking like "no old rights" (review T-013 n1).
	acls=$(acl_entries) || die "cannot read the ACLs of $PVE_USER; nothing was changed"
	while read -r path ugid role type propagate; do
		[[ -n $path ]] || continue
		planned=0
		for r in "${ROLE_NAMES[@]}"; do
			if [[ $path == "$(role_path "$r")" && $role == "$r" ]]; then
				planned=1
			fi
		done
		[[ $planned == 1 ]] || printf '%s %s %s %s\n' "$path" "$ugid" "$role" "$type"
	done <<<"$acls"
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

remove_stray_acls() {
	local path ugid role type
	while read -r path ugid role type; do
		[[ -n $path ]] || continue
		if [[ $type == token ]]; then
			run pveum acl delete "$path" --tokens "$ugid" --roles "$role"
		else
			run pveum acl delete "$path" --users "$ugid" --roles "$role"
		fi
	done <<<"$STRAY_ACLS"
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
	for role in "${ROLE_NAMES[@]}"; do
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
# not exist yet ("no such token"; test host, T-013 step 3), and a replaced
# token has lost the ACLs of the old one.
ensure_acls() {
	local role acls
	acls=$(acl_entries) || die "cannot read the ACLs of $PVE_USER"
	for role in "${ROLE_NAMES[@]}"; do
		local propagate path
		propagate=$(role_propagate "$role")
		path=$(role_path "$role")
		# An ACL that exists as planned is kept (repair run).
		grep -qxF "$path $PVE_USER $role user $propagate" <<<"$acls" ||
			run pveum acl modify "$path" --users "$PVE_USER" --roles "$role" --propagate "$propagate"
		grep -qxF "$path $PVE_USER!$TOKEN_NAME $role token $propagate" <<<"$acls" ||
			run pveum acl modify "$path" --tokens "$PVE_USER!$TOKEN_NAME" --roles "$role" --propagate "$propagate"
	done
	remove_stray_acls
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
		run pveum user token remove "$PVE_USER" "$TOKEN_NAME"
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
	content=$(printf 'lxc_id=%s\npool=%s\nsnippet_storage=%s\nsnippet_dir=%s\nimage_dir=%s' "$lxc" "$POOL" "$SNIPPET_STORAGE" "$snippets" "$images")
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
	key_init
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
		pct exec "$LXC_ID" -- rm -f "$tmp" </dev/null >/dev/null 2>&1 || true
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
	check_storages
	check_images
	check_user_groups
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
	state_put
	new_tmp ca
	fetch_plumb_ca "$ca"
	write_snippet "$ca" "https://$ip:8444"
	rm -f "$ca"

	say ""
	say "Plumb runs: https://$ip:8443 (certificate from the Plumb CA)."
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
	confirm "Remove all of it?" remove || die "stopped; nothing was removed"

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
		run pveum user token remove "$PVE_USER" "$TOKEN_NAME" || true
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

main() {
	parse_args "$@"
	check_host
	check_state_file
	case $MODE in
	install) do_install ;;
	snippet) do_snippet_only ;;
	remove) do_remove ;;
	esac
}

NODE=""
LXC_NEW=0
LXC_OURS=0
SNIPPETS_DONE=0
IMAGES_MADE=0
STRAY_ACLS=""
main "$@"
