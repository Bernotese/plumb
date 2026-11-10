#!/bin/sh
# check-embed.sh PLUMB AGENT – the built plumb serves the embedded web UI
# and the embedded agent binary of the same build (T-011).
set -eu
plumb=$1
agent=$2
work=$(mktemp -d)
# Wait for plumb to exit before removing its data: dev mode (simulated
# agents, T-024) still writes there while it shuts down.
trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; rm -rf "$work"' EXIT

api=127.0.0.1:${PLUMB_CHECK_PORT:-18443}
agents=127.0.0.1:${PLUMB_CHECK_AGENT_PORT:-18444}
"$plumb" serve --dev --data "$work/data" --listen "$api" --agent-listen "$agents" --hosts 127.0.0.1 \
	>"$work/out.log" 2>&1 &
pid=$!

i=0
until [ -f "$work/data/link/ca.crt" ] && curl -fs -o /dev/null "http://$api/" 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 100 ]; then
		cat "$work/out.log"
		echo "check-embed: plumb did not start" >&2
		exit 1
	fi
	sleep 0.1
done

# The UI: GET / and an unknown non-API path return the built index.html.
curl -fsS "http://$api/" >"$work/index.html"
curl -fsS "http://$api/targets/web1" >"$work/fallback.html"
grep -q '<div id="app"' "$work/index.html" || grep -qi '<!doctype html' "$work/index.html" || {
	echo "check-embed: GET / is not the UI" >&2
	exit 1
}
cmp -s "$work/index.html" "$work/fallback.html" || {
	echo "check-embed: unknown paths do not fall back to index.html" >&2
	exit 1
}

# The agent binary: served over the pinned TLS link, and it is this build.
curl -fsS --cacert "$work/data/link/ca.crt" -o "$work/plumb-agent" "https://$agents/agent/v1/binary"
chmod +x "$work/plumb-agent"
want=$("$agent" version)
got=$("$work/plumb-agent" version)
if [ "$got" != "$want" ]; then
	echo "check-embed: served agent says '$got', want '$want'" >&2
	exit 1
fi
grep -q "First start. User: admin" "$work/out.log" || {
	echo "check-embed: no admin password on first start" >&2
	exit 1
}
echo "check-embed: ok ($got, UI served)"
