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
# Test hooks (install/install_test.go; they only point the script at test
# files and change nothing for root on a real host):
#   PLUMB_INSTALL_ASSUME_ROOT=1   skip the root check (fake commands only)
#   PLUMB_INSTALL_PVE_CA=FILE     the host CA file instead of /etc/pve/pve-root-ca.pem
#   PLUMB_INSTALL_STATE=FILE      the state file instead of /etc/plumb/install.state
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
STATE_FILE=${PLUMB_INSTALL_STATE:-/etc/plumb/install.state}

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
                         Another storage must have exactly the content "import" (D-081)
  --template-storage ID  storage for the LXC template (default local)
  --bridge NAME          bridge for guests and the LXC (default vmbr0)
  --lxc-id N             VMID of the Plumb LXC (default: next free id)
  --lxc-ip CIDR|dhcp     address of the LXC (default dhcp; a static address is recommended:
                         the vendor snippet holds it, D-179)
  --lxc-gw IP            gateway for a static LXC address
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
trap cleanup_tmp EXIT
trap 'exit 130' INT TERM
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

# --- roles and rights (ARCHITECTURE §7, D-032, D-081) ---

# One role per ACL path, so no path gets a right it does not need.
ROLE_NAMES=(PlumbGuests PlumbDisk PlumbImages PlumbSnippets PlumbBridge PlumbNode)
declare -A ROLE_PRIVS=(
	[PlumbGuests]="VM.Allocate VM.Audit VM.Clone VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit"
	[PlumbDisk]="Datastore.AllocateSpace Datastore.Audit"
	[PlumbImages]="Datastore.AllocateTemplate Datastore.Audit"
	[PlumbSnippets]="Datastore.AllocateSpace"
	[PlumbBridge]="SDN.Use"
	[PlumbNode]="Sys.AccessNetwork"
)

role_path() {
	case $1 in
	PlumbGuests) echo "/pool/$POOL" ;;
	PlumbDisk) echo "/storage/$DISK_STORAGE" ;;
	PlumbImages) echo "/storage/$IMAGE_STORAGE" ;;
	PlumbSnippets) echo "/storage/$SNIPPET_STORAGE" ;;
	PlumbBridge) echo "/sdn/zones/localnetwork/$BRIDGE" ;;
	PlumbNode) echo "/nodes/$NODE" ;;
	esac
}

print_rights() {
	say "Plumb gets exactly these rights (token ${PVE_USER}!${TOKEN_NAME}, privilege separation):"
	local role
	for role in "${ROLE_NAMES[@]}"; do
		printf '  %-38s %-14s %s\n' "$(role_path "$role")" "$role" "${ROLE_PRIVS[$role]}"
	done
	say "It gets no Sys.Modify, Sys.Audit, Permissions.Modify, Pool.Allocate, console or guest-agent right, and no other ACL."
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
      # nothing when the agent runs. Only the pinned Plumb CA is trusted.
      set -eu
      if systemctl is-active --quiet plumb-agent.service; then exit 0; fi
      url='$url/agent/v1/binary'
      ca=/etc/plumb-agent/ca.pem
      tmp=\$(mktemp)
      trap 'rm -f "\$tmp"' EXIT
      fetch() {
        if command -v curl >/dev/null 2>&1; then
          curl -fsS --proto =https --connect-timeout 10 --max-time 300 --cacert "\$ca" -o "\$tmp" "\$url"
        elif command -v wget >/dev/null 2>&1; then
          wget -q --https-only --timeout=60 --tries=1 --ca-certificate="\$ca" -O "\$tmp" "\$url"
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
      install -m 0755 "\$tmp" /usr/local/bin/plumb-agent
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

# check_storages refuses, before any change, a storage setup that would
# let the token reach another snippet or overwrite foreign files (D-081).
check_storages() {
	local c
	c=$(storage_content "$DISK_STORAGE") || die "storage $DISK_STORAGE does not exist"
	[[ ,$c, != *,snippets,* ]] ||
		die "disk storage $DISK_STORAGE has the content \"snippets\": the token could point cicustom at any snippet there (D-081). Use a disk storage without snippets. Nothing was changed."
	if [[ $IMAGE_STORAGE != plumb-images ]]; then
		c=$(storage_content "$IMAGE_STORAGE") || die "storage $IMAGE_STORAGE does not exist"
		[[ $c == import ]] ||
			die "image storage $IMAGE_STORAGE has the content \"$c\"; it must have exactly \"import\" (D-081). Leave --image-storage out to get plumb-images. Nothing was changed."
	elif c=$(storage_content plumb-images); then
		[[ $c == import ]] || die "storage plumb-images exists with the content \"$c\", not \"import\". Nothing was changed."
	fi
	if c=$(storage_content "$SNIPPET_STORAGE"); then
		[[ $c == snippets ]] || die "storage $SNIPPET_STORAGE exists with the content \"$c\", not \"snippets\". Nothing was changed."
	fi
}

# ensure_image_storage makes the dir storage plumb-images (content import
# only, root-owned). It never changes a storage that it did not make.
ensure_image_storage() {
	[[ $IMAGE_STORAGE == plumb-images ]] || return 0
	if ! storage_content plumb-images >/dev/null; then
		run install -d -o root -g root -m 0755 "$IMAGE_DIR"
		run pvesm add dir plumb-images --path "$IMAGE_DIR" --content import --shared 0
		IMAGES_MADE=1
	elif [[ -n $(state_get image_dir) ]]; then
		IMAGES_MADE=1 # made by an earlier run of this script
	elif images_like_ours; then
		# An earlier run made it and stopped before it wrote the state file
		# (test host, T-013 step 3). It is exactly what this script makes, so
		# it is adopted: --remove then removes the storage entry, and never
		# a file in it (rmdir only).
		say "Storage plumb-images ($IMAGE_DIR, content import) exists without a state entry; it is exactly what install.sh makes, so it is adopted."
		IMAGES_MADE=1
	else
		say "Storage plumb-images exists, but not as install.sh makes it; it is used and --remove keeps it."
	fi
}

# images_like_ours reports whether plumb-images is exactly the storage that
# ensure_image_storage makes: type dir, path IMAGE_DIR, content import, not
# shared.
images_like_ours() {
	local cfg
	cfg=$(query pvesh get /storage/plumb-images --output-format json 2>/dev/null) || return 1
	[[ $cfg =~ \"type\"\ *:\ *\"dir\" ]] &&
		[[ $cfg =~ \"path\"\ *:\ *\"([^\"]*)\" && ${BASH_REMATCH[1]} == "$IMAGE_DIR" ]] &&
		[[ $cfg =~ \"content\"\ *:\ *\"import\" ]] &&
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
		local propagate=0 path
		[[ $role == PlumbGuests ]] && propagate=1 # pool members inherit
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
			die "stopped; nothing else was changed"
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
	content=$(printf 'lxc_id=%s\npool=%s\nsnippet_storage=%s\nsnippet_dir=%s\nimage_dir=%s' "$lxc" "$POOL" "$SNIPPET_STORAGE" "$SNIPPET_DIR" "$images")
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
		query pct exec "$LXC_ID" -- test -s /etc/plumb/plumb.env >/dev/null 2>&1
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
				run pct start "$LXC_ID"
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
	# Unprivileged, no host mount, not in the Plumb pool (ARCHITECTURE §7).
	# mp0 is a new volume on the disk storage (not a host path) with
	# backup=0: the key volume (D-177).
	run pct create "$LXC_ID" "$TEMPLATE" --hostname "$LXC_HOSTNAME" --unprivileged 1 --features nesting=1 \
		--cores 2 --memory 1024 --swap 512 --rootfs "$DISK_STORAGE:8" \
		--mp0 "$DISK_STORAGE:$KEY_VOLUME_GB,mp=$KEY_DIR,backup=0" --net0 "$net" --onboot 1 --ostype debian \
		--description "$LXC_MARKER Plumb controller, made by install.sh"
	# The state names the LXC before anything else can fail, so a failed
	# start never leads to a second LXC on the next run (review S1).
	LXC_NEW=1
	LXC_OURS=1
	state_put
	run pct start "$LXC_ID"
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
		ip=$({ query pct exec "$LXC_ID" -- hostname -I 2>/dev/null || true; } | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -n1 || true)
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
PLUMB_HOSTS=$ip,$LXC_HOSTNAME
EOF
	if ! run pct push "$LXC_ID" "$tmp" /etc/plumb/plumb.env --perms 0600 --user 0 --group 0; then
		rm -f "$tmp"
		die "cannot write the settings into the LXC"
	fi
	rm -f "$tmp"
	run pct exec "$LXC_ID" -- chown plumb:plumb /etc/plumb/plumb.env
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
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/var/lib/plumb

[Install]
WantedBy=multi-user.target'

setup_lxc() {
	local host=$1 ip=$2 tmp
	run pct exec "$LXC_ID" -- sh -c 'id plumb >/dev/null 2>&1 || useradd --system --home-dir /var/lib/plumb --shell /usr/sbin/nologin plumb'
	run pct exec "$LXC_ID" -- install -d -o plumb -g plumb -m 0700 /etc/plumb /var/lib/plumb
	# The key directory is the mount point of the key volume (D-102).
	if [[ $DRY_RUN != 1 ]]; then
		query pct exec "$LXC_ID" -- mountpoint -q "$KEY_DIR" ||
			die "$KEY_DIR in LXC $LXC_ID is not a mount point; the key volume is missing"
	fi
	run pct exec "$LXC_ID" -- chown plumb:plumb "$KEY_DIR"
	run pct exec "$LXC_ID" -- chmod 0700 "$KEY_DIR"
	run pct push "$LXC_ID" "$PLUMB_BINARY" /usr/local/bin/plumb --perms 0755 --user 0 --group 0
	run pct push "$LXC_ID" "$PVE_CA" /etc/plumb/pve-root-ca.pem --perms 0644 --user 0 --group 0
	if [[ -n $TOKEN_SECRET ]]; then
		push_env "$host" "$ip"
	fi
	new_tmp tmp
	printf '%s\n' "$UNIT" >"$tmp"
	run pct push "$LXC_ID" "$tmp" /etc/systemd/system/plumb.service --perms 0644 --user 0 --group 0
	rm -f "$tmp"
	run pct exec "$LXC_ID" -- systemctl daemon-reload
	run pct exec "$LXC_ID" -- systemctl enable plumb.service
	key_init
	run pct exec "$LXC_ID" -- systemctl restart plumb.service # a repair run brings a new binary
}

# lxc_has_key reports whether the LXC holds the master key file.
lxc_has_key() {
	query pct exec "$1" -- test -s "$KEY_FILE" >/dev/null 2>&1
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
	if [[ $DRY_RUN != 1 ]] && lxc_has_key "$LXC_ID"; then
		say "The master key exists in LXC $LXC_ID; it is kept."
		return 0
	fi
	say ""
	say "plumb key init: the RECOVERY KEY follows. Write it down and keep it offline."
	run lxc-attach -n "$LXC_ID" --clear-env -- /usr/sbin/runuser -u plumb -- /usr/local/bin/plumb key init --data /var/lib/plumb ||
		die "plumb key init failed; plumb.service was not started. Run install.sh again on a terminal."
	if [[ $DRY_RUN != 1 ]]; then
		lxc_has_key "$LXC_ID" || die "plumb key init wrote no $KEY_FILE; plumb.service was not started"
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
		if query pct exec "$LXC_ID" -- test -s /var/lib/plumb/link/ca.crt; then
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
	check_storages
	check_user_groups
	check_key_preflight
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
	pw=$({ query pct exec "$LXC_ID" -- journalctl -u plumb.service -o cat --no-pager 2>/dev/null || true; } | sed -n 's/^First start\. User: admin  Password: //p' | head -n1)
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
	if [[ -n $images ]] && query pvesm status --storage plumb-images >/dev/null 2>&1; then
		run pvesm remove plumb-images
		[[ -d $images/import ]] && { run rmdir -- "$images/import" 2>/dev/null || true; }
		run rmdir -- "$images" 2>/dev/null || say "Kept $images: it holds the downloaded images (plumb-*.qcow2); remove them by hand if you want."
	fi
	run rm -f -- "$STATE_FILE"
	say "Plumb is removed."
}

main() {
	parse_args "$@"
	check_host
	case $MODE in
	install) do_install ;;
	snippet) do_snippet_only ;;
	remove) do_remove ;;
	esac
}

NODE=""
LXC_NEW=0
LXC_OURS=0
IMAGES_MADE=0
STRAY_ACLS=""
main "$@"
