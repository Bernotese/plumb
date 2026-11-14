package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The read-only Proxmox view of GET /settings (D-361). It uses only read
// calls that the start-up check already makes (GET /version and GET
// /access/permissions, D-183, D-184): it never needs or changes a right.
// It holds no secret, no token id and no token name: TokenUser is the
// user part only.

// installRoles are the roles of install.sh (ROLE_NAMES, ROLE_PRIVS and
// role_path, D-081, D-240, D-405). TestInstallRolesMatchInstaller keeps
// both in step. A backup role has its line only with a guest backup
// storage; PlumbBridge has one more line per extra net (installLines).
var installRoles = []struct {
	Role   string
	Privs  []string
	path   func(Config, InstallExtras) string
	backup bool
}{
	{"PlumbGuests", []string{"VM.Allocate", "VM.Audit", "VM.Clone", "VM.Config.CPU", "VM.Config.Cloudinit", "VM.Config.Disk", "VM.Config.HWType", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.PowerMgmt", "Pool.Audit"},
		func(c Config, _ InstallExtras) string { return "/pool/" + c.Pool }, false},
	{"PlumbDisk", []string{"Datastore.AllocateSpace", "Datastore.Audit"}, func(c Config, _ InstallExtras) string { return "/storage/" + c.DiskStorage }, false},
	{"PlumbImages", []string{"Datastore.AllocateTemplate", "Datastore.Audit"}, func(c Config, _ InstallExtras) string { return "/storage/" + c.ImageStorage }, false},
	{"PlumbSnippets", []string{"Datastore.AllocateSpace"}, func(c Config, _ InstallExtras) string { return "/storage/" + c.SnippetStorage }, false},
	{"PlumbBridge", []string{"SDN.Use"}, func(c Config, _ InstallExtras) string { return "/sdn/zones/localnetwork/" + c.Bridge }, false},
	{"PlumbNode", []string{"Sys.AccessNetwork"}, func(c Config, _ InstallExtras) string { return "/nodes/" + c.Node }, false},
	{"PlumbAudit", []string{"Sys.Audit"}, func(Config, InstallExtras) string { return "/" }, false},
	{"PlumbGuestBackupVM", []string{"VM.Backup"}, func(c Config, _ InstallExtras) string { return "/pool/" + c.Pool }, true},
	{"PlumbGuestBackup", []string{"Datastore.AllocateSpace", "Datastore.Audit"},
		func(_ Config, x InstallExtras) string { return "/storage/" + x.GuestBackupStorage }, true},
}

// installLine is one planned ACL line of install.sh (acl_plan).
type installLine struct {
	path, role string
	privs      []string
}

// installLines are the ACL lines of install.sh in the order of acl_plan:
// the base roles, one PlumbBridge line per extra net (D-401), then the two
// guest backup lines (D-405).
func installLines(cfg Config, x InstallExtras) []installLine {
	var out, backup []installLine
	var bridgePrivs []string
	for _, r := range installRoles {
		l := installLine{path: r.path(cfg, x), role: r.Role, privs: r.Privs}
		switch {
		case r.backup:
			if x.GuestBackupStorage != "" {
				backup = append(backup, l)
			}
		default:
			out = append(out, l)
		}
		if r.Role == "PlumbBridge" {
			bridgePrivs = r.Privs
		}
	}
	for _, n := range x.ExtraNets {
		out = append(out, installLine{path: n.ACLPath(), role: "PlumbBridge", privs: bridgePrivs})
	}
	return append(out, backup...)
}

// ACL states of a View.
const (
	ACLOK      = "ok"
	ACLMissing = "missing"
	ACLUnknown = "unknown"
)

// ACLLine is one expected ACL line and whether the token holds all its
// privileges on the path.
type ACLLine struct {
	Path string
	Role string
	OK   bool
}

// View is the read-only Proxmox part of GET /settings.
type View struct {
	Node, Pool                   string
	ImageStorage, SnippetStorage string
	APIHost                      string
	APIPort                      int
	TLSSHA256                    string // leaf certificate of the last good call, "aa:bb:…"
	Version                      string // PVE version of the last good call
	TokenUser                    string // <user>@<realm>, never the token name
	LastOK                       time.Time
	ACL                          string // ACLOK, ACLMissing, ACLUnknown
	ACLError                     string // why ACLUnknown; empty otherwise
	ACLLines                     []ACLLine
	// ExtraNets are the extra nets of the install (D-401); GuestBackup is
	// nil without a guest backup storage (D-405, D-406). Both come from
	// plumb.env; Others in GuestBackup needs the reads.
	ExtraNets   []ExtraNet
	GuestBackup *GuestBackupView
}

// TokenUser returns the user part (user@realm) of a token id
// user@realm!name, or "" when id is not of that form.
func TokenUser(id string) string {
	user, _, ok := strings.Cut(id, "!")
	if !ok || !strings.Contains(user, "@") {
		return ""
	}
	return user
}

// Viewer makes the View with a short cache, so a page that reads the
// settings often does not send a Proxmox call each time.
type Viewer struct {
	d         *Driver
	tokenUser string
	extras    InstallExtras
	ttl       time.Duration
	now       func() time.Time

	mu      sync.Mutex
	cached  View
	at      time.Time // when cached was made; zero: none
	lastOK  time.Time
	version string
	tlsSum  string
}

// ViewTTL is how long a View is reused.
const ViewTTL = 30 * time.Second

// NewViewer returns the viewer of d. tokenUser is TokenUser of the token id;
// x are the install settings of slice 7 (InstallExtrasFromEnv).
func NewViewer(d *Driver, tokenUser string, x InstallExtras) *Viewer {
	return &Viewer{d: d, tokenUser: tokenUser, extras: x, ttl: ViewTTL, now: time.Now}
}

// View returns the view, from the cache when it is younger than the TTL.
// A failed read gives ACLUnknown with a fixed reason; the version, the
// fingerprint and LastOK stay those of the last good call.
func (v *Viewer) View(ctx context.Context) View {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if !v.at.IsZero() && now.Sub(v.at) < v.ttl {
		return v.copyCached()
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg := v.d.cfg
	out := View{Node: cfg.Node, Pool: cfg.Pool, ImageStorage: cfg.ImageStorage, SnippetStorage: cfg.SnippetStorage, TokenUser: v.tokenUser,
		ExtraNets: append([]ExtraNet(nil), v.extras.ExtraNets...)}
	out.APIHost, out.APIPort = hostPort(v.d.c.URL())
	version, sum, err := v.readVersion(ctx)
	if err == nil {
		v.version, v.tlsSum, v.lastOK = version, sum, v.now()
		var perms map[string]map[string]int
		if err = v.d.c.Get(ctx, "/access/permissions", nil, &perms); err == nil {
			v.lastOK = v.now()
			out.ACL, out.ACLLines = checkACL(cfg, v.extras, perms)
			// The storage configs only with a guest backup storage: the
			// encryption state and the second-storage check (M2 of #321).
			if v.extras.GuestBackupStorage != "" {
				var storages []map[string]any
				if err = v.d.c.Get(ctx, "/storage", nil, &storages); err == nil {
					out.GuestBackup = guestBackupView(v.extras, storages, perms)
				}
			}
		}
	}
	if err != nil {
		out.ACL, out.ACLError, out.ACLLines = ACLUnknown, aclReason(err), []ACLLine{}
		// Without the reads the state comes from the install alone, and
		// the other storages are not known: the ACL state says why.
		out.GuestBackup = guestBackupView(v.extras, nil, nil)
	}
	out.Version, out.TLSSHA256, out.LastOK = v.version, v.tlsSum, v.lastOK
	// A request that the caller cut is no answer of Proxmox: no cache.
	if parent.Err() == nil {
		v.cached, v.at = out, now
		return v.copyCached() // the caller gets no pointer into the cache
	}
	return out
}

func (v *Viewer) copyCached() View {
	out := v.cached
	out.ACLLines = append([]ACLLine{}, v.cached.ACLLines...)
	out.ExtraNets = append([]ExtraNet(nil), v.cached.ExtraNets...)
	if g := v.cached.GuestBackup; g != nil {
		c := *g
		c.Others = append([]string{}, g.Others...)
		out.GuestBackup = &c
	}
	return out
}

// readVersion reads GET /version and the SHA-256 of the leaf certificate
// of that connection. It sends the same request as Client.Get.
func (v *Viewer) readVersion(ctx context.Context) (string, string, error) {
	c := v.d.c
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/version", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", c.auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("proxmox: GET /version: %w", err)
	}
	defer resp.Body.Close()
	data, err := c.answer(resp, http.MethodGet, "/version")
	if err != nil {
		return "", "", err
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &ver); err != nil {
		return "", "", fmt.Errorf("proxmox: GET /version: decode: %w", err)
	}
	if len(ver.Version) > 64 {
		ver.Version = ver.Version[:64]
	}
	sum := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		sum = fingerprint(resp.TLS.PeerCertificates[0].Raw)
	}
	return ver.Version, sum, nil
}

// fingerprint is the colon form of the SHA-256 of a DER certificate, as
// the Proxmox UI shows it (lower case).
func fingerprint(der []byte) string {
	s := sha256.Sum256(der)
	h := hex.EncodeToString(s[:])
	parts := make([]string, 0, len(s))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// checkACL compares the permissions answer with the ACL lines of the
// install. A line is ok when every privilege of its role is held on its
// path (hasRight: on the path, or propagated from a parent).
func checkACL(cfg Config, x InstallExtras, perms map[string]map[string]int) (string, []ACLLine) {
	state := ACLOK
	planned := installLines(cfg, x)
	lines := make([]ACLLine, 0, len(planned))
	for _, l := range planned {
		ok := true
		for _, p := range l.privs {
			if !hasRight(perms, l.path, p) {
				ok = false
				break
			}
		}
		if !ok {
			state = ACLMissing
		}
		lines = append(lines, ACLLine{Path: l.path, Role: l.role, OK: ok})
	}
	return state, lines
}

// aclReason is a fixed text for a failed read: no answer body, no URL.
func aclReason(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return fmt.Sprintf("Proxmox answered %d for %s", ae.Status, ae.Path)
	}
	if isTLS(err) {
		return "the TLS certificate of Proxmox is not trusted"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "Proxmox did not answer in time"
	}
	if errors.Is(err, errNotJSON) {
		return "the answer is not a Proxmox API answer"
	}
	return "cannot reach Proxmox"
}

// hostPort splits the API URL; the port is 443 when the URL names none.
func hostPort(raw string) (string, int) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		return host, 443
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return host, 0
	}
	return host, n
}
