#!/bin/sh
# Install or repair the Plumb agent (D-383, D-387, D-390, ARCHITECTURE §19).
# Two modes, one script:
#  - repair: the forced command of the repair line runs it in the
#    extracted bundle dir; plumb-agent (sha256 checked), ca.pem and
#    enroll.json are next to it.
#  - install command: a person pipes it into sh with PLUMB_URL set; the
#    command wrote /etc/plumb-agent/ca.pem and the one-shot token before.
# Both end with the agent unit unmasked, enabled and restarted. The token
# goes only into the 0600 one-shot file, never into an argument or the
# environment of a process; nothing here prints it.
set -eu
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
etc=/etc/plumb-agent
bin=/usr/local/bin/plumb-agent
unit=/etc/systemd/system/plumb-agent.service

die() { echo "plumb-agent-install: $*" >&2; exit 1; }

# check_url accepts https://<host>:<port> with no quote, space or path.
check_url() {
  case $1 in
    https://*) ;;
    *) die "the agent URL is not https" ;;
  esac
  case ${1#https://} in
    *[!A-Za-z0-9.:\[\]-]* | '') die "the agent URL holds an unexpected character" ;;
  esac
}

if [ -z "${PLUMB_URL:-}" ]; then
  # repair mode
  if [ ! -f ./plumb-agent ] || [ ! -f ./enroll.json ] || [ ! -f ./ca.pem ]; then
    die "no repair bundle here and no PLUMB_URL"
  fi
  url=$(sed -n 's/^.*"url":"\([^"]*\)".*$/\1/p' ./enroll.json)
  token=$(sed -n 's/^.*"token":"\([^"]*\)".*$/\1/p' ./enroll.json)
  check_url "$url"
  case $token in
    *[!A-Za-z0-9_-]* | '') die "the bundle holds no valid token" ;;
  esac
  mkdir -p "$etc"
  chmod 0755 "$etc"
  cp ./ca.pem "$etc/.ca.pem.new"
  chmod 0644 "$etc/.ca.pem.new"
  mv -f "$etc/.ca.pem.new" "$etc/ca.pem"
  # printf is a shell builtin: the token is in no argument list.
  rm -f "$etc/.enroll.token.new"
  printf '%s\n' "$token" >"$etc/.enroll.token.new"
  chmod 0600 "$etc/.enroll.token.new"
  mv -f "$etc/.enroll.token.new" "$etc/enroll.token"
  token=
  install -m 0755 ./plumb-agent /usr/local/bin/.plumb-agent.new
  mv -f /usr/local/bin/.plumb-agent.new "$bin"
else
  # install command mode
  url=$PLUMB_URL
  check_url "$url"
  [ -s "$etc/ca.pem" ] || die "$etc/ca.pem is missing"
  [ -s "$etc/enroll.token" ] || die "$etc/enroll.token is missing"
  chmod 0755 "$etc"
  chmod 0644 "$etc/ca.pem"
  chmod 0600 "$etc/enroll.token"
  tmp=$(mktemp)
  trap 'rm -f "$tmp" /usr/local/bin/.plumb-agent.new' EXIT
  # Only the Plumb CA is trusted: --capath points at a dir with no hashed
  # CA names (as the first-boot script, review S1 of T-076).
  curl -fsS --proto =https --connect-timeout 10 --max-time 300 --cacert "$etc/ca.pem" --capath "$etc" \
    -o "$tmp" "$url/agent/v1/binary" || die "could not fetch the agent binary"
  install -m 0755 "$tmp" /usr/local/bin/.plumb-agent.new
  mv -f /usr/local/bin/.plumb-agent.new "$bin"
fi

# agent.json: the URL and the CA of this controller. A token_env of the
# old file stays (an LXC gets its re-enrollment token that way, D-231);
# a VM keeps the default token source (SMBIOS serial), so a later
# re-enrollment works as before. The one-shot token needs no entry.
tokenenv=
if [ -f "$etc/agent.json" ]; then
  tokenenv=$(sed -n 's/^.*"token_env":"\([A-Za-z_][A-Za-z0-9_]*\)".*$/\1/p' "$etc/agent.json" | head -n 1)
fi
ca=$(awk '{ sub(/\r$/, ""); printf "%s\\n", $0 }' "$etc/ca.pem")
case $ca in
  *[\"]*) die "ca.pem holds a quote" ;;
esac
{
  printf '{"url":"%s","ca":"%s"' "$url" "$ca"
  if [ -n "$tokenenv" ]; then printf ',"token_env":"%s"' "$tokenenv"; fi
  printf '}\n'
} >"$etc/.agent.json.new"
chmod 0644 "$etc/.agent.json.new"
mv -f "$etc/.agent.json.new" "$etc/agent.json"

cat >"$unit.new" <<'PLUMB_UNIT'
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
PLUMB_UNIT
chmod 0644 "$unit.new"
mv -f "$unit.new" "$unit"

systemctl unmask plumb-agent.service
systemctl daemon-reload
systemctl enable plumb-agent.service
systemctl restart plumb-agent.service
echo "plumb-agent-install: the agent is installed and restarted"
