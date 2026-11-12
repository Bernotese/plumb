package ctpack

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// TokenEnv is the variable of the container's `env` option that holds the
// enrollment token (D-231). The agent reads it from /proc/1/environ.
const TokenEnv = "PLUMB_ENROLL_TOKEN"

// mtime of every Plumb entry: fixed, so the same inputs give the same
// bytes.
var mtime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// firstBootScript is the script of the vendor snippet of install.sh
// (D-082), with @URL@ for the agent URL. A test keeps both texts equal.
const firstBootScript = `#!/bin/sh
# Install the Plumb agent from the controller. Idempotent: it does
# nothing when the agent runs. Only the pinned Plumb CA is trusted:
# --capath/--ca-directory replace the system CA store with a directory
# that holds no hashed CA names (review S1 of T-076).
set -eu
if systemctl is-active --quiet plumb-agent.service; then exit 0; fi
url='@URL@/agent/v1/binary'
ca=/etc/plumb-agent/ca.pem
tmp=$(mktemp)
new=/usr/local/bin/.plumb-agent.new
trap 'rm -f "$tmp" "$new"' EXIT
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --proto =https --connect-timeout 10 --max-time 300 --cacert "$ca" --capath /etc/plumb-agent -o "$tmp" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q --https-only --timeout=60 --tries=1 --ca-certificate="$ca" --ca-directory=/etc/plumb-agent -O "$tmp" "$url"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import ssl,sys,urllib.request; c=ssl.create_default_context(cafile=sys.argv[1]); open(sys.argv[3],"wb").write(urllib.request.urlopen(sys.argv[2],context=c,timeout=60).read())' "$ca" "$url" "$tmp"
  else
    echo "plumb-agent-install: no curl, wget or python3" >&2; return 1
  fi
}
i=0
until fetch; do
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then echo "plumb-agent-install: the controller did not answer" >&2; exit 1; fi
  sleep 10
done
# A temp name and a rename: a crash or a full disk never leaves a half
# binary under the real name (review S4 of T-038).
install -m 0755 "$tmp" "$new"
mv -f "$new" /usr/local/bin/plumb-agent
systemctl daemon-reload
systemctl enable --now plumb-agent.service
`

// FirstBootScript returns /usr/local/sbin/plumb-agent-install for an agent
// URL. The URL must match the rule of Options (no quote can break it).
func FirstBootScript(agentURL string) string {
	return strings.ReplaceAll(firstBootScript, "@URL@", agentURL)
}

// AgentUnit is plumb-agent.service, as in the vendor snippet.
const AgentUnit = `[Unit]
Description=Plumb agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/plumb-agent run
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`

// installUnit runs the first-boot script at every boot; the script exits
// at once when the agent runs. No ConditionPathExists: a container whose
// first fetch failed tries again at the next boot (D-230). The script
// retries up to 10 minutes, so the start has no time limit.
const installUnit = `[Unit]
Description=Install the Plumb agent from the controller
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/plumb-agent-install
TimeoutStartSec=infinity

[Install]
WantedBy=multi-user.target
`

func agentJSON(o Options) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(struct {
		URL      string `json:"url"`
		CA       string `json:"ca"`
		TokenEnv string `json:"token_env"`
	}{o.AgentURL, normPEM(o.CA), TokenEnv})
	return b.Bytes(), err
}

func normPEM(ca []byte) string {
	return strings.TrimRight(strings.ReplaceAll(string(ca), "\r\n", "\n"), "\n") + "\n"
}

type entry struct {
	name   string // normalised
	flag   byte
	mode   int64
	body   string
	target string // symlink
}

func plumbEntries(o Options) ([]entry, error) {
	js, err := agentJSON(o)
	if err != nil {
		return nil, err
	}
	return []entry{
		{name: "etc/plumb-agent", flag: tar.TypeDir, mode: 0o755},
		{name: "etc/plumb-agent/ca.pem", flag: tar.TypeReg, mode: 0o644, body: normPEM(o.CA)},
		{name: "etc/plumb-agent/agent.json", flag: tar.TypeReg, mode: 0o644, body: string(js)},
		{name: "usr/local/sbin/plumb-agent-install", flag: tar.TypeReg, mode: 0o755, body: FirstBootScript(o.AgentURL)},
		{name: "etc/systemd/system/plumb-agent-install.service", flag: tar.TypeReg, mode: 0o644, body: installUnit},
		{name: "etc/systemd/system/plumb-agent.service", flag: tar.TypeReg, mode: 0o644, body: AgentUnit},
		{name: "etc/systemd/system/multi-user.target.wants/plumb-agent-install.service", flag: tar.TypeSymlink, mode: 0o777,
			target: "/etc/systemd/system/plumb-agent-install.service"},
		{name: "etc/systemd/system/multi-user.target.wants/plumb-agent.service", flag: tar.TypeSymlink, mode: 0o777,
			target: "/etc/systemd/system/plumb-agent.service"},
	}, nil
}

// writePlumb writes the Plumb entries as USTAR and the end marker. prefix
// is "./" or "", as the base names.
func writePlumb(w io.Writer, o Options, prefix string) error {
	es, err := plumbEntries(o)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	for _, e := range es {
		h := &tar.Header{
			Typeflag: e.flag,
			Name:     prefix + e.name,
			Linkname: e.target,
			Mode:     e.mode,
			Uname:    "root",
			Gname:    "root",
			ModTime:  mtime,
			Format:   tar.FormatUSTAR,
		}
		if e.flag == tar.TypeDir {
			h.Name += "/"
		}
		if e.flag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.WriteString(tw, e.body); err != nil {
			return err
		}
	}
	return tw.Close()
}
