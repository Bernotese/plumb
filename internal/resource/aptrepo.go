package resource

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

// Paths of an apt_repository. Each repository has its own key file, named
// in Signed-By, so its key signs only this repository (D-260). Plumb never
// writes to trusted.gpg or trusted.gpg.d.
const (
	aptSourcesDir = "/etc/apt/sources.list.d/"
	aptKeyringDir = "/etc/apt/keyrings"
)

var (
	aptRepoNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	// The same patterns as the schema; the agent does not trust the wire.
	aptURIRE       = regexp.MustCompile(`^https?://[^\s]+$`)
	aptSuiteRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	aptComponentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	aptArchRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// maxAptKey bounds the inline key (schema: 64 KiB); a public key with its
// signatures is a few KiB.
const maxAptKey = 64 << 10

// aptRepo is an apt source in deb822 form with its own inline key
// (schema/apt_repository.json). The key is text in the Block; the agent
// never downloads a key.
type aptRepo struct {
	base
	a aptRepoAttrs
}

type aptRepoAttrs struct {
	Name          string   `json:"name"`
	URIs          []string `json:"uris"`
	Suites        []string `json:"suites"`
	Components    []string `json:"components"`
	Architectures []string `json:"architectures"`
	Key           string   `json:"key"`
	State         string   `json:"state"`
}

func newAptRepo(r wire.Resource, env Env) (Resource, error) {
	a := &aptRepo{base: base{res: r, env: env}, a: aptRepoAttrs{State: wire.StatePresent}}
	if err := decode(r, "apt_repository", &a.a, func() string { return a.a.Name }); err != nil {
		return nil, err
	}
	if err := a.a.check(); err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	return a, nil
}

// check is the agent's own check of the attributes (the agent does not
// trust the wire). Every value goes into a deb822 field, so no value may
// hold whitespace or a line break.
func (a aptRepoAttrs) check() error {
	if !aptRepoNameRE.MatchString(a.Name) {
		return errors.New("bad repository name")
	}
	if a.State == wire.StateAbsent {
		return nil
	}
	if len(a.URIs) == 0 || len(a.Suites) == 0 {
		return errors.New("uris and suites must not be empty")
	}
	for _, u := range a.URIs {
		if !aptURIRE.MatchString(u) {
			return fmt.Errorf("uri %q is not one http or https URI", clip(u, 100))
		}
	}
	flat := 0
	for _, s := range a.Suites {
		if !aptSuiteRE.MatchString(s) {
			return fmt.Errorf("bad suite %q", clip(s, 100))
		}
		if strings.HasSuffix(s, "/") {
			flat++
		}
	}
	// deb822: an exact path (a suite with "/" at the end) has no
	// components; a distribution needs at least one.
	switch {
	case flat > 0 && (flat != len(a.Suites) || len(a.Components) > 0):
		return errors.New("a suite that ends in \"/\" is an exact path: all suites must be one and components must be empty")
	case flat == 0 && len(a.Components) == 0:
		return errors.New("components must not be empty")
	}
	for _, c := range a.Components {
		if !aptComponentRE.MatchString(c) {
			return fmt.Errorf("bad component %q", clip(c, 100))
		}
	}
	for _, c := range a.Architectures {
		if !aptArchRE.MatchString(c) {
			return fmt.Errorf("bad architecture %q", clip(c, 100))
		}
	}
	return checkPublicKey(a.Key)
}

// checkPublicKey accepts one ASCII-armored OpenPGP public key block and
// nothing else: no secret key, no second block, no text around it. It
// checks the armor and the tag of the first packet, not the signatures:
// apt checks those.
func checkPublicKey(key string) error {
	const begin, end = "-----BEGIN PGP PUBLIC KEY BLOCK-----", "-----END PGP PUBLIC KEY BLOCK-----"
	if len(key) > maxAptKey {
		return fmt.Errorf("key is larger than %d bytes", maxAptKey)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(key, "\r\n", "\n")), "\n")
	if len(lines) < 3 || lines[0] != begin || lines[len(lines)-1] != end {
		return errors.New("key must be one ASCII-armored OpenPGP public key block (" + begin + ")")
	}
	body := lines[1 : len(lines)-1]
	// Armor headers ("Comment: ...") end at the first empty line.
	for i, l := range body {
		if l == "" {
			body = body[i+1:]
			break
		}
		if !strings.Contains(l, ": ") {
			break
		}
	}
	var b64 strings.Builder
	for _, l := range body {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "=") && len(l) == 5 { // CRC-24 line
			continue
		}
		if strings.HasPrefix(l, "-----") {
			return errors.New("key must hold exactly one armored block")
		}
		b64.WriteString(l)
	}
	data, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil || len(data) == 0 {
		return errors.New("key armor is not valid base64")
	}
	// First packet: a public key (tag 6). Old format 0b10TTTTLL, new
	// format 0b11TTTTTT. A secret key (tag 5) is refused.
	var tag byte
	switch h := data[0]; {
	case h&0xC0 == 0xC0:
		tag = h & 0x3F
	case h&0xC0 == 0x80:
		tag = (h >> 2) & 0x0F
	default:
		return errors.New("key is not an OpenPGP packet")
	}
	if tag != 6 {
		return errors.New("key is not an OpenPGP public key")
	}
	return nil
}

func (a *aptRepo) sourcesPath() string { return aptSourcesDir + "plumb-" + a.a.Name + ".sources" }
func (a *aptRepo) keyPath() string     { return aptKeyringDir + "/plumb-" + a.a.Name + ".asc" }

// sources renders the deb822 file. Signed-By always names the key file of
// this repository; there is no field for "Trusted".
func (a *aptRepo) sources() string {
	var b strings.Builder
	b.WriteString("# Managed by Plumb (resource " + a.res.ID + "). Local changes are drift.\n")
	b.WriteString("Types: deb\n")
	b.WriteString("URIs: " + strings.Join(a.a.URIs, " ") + "\n")
	b.WriteString("Suites: " + strings.Join(a.a.Suites, " ") + "\n")
	if len(a.a.Components) > 0 {
		b.WriteString("Components: " + strings.Join(a.a.Components, " ") + "\n")
	}
	if len(a.a.Architectures) > 0 {
		b.WriteString("Architectures: " + strings.Join(a.a.Architectures, " ") + "\n")
	}
	b.WriteString("Signed-By: " + a.keyPath() + "\n")
	return b.String()
}

// Observe reads both files with mode and owner: a key or source file that
// a guest user can write lets that user install packages as root. The
// state is present when either file exists.
func (a *aptRepo) Observe(_ context.Context) (State, error) {
	src, err := readManaged(a.env.Root, a.sourcesPath())
	if err != nil {
		return nil, err
	}
	key, err := readManaged(a.env.Root, a.keyPath())
	if err != nil {
		return nil, err
	}
	if !src.exists && !key.exists {
		return absent(), nil
	}
	st := State{wire.AttrState: wire.StatePresent}
	var perms []string
	for _, f := range []struct {
		attr string
		m    managedFile
	}{{"sources", src}, {"key", key}} {
		if !f.m.exists {
			perms = append(perms, "missing")
			continue
		}
		st[f.attr] = f.m.content
		owner, grp, err := ownerNames(a.env.Root, f.m.uid, f.m.gid)
		if err != nil {
			return nil, err
		}
		perms = append(perms, fmt.Sprintf("%s:%s %04o", owner, grp, f.m.mode))
	}
	// The keyrings directory too: a user who can write it can put another
	// key file in place of this one.
	dir, err := a.keyringPerms()
	if err != nil {
		return nil, err
	}
	perms = append(perms, "dir "+dir)
	st["perms"] = strings.Join(perms, ", ")
	return st, nil
}

// keyringPerms is "owner:group mode" of the keyrings directory, or
// "missing".
func (a *aptRepo) keyringPerms() (string, error) {
	dfd, name, err := openParent(a.env.Root, aptKeyringDir)
	if isNoParent(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	defer unix.Close(dfd)
	st, ok, err := lstatAt(dfd, name, aptKeyringDir)
	if err != nil || !ok {
		return "missing", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("%s is not a directory", aptKeyringDir)
	}
	owner, grp, err := ownerNames(a.env.Root, int(st.Uid), int(st.Gid))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s %04o", owner, grp, st.Mode&0o7777), nil
}

// wantPerms is the observed "perms" of a repository as Plumb writes it.
const wantPerms = "root:root 0644, root:root 0644, dir root:root 0755"

// Diff: every change is reversible. Removing a repository leaves its
// installed packages; they only get no more updates from it.
func (a *aptRepo) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	if a.a.State == wire.StateAbsent {
		if present {
			return a.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskReversible)})
		}
		return nil
	}
	want := State{"sources": a.sources(), "key": a.a.Key, "perms": wantPerms}
	var out []wire.Change
	if !present {
		out = append(out, change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible))
	}
	for _, attr := range []string{"sources", "key", "perms"} {
		if !present {
			out = append(out, change(attr, nil, want[attr], wire.RiskReversible))
		} else if observed[attr] != want[attr] {
			out = append(out, change(attr, observed[attr], want[attr], wire.RiskReversible))
		}
	}
	return a.finish(out)
}

// Apply writes the key before the source (apt never sees a source without
// its key) and removes the source before the key. Then it runs
// "apt-get update", so the next package resource sees the repository. The
// package lock covers the whole apply.
func (a *aptRepo) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	a.env.PackageLock.Lock()
	defer a.env.PackageLock.Unlock()
	if a.a.State == wire.StateAbsent {
		if err := removeManaged(a.env.Root, a.sourcesPath()); err != nil {
			return err
		}
		if err := removeManaged(a.env.Root, a.keyPath()); err != nil {
			return err
		}
	} else {
		if err := mkdirManaged(a.env.Root, aptKeyringDir, 0o755); err != nil {
			return err
		}
		if err := writeManaged(a.env.Root, a.keyPath(), []byte(a.a.Key), 0o644); err != nil {
			return err
		}
		if err := writeManaged(a.env.Root, a.sourcesPath(), []byte(a.sources()), 0o644); err != nil {
			return err
		}
	}
	_, err := a.run(ctx, "apt-get", append(append([]string{}, aptOptions...), "update")...)
	if err != nil && a.a.State != wire.StateAbsent {
		// Without the sources file the next Plan shows the create again
		// and runs "apt-get update" again (a mirror that was down, a
		// fixed suite). The key stays: it is harmless alone.
		if rerr := removeManaged(a.env.Root, a.sourcesPath()); rerr != nil {
			return errors.Join(err, rerr)
		}
		return fmt.Errorf("%w; the sources file was removed again, so the next run tries again", err)
	}
	return err
}

// Watch returns both files.
func (a *aptRepo) Watch() []string {
	return []string{a.path(a.sourcesPath()), a.path(a.keyPath())}
}

// mkdirManaged makes the guest directory p (its parent must exist), or
// takes the existing one, and sets owner root and mode.
func mkdirManaged(root, p string, mode uint32) error {
	dfd, name, err := openParent(root, p)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	_, ok, err := lstatAt(dfd, name, p)
	if err != nil {
		return err
	}
	if !ok {
		if err := unix.Mkdirat(dfd, name, mode); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	fd, _, err := openDir(dfd, name, p)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	uid, gid, err := ownerIDs(root, "root", "root")
	if err != nil {
		return err
	}
	return setOwnerMode(fd, true, uid, gid, mode)
}
