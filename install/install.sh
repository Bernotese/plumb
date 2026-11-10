#!/usr/bin/env bash
# install.sh – prepare a Proxmox VE host for Plumb (T-013, D-007, D-081..D-084).
#
# Run as root on the Proxmox host:
#   install.sh --plumb-binary ./plumb [options]        install or repair
#   install.sh --snippet-only --ca FILE --url URL      write only the vendor snippet
#   install.sh --remove                                remove what this script made
# Add --dry-run to print every pveum/pvesm/pveam/pct command instead of
# running it. The script downloads no third-party code: the controller
# binary comes from --plumb-binary, the LXC template from Proxmox's own
# template list (pveam).
set -euo pipefail
umask 077

readonly VERSION_MIN_MAJOR=9 VERSION_MIN_MINOR=1

# --- settings (flags override them) ---
POOL=plumb
PVE_USER=plumb@pve
TOKEN_NAME=plumb
SNIPPET_STORAGE=plumb-snippets
SNIPPET_DIR=/var/lib/plumb-snippets
SNIPPET=plumb-vendor.yaml
DISK_STORAGE=local-lvm
IMAGE_STORAGE=local
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
PVE_CA=${PLUMB_INSTALL_PVE_CA:-/etc/pve/pve-root-ca.pem}
STATE_FILE=${PLUMB_INSTALL_STATE:-/etc/plumb/install.state}

usage() {
	cat <<'EOF'
Usage:
  install.sh --plumb-binary FILE [options]     install Plumb, or repair an install
  install.sh --snippet-only --ca FILE --url URL
  install.sh --remove [--yes]

Options:
  --plumb-binary FILE    the plumb binary (make build → bin/plumb)
  --disk-storage ID      storage for guest disks and the LXC (default local-lvm)
  --image-storage ID     storage for downloaded images, content "import" (default local)
  --template-storage ID  storage for the LXC template (default local)
  --bridge NAME          bridge for guests and the LXC (default vmbr0)
  --lxc-id N             VMID of the Plumb LXC (default: next free id)
  --lxc-ip CIDR|dhcp     address of the LXC (default dhcp)
  --lxc-gw IP            gateway for a static LXC address
  --vmid-range MIN-MAX   VMIDs for Plumb guests (default 9000-9099)
  --pool NAME            pool of Plumb guests (default plumb)
  --snippet-dir DIR      directory of the plumb-snippets storage (default /var/lib/plumb-snippets)
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
		--snippet-dir) SNIPPET_DIR=${2:?}; shift ;;
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
}

# json_has FIELD VALUE: stdin is the JSON of a pveum/pvesh list.
json_has() { grep -Eq "\"$1\" *: *\"$2\""; }

# --- roles and rights (ARCHITECTURE §7, D-032, D-081) ---

# One role per ACL path, so no path gets a right it does not need.
ROLE_NAMES=(PlumbGuests PlumbDisk PlumbImages PlumbSnippets PlumbBridge PlumbNode)
declare -A ROLE_PRIVS=(
	[PlumbGuests]="VM.Allocate VM.Audit VM.Clone VM.Config.CDROM VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit"
	[PlumbDisk]="Datastore.AllocateSpace Datastore.Audit"
	[PlumbImages]="Datastore.AllocateTemplate Datastore.Audit"
	[PlumbSnippets]="Datastore.Audit"
	[PlumbBridge]="SDN.Use"
	[PlumbNode]="Sys.Audit Sys.AccessNetwork"
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
	say "It gets no Sys.Modify, Permissions.Modify, Pool.Allocate, console or guest-agent right."
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
          curl -fsS --proto =https --cacert "\$ca" -o "\$tmp" "\$url"
        elif command -v wget >/dev/null 2>&1; then
          wget -q --https-only --ca-certificate="\$ca" -O "\$tmp" "\$url"
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
		run pvesm set "$SNIPPET_STORAGE" --content snippets
	else
		run pvesm add dir "$SNIPPET_STORAGE" --path "$SNIPPET_DIR" --content snippets --shared 0
	fi
}

write_snippet() {
	local ca=$1 url=$2 tmp
	tmp=$(mktemp)
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

ensure_image_storage() {
	local cfg
	cfg=$(query pvesh get "/storage/$IMAGE_STORAGE" --output-format json) || die "storage $IMAGE_STORAGE does not exist"
	[[ $cfg =~ \"content\"\ *:\ *\"([^\"]*)\" ]] || die "cannot read the content types of $IMAGE_STORAGE"
	local content=${BASH_REMATCH[1]}
	if [[ ,$content, != *,import,* ]]; then
		say "Storage $IMAGE_STORAGE gets the content type \"import\" for cloud images (it has: $content)."
		run pvesm set "$IMAGE_STORAGE" --content "$content,import"
	fi
}

ensure_pool_user_roles() {
	if ! query pveum pool list --output-format json | json_has poolid "$POOL"; then
		run pveum pool add "$POOL" --comment "Plumb guests and templates"
	fi
	local roles role
	roles=$(query pveum role list --output-format json)
	for role in "${ROLE_NAMES[@]}"; do
		if json_has roleid "$role" <<<"$roles"; then
			run pveum role modify "$role" --privs "${ROLE_PRIVS[$role]// /,}" # replaces: exactly these
		else
			run pveum role add "$role" --privs "${ROLE_PRIVS[$role]// /,}"
		fi
	done
	if ! query pveum user list --output-format json | json_has userid "$PVE_USER"; then
		run pveum user add "$PVE_USER" --comment "Plumb controller (API token only, no password)"
	fi
	for role in "${ROLE_NAMES[@]}"; do
		local propagate=0
		[[ $role == PlumbGuests ]] && propagate=1 # pool members inherit
		run pveum acl modify "$(role_path "$role")" --users "$PVE_USER" --roles "$role" --propagate "$propagate"
		run pveum acl modify "$(role_path "$role")" --tokens "$PVE_USER!$TOKEN_NAME" --roles "$role" --propagate "$propagate"
	done
}

# ensure_token sets TOKEN_SECRET when it makes a new token. An existing
# token is kept: its secret is already in the LXC.
TOKEN_SECRET=""
ensure_token() {
	if query pveum user token list "$PVE_USER" --output-format json 2>/dev/null | json_has tokenid "$TOKEN_NAME"; then
		if [[ -n $(state_get lxc_id) ]]; then
			say "Token $PVE_USER!$TOKEN_NAME exists; it is kept."
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

state_put() {
	run install -d -o root -g root -m 0700 "$(dirname "$STATE_FILE")"
	local tmp
	tmp=$(mktemp)
	printf 'lxc_id=%s\npool=%s\nsnippet_storage=%s\nsnippet_dir=%s\n' "$LXC_ID" "$POOL" "$SNIPPET_STORAGE" "$SNIPPET_DIR" >"$tmp"
	run install -o root -g root -m 0600 "$tmp" "$STATE_FILE"
	rm -f "$tmp"
}

# --- the Plumb LXC (D-006, D-083) ---

# lxc_template sets TEMPLATE to the newest Debian 13 template, and
# downloads it through Proxmox's own template list when it is missing.
TEMPLATE=""
lxc_template() {
	local name
	name=$({ query pveam list "$TEMPLATE_STORAGE" || true; } | awk '/debian-13-standard_/ {sub(/.*vztmpl\//, "", $1); print $1}' | sort -V | tail -n1)
	if [[ -z $name ]]; then
		run pveam update
		name=$(query pveam available --section system | awk '/debian-13-standard_/ {print $2}' | sort -V | tail -n1)
		[[ -n $name ]] || die "no debian-13-standard template in the Proxmox template list"
		run pveam download "$TEMPLATE_STORAGE" "$name"
	fi
	TEMPLATE="$TEMPLATE_STORAGE:vztmpl/$name"
}

ensure_lxc() {
	if [[ -z $LXC_ID ]]; then
		LXC_ID=$(state_get lxc_id)
	fi
	if [[ -n $LXC_ID ]] && query pct status "$LXC_ID" >/dev/null 2>&1; then
		say "LXC $LXC_ID exists; it is kept."
		LXC_NEW=0
		return 0
	fi
	if [[ -z $LXC_ID ]]; then
		LXC_ID=$(query pvesh get /cluster/nextid)
	fi
	local net="name=eth0,bridge=$BRIDGE,ip=$LXC_IP"
	[[ -n $LXC_GW ]] && net+=",gw=$LXC_GW"
	lxc_template
	# Unprivileged, no mount point, not in the Plumb pool (ARCHITECTURE §7).
	run pct create "$LXC_ID" "$TEMPLATE" --hostname "$LXC_HOSTNAME" --unprivileged 1 --features nesting=1 \
		--cores 2 --memory 1024 --swap 512 --rootfs "$DISK_STORAGE:8" --net0 "$net" --onboot 1 --ostype debian \
		--description "Plumb controller, installed by install.sh"
	run pct start "$LXC_ID"
	LXC_NEW=1
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
		ip=$({ query pct exec "$LXC_ID" -- hostname -I 2>/dev/null || true; } | awk '{print $1}')
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
	tmp=$(mktemp)
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
	run pct push "$LXC_ID" "$PLUMB_BINARY" /usr/local/bin/plumb --perms 0755 --user 0 --group 0
	run pct push "$LXC_ID" "$PVE_CA" /etc/plumb/pve-root-ca.pem --perms 0644 --user 0 --group 0
	if [[ -n $TOKEN_SECRET ]]; then
		push_env "$host" "$ip"
	fi
	tmp=$(mktemp)
	printf '%s\n' "$UNIT" >"$tmp"
	run pct push "$LXC_ID" "$tmp" /etc/systemd/system/plumb.service --perms 0644 --user 0 --group 0
	rm -f "$tmp"
	run pct exec "$LXC_ID" -- systemctl daemon-reload
	run pct exec "$LXC_ID" -- systemctl enable --now plumb.service
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
	say "the vendor snippet $SNIPPET, and an unprivileged Debian 13 LXC \"$LXC_HOSTNAME\" without host mounts."
	confirm "Continue?" || die "stopped; nothing was changed"

	ensure_image_storage
	ensure_pool_user_roles
	ensure_snippet_storage
	ensure_token
	ensure_lxc
	local ip ca
	ip=$(lxc_ip)
	setup_lxc "$host" "$ip"
	state_put
	ca=$(mktemp)
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
	local lxc members
	lxc=$(state_get lxc_id)
	members=$(query pvesh get "/pools" --poolid "$POOL" --output-format json 2>/dev/null || true)
	if [[ $members =~ \"vmid\" ]]; then
		die "pool $POOL still has guests or templates; remove them through Plumb first. Nothing was removed."
	fi
	say "This removes:"
	[[ -n $lxc ]] && say "  the Plumb LXC $lxc with its disk (all Plumb data)"
	say "  token $PVE_USER!$TOKEN_NAME, user $PVE_USER, roles ${ROLE_NAMES[*]}"
	say "  pool $POOL, storage $SNIPPET_STORAGE and $SNIPPET_DIR, $STATE_FILE"
	confirm "Remove all of it?" remove || die "stopped; nothing was removed"

	if [[ -n $lxc ]] && query pct status "$lxc" >/dev/null 2>&1; then
		run pct stop "$lxc"
		run pct destroy "$lxc" --purge 1
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
	run rm -rf -- "$SNIPPET_DIR"
	run rm -f -- "$STATE_FILE"
	say "Plumb is removed. Images in $IMAGE_STORAGE (plumb-*.qcow2) stay; remove them with pvesm if you want."
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
main "$@"
