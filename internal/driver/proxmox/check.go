package proxmox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The start-up check of the host (D-183). It uses only rights of D-081:
// a check that needs a new right is dropped, not granted. Check returns
// typed errors; cmd/plumb makes the texts and the exit codes.

// UnreachableError: /version did not answer within the start wait (a dial
// error, a timeout, or 5xx/595 from pveproxy). A restart can fix it.
type UnreachableError struct {
	URL string
	Err error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("cannot reach Proxmox at %s: %v", e.URL, e.Err)
}
func (e *UnreachableError) Unwrap() error { return e.Err }

// VersionError: the Proxmox VE version is too old, or not readable.
type VersionError struct {
	Version  string
	Readable bool
}

func (e *VersionError) Error() string {
	if !e.Readable {
		return fmt.Sprintf("cannot read the Proxmox VE version from %q", e.Version)
	}
	return fmt.Sprintf("Plumb needs Proxmox VE 9.1 or newer; this host has %s", e.Version)
}

// Problem kinds of HostProblems.
const (
	ProblemPool           = "pool"
	ProblemNode           = "node"
	ProblemStorageMissing = "storage-missing"
	ProblemStorageContent = "storage-content"
	ProblemBridge         = "bridge"
)

// HostProblem is one finding of steps 2 to 4.
type HostProblem struct {
	Kind    string
	ID      string // pool, node, storage or bridge
	Content string // storage-content: the content list as it is
	Rule    string // storage-content: what it must be
}

// HostProblems holds every finding of steps 2 to 4 in one error.
type HostProblems struct {
	Node     string
	Problems []HostProblem
}

func (e *HostProblems) Error() string {
	var parts []string
	for _, p := range e.Problems {
		parts = append(parts, p.Kind+" "+p.ID)
	}
	return "proxmox host check: " + strings.Join(parts, ", ")
}

// Check proves at the start that the host fits the settings:
//  1. GET /version: TLS, the token, Proxmox VE ≥ 9.1. Only this step is
//     tried again, and only for a dial error, a timeout or 5xx/595 (the
//     LXC can start before pveproxy): every StartPoll for StartWait.
//  2. GET /pools?poolid=: the pool is in the answer.
//  3. GET /nodes/<node>/storage: the three storages are enabled and
//     active, and their content obeys D-081.
//  4. GET /access/permissions: SDN.Use on the bridge (only when the
//     answer can be read; it needs no right).
//
// It returns the version on success.
func (d *Driver) Check(ctx context.Context) (string, error) {
	version, err := d.checkVersion(ctx)
	if err != nil {
		return "", err
	}
	hp := &HostProblems{Node: d.cfg.Node}
	if err := d.checkPool(ctx, hp); err != nil {
		return "", err
	}
	if err := d.checkStorages(ctx, hp); err != nil {
		return "", err
	}
	d.checkBridgeRight(ctx, hp)
	if len(hp.Problems) > 0 {
		return "", hp
	}
	return version, nil
}

func (d *Driver) checkVersion(ctx context.Context) (string, error) {
	wait, poll := d.cfg.StartWait, d.cfg.StartPoll
	if wait == 0 {
		wait = 60 * time.Second
	}
	if poll == 0 {
		poll = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	var v struct {
		Version string `json:"version"`
	}
	for {
		err := d.c.Get(ctx, "/version", nil, &v)
		if err == nil {
			break
		}
		if !retryable(err) {
			return "", err
		}
		if !time.Now().Add(poll).Before(deadline) {
			return "", &UnreachableError{URL: d.c.URL(), Err: err}
		}
		select {
		case <-ctx.Done():
			return "", &UnreachableError{URL: d.c.URL(), Err: ctx.Err()}
		case <-time.After(poll):
		}
	}
	major, minor, ok := parseVersion(v.Version)
	if !ok {
		return "", &VersionError{Version: v.Version}
	}
	if major < 9 || (major == 9 && minor < 1) {
		return "", &VersionError{Version: v.Version, Readable: true}
	}
	return v.Version, nil
}

// retryable: a failure that a running pveproxy will not give.
func retryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500 // 5xx, 595 "no connection"
	}
	if isTLS(err) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true // dial error, timeout
	}
	var oe *net.OpError
	return errors.As(err, &oe)
}

// isTLS reports a certificate or TLS failure: never tried again.
func isTLS(err error) bool {
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	var rh tls.RecordHeaderError
	var ve *tls.CertificateVerificationError
	return errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) || errors.As(err, &rh) || errors.As(err, &ve)
}

// parseVersion reads "9.2.26" (or "9.2"); the compare is numeric.
func parseVersion(s string) (int, int, bool) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || major < 0 || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

func (d *Driver) checkPool(ctx context.Context, hp *HostProblems) error {
	var pools []struct {
		PoolID string `json:"poolid"`
	}
	err := d.c.Get(ctx, "/pools", url.Values{"poolid": {d.cfg.Pool}}, &pools)
	var ae *APIError
	switch {
	case errors.As(err, &ae):
		// "does not exist", or 403 without Pool.Audit: the pool text.
		hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemPool, ID: d.cfg.Pool})
		return nil
	case err != nil:
		return err
	}
	for _, p := range pools {
		if p.PoolID == d.cfg.Pool {
			return nil
		}
	}
	hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemPool, ID: d.cfg.Pool})
	return nil
}

func (d *Driver) checkStorages(ctx context.Context, hp *HostProblems) error {
	var list []struct {
		Storage string `json:"storage"`
		Content string `json:"content"`
		Enabled *int   `json:"enabled"`
		Active  *int   `json:"active"`
	}
	err := d.c.Get(ctx, d.node()+"/storage", nil, &list)
	var ae *APIError
	switch {
	case errors.As(err, &ae):
		// 5xx/595 for a node name that does not exist: the node text,
		// not "cannot reach" (never tried again).
		hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemNode, ID: d.cfg.Node})
		return nil
	case err != nil:
		return err
	}
	want := []struct {
		id   string
		ok   func(set map[string]bool) bool
		rule string
	}{
		{d.cfg.DiskStorage, func(c map[string]bool) bool { return c["images"] && !c["snippets"] }, `"images" and not "snippets"`},
		{d.cfg.ImageStorage, func(c map[string]bool) bool { return len(c) == 1 && c["import"] }, `exactly "import"`},
		{d.cfg.SnippetStorage, func(c map[string]bool) bool { return len(c) == 1 && c["snippets"] }, `exactly "snippets"`},
	}
	for _, w := range want {
		found := false
		for _, s := range list {
			if s.Storage != w.id {
				continue
			}
			found = true
			if (s.Enabled != nil && *s.Enabled == 0) || s.Active == nil || *s.Active != 1 {
				hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemStorageMissing, ID: w.id})
				break
			}
			set := map[string]bool{}
			for _, c := range strings.Split(s.Content, ",") {
				if c = strings.TrimSpace(c); c != "" {
					set[c] = true
				}
			}
			if !w.ok(set) {
				hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemStorageContent, ID: w.id, Content: s.Content, Rule: w.rule})
			}
			break
		}
		if !found {
			hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemStorageMissing, ID: w.id})
		}
	}
	return nil
}

// checkBridgeRight proves SDN.Use on the bridge when the token can read its
// rights (D-184). An answer that cannot be read is skipped: the check never
// needs a new right.
func (d *Driver) checkBridgeRight(ctx context.Context, hp *HostProblems) {
	var perms map[string]map[string]int
	if err := d.c.Get(ctx, "/access/permissions", nil, &perms); err != nil || len(perms) == 0 {
		return
	}
	if perms["/sdn/zones/localnetwork/"+d.cfg.Bridge]["SDN.Use"] != 1 {
		hp.Problems = append(hp.Problems, HostProblem{Kind: ProblemBridge, ID: d.cfg.Bridge})
	}
}

// URL is the API base of the client, for the start line and the texts.
func (c *Client) URL() string { return strings.TrimSuffix(c.base, "/api2/json") }
