package agentinstall

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// ErrBadCommandInput: the agent URL, the CA or the token could break the
// quoting of the install command (they come from the controller config,
// never from a request, but the function does not trust its caller).
var ErrBadCommandInput = errors.New("agentinstall: bad input for the install command")

// InstallCommand is the root shell command of D-387 that a person runs in
// a guest without the repair line (an adopted guest, an LXC without sshd).
// It fails loudly, never silently (host finding F6 of T-192), and a failed
// try leaves nothing behind:
//
//   - Everything runs in one subshell: umask 077, the checks and every
//     file. An error ends only the subshell, never the person's shell, its
//     exit code is the command's exit code, and the person's umask stays.
//   - Without curl it says "install curl first" and stops; Plumb installs
//     no package. There is no fallback without the CA pin.
//   - The CA goes to a temp dir first; the script is downloaded there (no
//     "curl | sh", which hides curl's exit code and runs an empty script
//     with exit 0), with only that CA (--capath to the temp dir, which has
//     no hashed names, review S1 of T-076) and only https.
//   - A cut or empty download has no `main "$@"` as its last line (the
//     guard of install.sh) and is refused.
//   - Only then are /etc/plumb-agent/ca.pem and the one-time token written.
//     A failed try writes nothing in /etc (a failure inside install.sh
//     removes both again) and the temp dir goes; the token
//     is used only at enrollment, so the same command can run again within
//     its life. It is never on a command line or in the output.
//
// Every refusal is one line on stderr that starts with "plumb-install:",
// the prefix of install.sh too.
func InstallCommand(ca, agentURL, token string) (string, error) {
	u, err := url.Parse(agentURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil ||
		strings.ContainsAny(agentURL, "'\"\\ \t\r\n$`;&|<>(){}") {
		return "", ErrBadCommandInput
	}
	if token == "" || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return "", ErrBadCommandInput
	}
	ca = strings.TrimRight(ca, "\n") + "\n"
	if !strings.HasPrefix(ca, "-----BEGIN CERTIFICATE-----\n") || strings.Contains(ca, "PLUMB_CA") {
		return "", ErrBadCommandInput
	}
	const etc = "/etc/plumb-agent"
	return "(\n" +
		"umask 077\n" +
		"command -v curl >/dev/null 2>&1 || { echo 'plumb-install: curl is missing in this guest. Install it first (apt-get install -y curl), then run this command again. Nothing was installed.' >&2; exit 1; }\n" +
		"d=$(mktemp -d) || exit 1\n" +
		"trap 'rm -rf \"$d\"' EXIT\n" +
		"cat > \"$d/ca.pem\" <<'PLUMB_CA'\n" + ca + "PLUMB_CA\n" +
		"curl -fsS --proto =https --connect-timeout 10 --max-time 300 --capath \"$d\" --cacert \"$d/ca.pem\" -o \"$d/install.sh\" '" +
		agentURL + wire.PathInstallScript + "' || { echo 'plumb-install: the download of the install script failed (see the line above). Nothing was installed.' >&2; exit 1; }\n" +
		"[ \"$(tail -n 1 \"$d/install.sh\")\" = 'main \"$@\"' ] || { echo 'plumb-install: the install script is empty or incomplete. Nothing was installed.' >&2; exit 1; }\n" +
		"mkdir -p " + etc + " && cp \"$d/ca.pem\" " + etc + "/ca.pem || { echo 'plumb-install: cannot write " + etc + ". Nothing was installed.' >&2; exit 1; }\n" +
		"cat > " + etc + "/enroll.token <<'PLUMB_TOKEN'\n" + token + "\nPLUMB_TOKEN\n" +
		// install.sh removes the token itself when it fails (T-190 F1);
		// the CA goes too, so a failed run leaves no file of this command.
		"PLUMB_URL='" + agentURL + "' sh \"$d/install.sh\" || { r=$?; rm -f " + etc + "/ca.pem; rmdir " + etc + " 2>/dev/null; exit $r; }\n" +
		")\n", nil
}
