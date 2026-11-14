package agentinstall

import (
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// InstallCommand is the root shell command of D-387 that a person runs in
// a guest without the repair line (an adopted guest, an LXC without sshd).
// It fails loudly, never silently (host finding F6 of T-192):
//
//   - The whole work runs in a subshell, so an error ends only the
//     subshell, never the person's shell, and its exit code is the
//     command's exit code.
//   - Without curl it says "install curl first" and stops; Plumb installs
//     no package. There is no fallback without the CA pin.
//   - The script is downloaded to a file first (no "curl | sh", which
//     hides curl's exit code and runs an empty script with exit 0), with
//     only the Plumb CA (--capath to a dir without hashed names, review S1
//     of T-076) and only https.
//   - A cut or empty download has no `main "$@"` as its last line (the
//     guard of install.sh) and is refused.
//   - The one-time token is written only after a good download: a failed
//     try writes no token file, and the token stays valid until it is
//     used or expires, so the same command can run again. It is never on a
//     command line or in the output.
//
// Every refusal is one line on stderr that starts with "plumb-install:".
func InstallCommand(ca, agentURL, token string) string {
	ca = strings.TrimRight(ca, "\n") + "\n"
	etc := "/etc/plumb-agent"
	return "umask 077; mkdir -p " + etc + " && cat > " + etc + "/ca.pem <<'PLUMB_CA'\n" + ca + "PLUMB_CA\n" +
		"(\n" +
		"command -v curl >/dev/null 2>&1 || { echo 'plumb-install: curl is missing in this guest. Install it first (apt-get install -y curl), then run this command again. Nothing was installed.' >&2; exit 1; }\n" +
		"f=$(mktemp) || exit 1\n" +
		"trap 'rm -f \"$f\"' EXIT\n" +
		"curl -fsS --proto =https --connect-timeout 10 --max-time 300 --capath " + etc + " --cacert " + etc + "/ca.pem -o \"$f\" '" +
		agentURL + wire.PathInstallScript + "' || { echo 'plumb-install: the download of the install script failed (see the line above). Nothing was installed.' >&2; exit 1; }\n" +
		"[ \"$(tail -n 1 \"$f\")\" = 'main \"$@\"' ] || { echo 'plumb-install: the install script is empty or incomplete. Nothing was installed.' >&2; exit 1; }\n" +
		"cat > " + etc + "/enroll.token <<'PLUMB_TOKEN'\n" + token + "\nPLUMB_TOKEN\n" +
		"PLUMB_URL='" + agentURL + "' sh \"$f\"\n" +
		")\n"
}
