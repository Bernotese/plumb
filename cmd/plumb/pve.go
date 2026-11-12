package main

// The Proxmox settings of plumb serve (D-182) and the texts of the
// start-up check (D-183). serve reads the settings from the environment
// only: the unit has EnvironmentFile=/etc/plumb/plumb.env, so the secret
// is never in argv.

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/driver/proxmox"
)

// errConfig is a start failure that a restart cannot repair: exit 78
// (EX_CONFIG). The unit of install.sh has RestartPreventExitStatus=78.
type errConfig struct{ msg string }

func (e *errConfig) Error() string { return e.msg }

func configErr(format string, a ...any) error { return &errConfig{msg: fmt.Sprintf(format, a...)} }

const secretVar = "PLUMB_PVE_SECRET"

// pveStartWait and pveStartPoll shorten the retry of the start-up check in
// tests; zero is the driver default (60 s, 5 s).
var pveStartWait, pveStartPoll time.Duration

// pveVars are the settings that install.sh writes (push_env), in that order.
var pveVars = []string{"PLUMB_PVE_URL", "PLUMB_PVE_TOKEN_ID", secretVar, "PLUMB_PVE_NODE", "PLUMB_PVE_POOL",
	"PLUMB_PVE_DISK", "PLUMB_PVE_IMAGES", "PLUMB_PVE_SNIPPETS", "PLUMB_PVE_SNIPPET", "PLUMB_PVE_BRIDGE",
	"PLUMB_PVE_VMID_RANGE", "PLUMB_PVE_NAMESERVER", "PLUMB_PVE_SEARCHDOMAIN"}

var (
	reName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	reBridge  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
	reTokenID = regexp.MustCompile(`^[^\s=!@]+@[^\s=!@]+![^\s=!@]+$`)
	reRange   = regexp.MustCompile(`^([0-9]{3,9})-([0-9]{3,9})$`)
	reDomain  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// shown is a bad value for a message: quoted (control characters escaped)
// and cut to 100 characters.
func shown(v string) string {
	if utf8.RuneCountInString(v) > 100 {
		v = string([]rune(v)[:100]) + "…"
	}
	return strconv.Quote(v)
}

// pveSettings reads and checks the settings. The error names every missing
// and every bad variable; it never holds the secret.
func pveSettings(getenv func(string) string) (proxmox.ClientConfig, proxmox.Config, error) {
	var cc proxmox.ClientConfig
	var pc proxmox.Config
	var missing, bad []string
	get := func(name string) string {
		v := getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	badf := func(name, form, v string) {
		// Never the value of the secret or of the token id: a token id
		// pasted as user@realm!name=<secret> holds the secret (review S1).
		if name == secretVar || name == "PLUMB_PVE_TOKEN_ID" {
			bad = append(bad, name+" must be "+form)
			return
		}
		bad = append(bad, fmt.Sprintf("%s must be %s, not %s", name, form, shown(v)))
	}
	name := func(n string) string {
		v := get(n)
		if v != "" && !reName.MatchString(v) {
			badf(n, "a Proxmox name (letters, digits, . _ -)", v)
		}
		return v
	}

	if v := get("PLUMB_PVE_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			badf("PLUMB_PVE_URL", "https://host[:port]", v)
		}
		cc.URL = strings.TrimSuffix(v, "/")
	}
	if v := get("PLUMB_PVE_TOKEN_ID"); v != "" && !reTokenID.MatchString(v) {
		badf("PLUMB_PVE_TOKEN_ID", "user@realm!name without white space or =", v)
	} else {
		cc.TokenID = v
	}
	if v := get(secretVar); v != "" && strings.ContainsAny(v, " \t\r\n=") {
		badf(secretVar, "one word without = or white space", v)
	} else {
		cc.Secret = v
	}
	cc.CAFile = getenv("PLUMB_PVE_CA") // optional: empty = system roots (D-076)

	pc.Node = name("PLUMB_PVE_NODE")
	pc.Pool = name("PLUMB_PVE_POOL")
	pc.DiskStorage = name("PLUMB_PVE_DISK")
	pc.ImageStorage = name("PLUMB_PVE_IMAGES")
	pc.SnippetStorage = name("PLUMB_PVE_SNIPPETS")
	pc.Snippet = name("PLUMB_PVE_SNIPPET")
	if v := get("PLUMB_PVE_BRIDGE"); v != "" && !reBridge.MatchString(v) {
		badf("PLUMB_PVE_BRIDGE", "a bridge name like vmbr0", v)
	} else {
		pc.Bridge = v
	}
	if v := get("PLUMB_PVE_VMID_RANGE"); v != "" {
		m := reRange.FindStringSubmatch(v)
		lo, hi := 0, 0
		if m != nil {
			lo, _ = strconv.Atoi(m[1])
			hi, _ = strconv.Atoi(m[2])
		}
		if m == nil || lo < 100 || hi < lo {
			badf("PLUMB_PVE_VMID_RANGE", "<min>-<max> with 100 <= min <= max", v)
		}
		pc.VMIDMin, pc.VMIDMax = lo, hi
	}
	if v := get("PLUMB_PVE_NAMESERVER"); v != "" {
		parts := strings.Split(v, ",")
		ok := len(parts) >= 1 && len(parts) <= 3
		for _, p := range parts {
			if net.ParseIP(p) == nil {
				ok = false
			}
		}
		if !ok {
			badf("PLUMB_PVE_NAMESERVER", "1 to 3 IP addresses, comma-separated", v)
		}
		pc.Nameservers = parts
	}
	if v := get("PLUMB_PVE_SEARCHDOMAIN"); v != "" && (!reDomain.MatchString(v) || len(v) > 253) {
		badf("PLUMB_PVE_SEARCHDOMAIN", "one DNS name", v)
	} else {
		pc.SearchDomain = v
	}

	var msgs []string
	if len(missing) > 0 {
		msgs = append(msgs, "the Proxmox settings are missing: "+strings.Join(missing, ", ")+
			". install.sh writes them to /etc/plumb/plumb.env; use --dev to try Plumb without Proxmox")
	}
	msgs = append(msgs, bad...)
	if len(msgs) > 0 {
		return cc, pc, &errConfig{msg: strings.Join(msgs, "; ")}
	}
	return cc, pc, nil
}

// startError makes the text of a failed start-up check. "No answer" stays
// a plain error (exit 1: systemd starts serve again); every other failure
// is errConfig (exit 78).
func startError(err error, cc proxmox.ClientConfig) error {
	var (
		ue *proxmox.UnreachableError
		ve *proxmox.VersionError
		hp *proxmox.HostProblems
		ae *proxmox.APIError
		ua x509.UnknownAuthorityError
		he x509.HostnameError
		ci x509.CertificateInvalidError
	)
	caPath := cc.CAFile
	if caPath == "" {
		caPath = "the system roots"
	}
	switch {
	case errors.As(err, &ua):
		issuer := "an unknown issuer"
		if ua.Cert != nil {
			issuer = ua.Cert.Issuer.String()
		}
		return configErr("the certificate of %s is from %q; the CA in %s (PLUMB_PVE_CA) did not sign it. If the node uses its own CA, copy /etc/pve/pve-root-ca.pem of the node to %s; for another certificate, put its CA there",
			cc.URL, issuer, caPath, caPath)
	case errors.As(err, &he):
		var names []string
		if he.Certificate != nil {
			names = append(names, he.Certificate.DNSNames...)
			for _, ip := range he.Certificate.IPAddresses {
				names = append(names, ip.String())
			}
		}
		return configErr("the certificate of %s does not name %s; it names %s. Set PLUMB_PVE_URL to one of these names",
			cc.URL, he.Host, strings.Join(names, ", "))
	case errors.As(err, &ci) && (ci.Reason == x509.Expired):
		span := ""
		if ci.Cert != nil {
			span = fmt.Sprintf(" (%s … %s)", ci.Cert.NotBefore.UTC().Format("2006-01-02 15:04 MST"), ci.Cert.NotAfter.UTC().Format("2006-01-02 15:04 MST"))
		}
		return configErr("the certificate of %s is not valid at this time%s; check the clock of the LXC and the certificate of the node", cc.URL, span)
	case errors.As(err, &ae) && ae.Status == 401:
		return configErr("Proxmox refused the API token %s. The secret in the settings is wrong or the token was removed", cc.TokenID)
	case errors.As(err, &ue):
		return fmt.Errorf("cannot reach Proxmox at %s: %v", ue.URL, ue.Err)
	case errors.As(err, &ve):
		return configErr("%s", ve.Error())
	case errors.As(err, &hp):
		var msgs []string
		for _, p := range hp.Problems {
			msgs = append(msgs, problemText(p, hp.Node))
		}
		return configErr("%s", strings.Join(msgs, "; "))
	}
	return configErr("the Proxmox start-up check failed: %v", err)
}

func problemText(p proxmox.HostProblem, node string) string {
	switch p.Kind {
	case proxmox.ProblemPool:
		return fmt.Sprintf("pool %s does not exist, or the token has no Pool.Audit right on /pool/%s", p.ID, p.ID)
	case proxmox.ProblemNode:
		return fmt.Sprintf("node %s does not exist on this host", p.ID)
	case proxmox.ProblemStorageMissing:
		return fmt.Sprintf("storage %s is missing on node %s, is not active, or the token has no right on it", p.ID, node)
	case proxmox.ProblemStorageContent:
		return fmt.Sprintf("storage %s has the content %q; it must have %s (D-081)", p.ID, p.Content, p.Rule)
	case proxmox.ProblemBridge:
		return fmt.Sprintf("the token has no SDN.Use right on bridge %s", p.ID)
	}
	return p.Kind + " " + p.ID
}

// proxmoxDriver reads the settings, builds the client and the driver, and
// runs the start-up check. It logs one line on success (no secret).
func proxmoxDriver(ctx context.Context, getenv func(string) string, log *slog.Logger) (*proxmox.Driver, error) {
	cc, pc, err := pveSettings(getenv)
	// A later child process must not get the secret (D-182).
	os.Unsetenv(secretVar)
	if err != nil {
		return nil, err
	}
	c, err := proxmox.NewClient(cc)
	if err != nil {
		if cc.CAFile != "" {
			return nil, configErr("cannot read the Proxmox CA file %s (PLUMB_PVE_CA): %s", cc.CAFile,
				strings.TrimPrefix(strings.TrimPrefix(err.Error(), "proxmox: "), "CA file: "))
		}
		return nil, configErr("%v", err)
	}
	pc.Client = c
	pc.StartWait, pc.StartPoll = pveStartWait, pveStartPoll
	d, err := proxmox.New(pc)
	if err != nil {
		return nil, configErr("%v", err)
	}
	version, err := d.Check(ctx)
	if err != nil {
		return nil, startError(err, cc)
	}
	log.Info("Proxmox host checked", "url", c.URL(), "node", pc.Node, "version", version, "pool", pc.Pool,
		"vmid_range", fmt.Sprintf("%d-%d", pc.VMIDMin, pc.VMIDMax))
	if err := d.TemplateContentError(); err != nil {
		// An install from before D-229: VM Targets work, LXC Images do not.
		log.Warn("Proxmox image storage without LXC templates", "storage", pc.ImageStorage, "err", err)
	}
	return d, nil
}
