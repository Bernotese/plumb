package repair

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestKindTarget(t *testing.T) {
	if kindTarget != state.KindTarget {
		t.Fatal("kindTarget differs from state.KindTarget")
	}
}

// fakeSSHD is a guest sshd: it accepts only keys that the test lists,
// runs no command, reads stdin and answers with an exit code.
type fakeSSHD struct {
	t        *testing.T
	ln       net.Listener
	hostKey  ssh.Signer
	more     []ssh.Signer // further host keys of other types (T-207)
	mu       sync.Mutex
	allowed  []ssh.PublicKey
	stdin    [][]byte
	requests []string // channel and request types the client asked for
	exit     uint32
	out      string
}

func newFakeSSHD(t *testing.T) *fakeSSHD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, hk, _ := ed25519.GenerateKey(nil)
	signer, _ := ssh.NewSignerFromKey(hk)
	f := &fakeSSHD{t: t, ln: ln, hostKey: signer}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSSHD) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSSHD) newHostKey() {
	_, hk, _ := ed25519.GenerateKey(nil)
	s, _ := ssh.NewSignerFromKey(hk)
	f.mu.Lock()
	f.hostKey = s
	f.mu.Unlock()
}

func (f *fakeSSHD) fp() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ssh.FingerprintSHA256(f.hostKey.PublicKey())
}

func (f *fakeSSHD) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeSSHD) handle(c net.Conn) {
	defer c.Close()
	f.mu.Lock()
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, k := range f.allowed {
			if bytes.Equal(k.Marshal(), key.Marshal()) {
				return nil, nil
			}
		}
		return nil, errors.New("no")
	}}
	cfg.AddHostKey(f.hostKey)
	for _, k := range f.more {
		cfg.AddHostKey(k)
	}
	f.mu.Unlock()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go func() {
		for r := range reqs {
			f.mu.Lock()
			f.requests = append(f.requests, "global:"+r.Type)
			f.mu.Unlock()
			r.Reply(false, nil)
		}
	}()
	for nc := range chans {
		f.mu.Lock()
		f.requests = append(f.requests, "channel:"+nc.ChannelType())
		f.mu.Unlock()
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.Prohibited, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for r := range creqs {
				f.mu.Lock()
				f.requests = append(f.requests, "req:"+r.Type)
				exit, out := f.exit, f.out
				f.mu.Unlock()
				if r.Type != "exec" {
					r.Reply(false, nil)
					continue
				}
				r.Reply(true, nil)
				b, _ := io.ReadAll(ch)
				f.mu.Lock()
				f.stdin = append(f.stdin, b)
				f.mu.Unlock()
				io.WriteString(ch, out)
				var st [4]byte
				binary.BigEndian.PutUint32(st[:], exit)
				ch.SendRequest("exit-status", false, st[:])
				return
			}
		}()
	}
}

type env struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	sec *secret.Store
	ev  *event.Log
	s   *Service
	now time.Time
	// neigh is the controller's neighbour table: address → MAC.
	neigh      map[string]string
	neighAsked []string
	rewatches  int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sec, err := secret.Open(ctx, st, secret.NewKey())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: ctx, st: st, sec: sec, ev: event.New(st), now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e.neigh = map[string]string{}
	e.s, err = New(Config{Store: st, Secrets: sec, Events: e.ev, From: []string{"127.0.0.1"},
		Neigh: func(_ context.Context, ip net.IP) (string, error) {
			e.neighAsked = append(e.neighAsked, ip.String())
			if m, ok := e.neigh[ip.String()]; ok {
				return m, nil
			}
			return "", errors.New("not on-link")
		},
		Rewatch: func(context.Context) { e.rewatches++ },
		Now:     func() time.Time { return e.now }, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

// bind makes Target name with guest id ref and an enrolled agent.
func (e *env) bind(name, ref string) {
	e.t.Helper()
	if err := e.st.PutBinding(e.ctx, store.Binding{Kind: state.KindTarget, Name: name, Ref: ref}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.PutEnrollToken(e.ctx, name, "h-"+ref, e.now.Add(time.Hour)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.Enroll(e.ctx, "h-"+ref, e.now, store.Agent{ID: "a-" + ref, CredentialHash: "x"}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) report(target string, lines []string, port int, keys ...string) {
	e.t.Helper()
	r := wire.RepairReport{Lines: lines, SSHD: wire.RepairSSHD{Status: wire.SSHDOk, Port: port}, HostKeys: keys}
	if err := e.s.OnReport(e.ctx, target, r); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) currentID() string {
	keys, _ := e.st.RepairKeys(e.ctx)
	return keys[0].ID
}

// allow puts the current repair key into the fake sshd.
func (e *env) allow(f *fakeSSHD) {
	keys, _ := e.st.RepairKeys(e.ctx)
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keys[0].PublicKey))
	if err != nil {
		e.t.Fatal(err)
	}
	f.mu.Lock()
	f.allowed = append(f.allowed, pk)
	f.mu.Unlock()
}

func send(e *env, accept string) (int, bool, *bytes.Buffer, error) {
	var out bytes.Buffer
	exit, sent, err := e.s.Send(e.ctx, plan.RepairSend{Target: "web1", StaticIP: "127.0.0.1", AcceptHostKey: accept,
		Bundle: func(w io.Writer) error { _, err := io.WriteString(w, "BUNDLE"); return err }, Output: &out})
	return exit, sent, &out, err
}

// TestRepairSessionPinnedHostKey: with the pinned host key the bundle goes
// on stdin and the exit code comes back. Only a session with one exec is
// asked: no PTY, no env, no forwarding, no subsystem.
func TestRepairSessionPinnedHostKey(t *testing.T) {
	e := newEnv(t)
	f := newFakeSSHD(t)
	e.allow(f)
	e.bind("web1", "vm/101")
	e.report("web1", []string{e.currentID()}, f.port(), f.fp())
	f.exit, f.out = 3, "hello from the guest"
	exit, sent, out, err := send(e, "")
	if err != nil || !sent || exit != 3 || out.String() != "hello from the guest" {
		t.Fatalf("exit %d sent %v err %v out %q", exit, sent, err, out)
	}
	if len(f.stdin) != 1 || string(f.stdin[0]) != "BUNDLE" {
		t.Fatalf("stdin %q", f.stdin)
	}
	if strings.Join(f.requests, " ") != "channel:session req:exec" {
		t.Fatalf("requests %v", f.requests)
	}
}

// TestRepairHostKeyChangedRefused (D-384, no TOFU): a changed host key
// fails the Run before anything is sent; with accept_host_key of the
// approved Plan it pins the key and sends.
func TestRepairHostKeyChangedRefused(t *testing.T) {
	e := newEnv(t)
	f := newFakeSSHD(t)
	e.allow(f)
	e.bind("web1", "vm/101")
	e.report("web1", []string{e.currentID()}, f.port(), f.fp())
	f.newHostKey()
	_, sent, _, err := send(e, "")
	if !errors.Is(err, ErrHostKey) || sent || len(f.stdin) != 0 || len(f.requests) != 0 {
		t.Fatalf("sent %v err %v stdin %d requests %v", sent, err, len(f.stdin), f.requests)
	}
	// Another fingerprint than the guest's: still refused.
	if _, sent, _, err := send(e, "SHA256:"+strings.Repeat("A", 43)); !errors.Is(err, ErrHostKey) || sent {
		t.Fatalf("a wrong accept passed: %v", err)
	}
	newFP := f.fp()
	if _, sent, _, err := send(e, newFP); err != nil || !sent {
		t.Fatalf("accepted key: sent %v err %v", sent, err)
	}
	pins := e.pinsOf("web1", "vm/101")
	if len(pins) != 2 || (pins[0] != newFP && pins[1] != newFP) {
		t.Fatalf("pins %v", pins)
	}
	// Now pinned: no accept needed.
	if _, sent, _, err := send(e, ""); err != nil || !sent {
		t.Fatalf("after the pin: %v", err)
	}
}

// TestRepairPinsFromFirstReportOnly (D-384): the first report after a new
// binding pins; a later report with other keys never changes the pins and
// shows them once in target.hostkeys; a new binding clears the pins.
func TestRepairPinsFromFirstReportOnly(t *testing.T) {
	e := newEnv(t)
	a, b := "SHA256:"+strings.Repeat("A", 43), "SHA256:"+strings.Repeat("B", 43)
	e.bind("web1", "vm/101")
	e.report("web1", nil, 22, a)
	e.report("web1", nil, 22, b)
	e.report("web1", nil, 22, b)
	if pins := e.pinsOf("web1", "vm/101"); len(pins) != 1 || pins[0] != a {
		t.Fatalf("pins %v", pins)
	}
	st, _ := e.s.Status(e.ctx, "web1")
	if len(st.HostKeysSeen) != 1 || st.HostKeysSeen[0] != b {
		t.Fatalf("seen %v", st.HostKeysSeen)
	}
	evs, _ := e.st.EventsAfter(e.ctx, 0, 100)
	n := 0
	for _, ev := range evs {
		if ev.Type == "target.hostkeys" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d target.hostkeys events, want 1", n)
	}
	// A new guest of the Target: the first report pins again.
	if err := e.st.PutBinding(e.ctx, store.Binding{Kind: state.KindTarget, Name: "web1", Ref: "vm/102"}); err != nil {
		t.Fatal(err)
	}
	e.report("web1", nil, 22, b)
	if pins := e.pinsOf("web1", "vm/102"); len(pins) != 1 || pins[0] != b {
		t.Fatalf("pins of the new guest %v", pins)
	}
	if pins := e.pinsOf("web1", "vm/101"); len(pins) != 0 {
		t.Fatalf("old pins stay %v", pins)
	}
}

// TestRepairDialOnlyGuestMAC (review S1 of #298): without a static
// address the Run dials a reported address only when the controller's
// neighbour entry has the MAC of the guest's NIC in Proxmox. A guest that
// reports the address of a LAN host (the router, the NAS) gets nothing
// dialled; a routed address has no entry; IPv6 is not dialled.
func TestRepairDialOnlyGuestMAC(t *testing.T) {
	e := newEnv(t)
	guestMAC := "bc:24:11:00:00:01"
	e.neigh["192.0.2.1"] = "aa:bb:cc:dd:ee:01"  // the router
	e.neigh["192.0.2.20"] = "aa:bb:cc:dd:ee:20" // the NAS
	e.neigh["192.0.2.50"] = "BC:24:11:00:00:01" // the guest itself
	got := e.s.dialAddrs(e.ctx, "", []string{"192.0.2.1/24", "192.0.2.20/24", "198.51.100.7/24", "2001:db8::9/64",
		"127.0.0.1/8", "192.0.2.50/24"}, guestMAC)
	if strings.Join(got, ",") != "192.0.2.50" {
		t.Fatalf("dial %v", got)
	}
	if slices.Contains(e.neighAsked, "2001:db8::9") || slices.Contains(e.neighAsked, "127.0.0.1") {
		t.Fatalf("asked the neighbour table for %v", e.neighAsked)
	}
	if got := e.s.dialAddrs(e.ctx, "", []string{"192.0.2.50/24"}, ""); len(got) != 0 {
		t.Fatalf("without a guest MAC: %v", got)
	}
	if got := e.s.dialAddrs(e.ctx, "10.9.9.9", []string{"192.0.2.50/24"}, guestMAC); len(got) != 1 || got[0] != "10.9.9.9" {
		t.Fatalf("static %v", got)
	}
	many := []string{}
	for i := 100; i < 120; i++ {
		many = append(many, "192.0.2."+strconv.Itoa(i)+"/24")
	}
	e.neighAsked = nil
	e.s.dialAddrs(e.ctx, "", many, guestMAC)
	if len(e.neighAsked) != maxDialAddrs {
		t.Fatalf("%d neighbour checks", len(e.neighAsked))
	}
	// A DHCP guest that reports only the router's address: nothing is
	// dialled, the Run says why.
	e.bind("web1", "vm/101")
	e.report("web1", []string{e.currentID()}, 22, "SHA256:"+strings.Repeat("A", 43))
	dialed := false
	e.s.cfg.Dial = func(context.Context, string) (net.Conn, error) { dialed = true; return nil, errors.New("no") }
	_, sent, err := e.s.Send(e.ctx, plan.RepairSend{Target: "web1", Reported: []string{"192.0.2.1/24"}, GuestMAC: guestMAC,
		Bundle: func(io.Writer) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "no safe address") || sent || dialed {
		t.Fatalf("LAN host: err %v sent %v dialed %v", err, sent, dialed)
	}
}

func TestArpLookup(t *testing.T) {
	f := filepath.Join(t.TempDir(), "arp")
	os.WriteFile(f, []byte("IP address       HW type     Flags       HW address            Mask     Device\n"+
		"192.0.2.50       0x1         0x2         BC:24:11:00:00:01     *        eth0\n"+
		"192.0.2.51       0x1         0x0         00:00:00:00:00:00     *        eth0\n"), 0o644)
	old := arpFile
	arpFile = f
	defer func() { arpFile = old }()
	if m, ok := arpLookup("192.0.2.50"); !ok || m != "bc:24:11:00:00:01" {
		t.Fatalf("%q %v", m, ok)
	}
	if _, ok := arpLookup("192.0.2.51"); ok {
		t.Fatal("an incomplete entry counts")
	}
	if _, ok := arpLookup("192.0.2.5"); ok {
		t.Fatal("a prefix matched")
	}
}

// TestRepairSharedHostKey (review S3 of #298): a host key pinned for
// another Target is never pinned again, never accepted and refused in the
// session, with the name of the other Target.
func TestRepairSharedHostKey(t *testing.T) {
	e := newEnv(t)
	f := newFakeSSHD(t)
	e.allow(f)
	e.bind("db1", "vm/102")
	e.report("db1", []string{e.currentID()}, f.port(), f.fp())
	// web1 reports the same host key (a clone, or a hostile guest).
	e.bind("web1", "vm/101")
	e.report("web1", []string{e.currentID()}, f.port(), f.fp())
	if pins := e.pinsOf("web1", "vm/101"); len(pins) != 0 {
		t.Fatalf("a shared key was pinned: %v", pins)
	}
	st, _ := e.s.Status(e.ctx, "web1")
	if st.State != plan.RepairOff || !contains(st.Reasons, "host-key-shared") {
		t.Fatalf("status %+v", st)
	}
	if err := e.s.CheckAcceptHostKey(e.ctx, "web1", f.fp()); err == nil || !strings.Contains(err.Error(), "Target db1") {
		t.Fatalf("accept: %v", err)
	}
	// The own pinned key is not a new key: nothing to accept (T-207).
	if err := e.s.CheckAcceptHostKey(e.ctx, "db1", f.fp()); err == nil || !strings.Contains(err.Error(), "never reported") {
		t.Fatalf("own key: %v", err)
	}
	// A pin made before (or by hand) that another Target holds too is
	// refused in the session before anything is sent.
	e.st.AddHostKeyPin(e.ctx, "web1", e.guestKey("web1"), f.fp(), "test", e.now)
	_, sent, _, err := send(e, "")
	if !errors.Is(err, ErrHostKey) || !strings.Contains(err.Error(), "Target db1") || sent || len(f.stdin) != 0 {
		t.Fatalf("shared pin: sent %v err %v", sent, err)
	}
}

// TestRepairFromIPOnly (review R4 of #290): serve resolves the agent URL
// host to IP literals; New refuses a name.
func TestRepairFromIPOnly(t *testing.T) {
	if _, err := New(Config{Store: &store.Store{}, Secrets: &secret.Store{}, From: []string{"plumb.lan"}}); err == nil {
		t.Fatal("a host name in From passed")
	}
	got, err := FromAddrs(context.Background(), "plumb.lan", func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::1"), net.IPv4zero}, nil
	})
	if err != nil || strings.Join(got, ",") != "192.0.2.10,2001:db8::1" {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := FromAddrs(context.Background(), "192.0.2.9", nil); len(got) != 1 || got[0] != "192.0.2.9" {
		t.Fatalf("ip %v", got)
	}
	if _, err := FromAddrs(context.Background(), "x", func(context.Context, string) ([]net.IP, error) { return nil, nil }); err == nil {
		t.Fatal("no address passed")
	}
}

// TestRepairKeyRotation (review R2 of #290): the old key stays until
// revoke_old, never by time; previous.targets lists the Targets without
// the new line; 409 cases.
func TestRepairKeyRotation(t *testing.T) {
	e := newEnv(t)
	fpA, fpB := "SHA256:"+strings.Repeat("A", 43), "SHA256:"+strings.Repeat("B", 43)
	old := e.currentID()
	e.bind("web1", "vm/101")
	e.bind("db1", "vm/102")
	e.report("web1", []string{old}, 22, fpA)
	e.report("db1", []string{old}, 22, fpB)
	v, err := e.s.Rotate(e.ctx, false, "user:admin")
	if err != nil || v.Previous == nil || strings.Join(v.Previous.Targets, ",") != "db1,web1" {
		t.Fatalf("rotate: %+v %v", v, err)
	}
	if _, err := e.s.Rotate(e.ctx, false, "user:admin"); !errors.Is(err, store.ErrRotationRunning) {
		t.Fatalf("second rotate: %v", err)
	}
	e.now = e.now.Add(30 * 24 * time.Hour)
	w := e.s.Watch(e.ctx)
	if len(w.Keys) != 2 || w.Keys[1].ID != old {
		t.Fatalf("after 30 days the old key is gone: %+v", w.Keys)
	}
	e.report("web1", []string{e.currentID(), old}, 22, fpA)
	v, _ = e.s.View(e.ctx)
	if strings.Join(v.Previous.Targets, ",") != "db1" {
		t.Fatalf("previous.targets %v", v.Previous.Targets)
	}
	if e.rewatches != 1 {
		t.Fatalf("rotate pushed %d watches, want 1", e.rewatches)
	}
	if v, err = e.s.Rotate(e.ctx, true, "user:admin"); err != nil || v.Previous != nil {
		t.Fatalf("revoke: %+v %v", v, err)
	}
	if e.rewatches != 2 {
		t.Fatalf("revoke pushed %d watches in all, want 2", e.rewatches)
	}
	// web1 still has the revoked line next to the new one: ready, but it
	// counts as on the old key until its agent drops that line.
	st, _ := e.s.Status(e.ctx, "web1")
	if st.State != plan.RepairReady || st.KeyConfirmed || !contains(st.Reasons, "old-key") {
		t.Fatalf("web1 after revoke %+v", st)
	}
	e.report("web1", []string{e.currentID()}, 22, fpA)
	if st, _ := e.s.Status(e.ctx, "web1"); !st.KeyConfirmed || contains(st.Reasons, "old-key") {
		t.Fatalf("web1 after its new report %+v", st)
	}
	if w := e.s.Watch(e.ctx); len(w.Keys) != 1 || w.Keys[0].ID == old {
		t.Fatalf("after revoke %+v", w.Keys)
	}
	if _, err := e.s.Rotate(e.ctx, true, "user:admin"); !errors.Is(err, store.ErrNothingToRevoke) {
		t.Fatalf("revoke twice: %v", err)
	}
	// db1 still has only the old line: off with old-key.
	st, _ = e.s.Status(e.ctx, "db1")
	if st.State != plan.RepairOff || !contains(st.Reasons, "old-key") {
		t.Fatalf("db1 %+v", st)
	}
}

// TestRepairStatusReasons: ready only with a valid line, sshd ok and a pin.
func TestRepairStatusReasons(t *testing.T) {
	e := newEnv(t)
	if st, _ := e.s.Status(e.ctx, "web1"); st != nil {
		t.Fatalf("no guest: %+v", st)
	}
	e.bind("web1", "vm/101")
	if st, _ := e.s.Status(e.ctx, "web1"); st.State != plan.RepairUnknown {
		t.Fatalf("no hello: %+v", st)
	}
	fp := "SHA256:" + strings.Repeat("A", 43)
	e.report("web1", []string{e.currentID()}, 22, fp)
	if st, _ := e.s.Status(e.ctx, "web1"); st.State != plan.RepairReady || !st.KeyConfirmed || *st.Port != 22 {
		t.Fatalf("ready: %+v", st)
	}
	for name, r := range map[string]wire.RepairReport{
		"no-line":      {SSHD: wire.RepairSSHD{Status: wire.SSHDOk, Port: 22}, HostKeys: []string{fp}},
		"no-sshd":      {Lines: []string{e.currentID()}, SSHD: wire.RepairSSHD{Status: wire.SSHDAbsent}, HostKeys: []string{fp}},
		"root-refused": {Lines: []string{e.currentID()}, SSHD: wire.RepairSSHD{Status: wire.SSHDRootRefused, Port: 22}, HostKeys: []string{fp}},
	} {
		if err := e.s.OnReport(e.ctx, "web1", r); err != nil {
			t.Fatal(err)
		}
		if st, _ := e.s.Status(e.ctx, "web1"); st.State != plan.RepairOff || !contains(st.Reasons, name) {
			t.Fatalf("%s: %+v", name, st)
		}
	}
	e.bind("db1", "vm/102")
	e.report("db1", []string{e.currentID()}, 22) // no host keys: no pin
	if st, _ := e.s.Status(e.ctx, "db1"); !contains(st.Reasons, "no-pin") {
		t.Fatalf("no-pin: %+v", st)
	}
}

// TestRepairKeyNeverLeaks: the private key (seed and its base64) is in no
// view, watch, event or report; the store holds it sealed only.
func TestRepairKeyNeverLeaks(t *testing.T) {
	e := newEnv(t)
	keys, _ := e.st.RepairKeys(e.ctx)
	seed, err := e.sec.OpenBlob(secret.PurposeRepairKey, keyName(keys[0].ID), keys[0].Nonce, keys[0].Sealed)
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatalf("open %v", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	needles := [][]byte{seed, []byte(base64.StdEncoding.EncodeToString(seed)), []byte(base64.RawStdEncoding.EncodeToString(seed)),
		[]byte(priv), []byte(base64.StdEncoding.EncodeToString(priv))}
	e.s.Rotate(e.ctx, false, "user:admin")
	e.s.Rotate(e.ctx, true, "user:admin")
	vj, _ := e.s.ViewJSON(e.ctx)
	var hay [][]byte
	hay = append(hay, vj, keys[0].Sealed)
	w := e.s.Watch(e.ctx)
	hay = append(hay, []byte(w.Keys[0].PublicKey))
	evs, _ := e.st.EventsAfter(e.ctx, 0, 100)
	for _, ev := range evs {
		hay = append(hay, ev.Data)
	}
	for _, h := range hay {
		for _, n := range needles {
			if bytes.Contains(h, n) {
				t.Fatal("the private key leaks")
			}
		}
	}
}

// T-205 (review F7 of T-192, D-396): a deleted Target is not in
// previous.targets; a guest whose agent was not unbound is in
// previous.left_behind.
func TestRepairKeyViewDeletedAndLeftBehind(t *testing.T) {
	e := newEnv(t)
	old := e.currentID()
	e.bind("web1", "vm/101")
	e.bind("wiki", "150")
	e.report("web1", []string{old}, 22, "SHA256:"+strings.Repeat("A", 43))
	e.report("wiki", []string{old}, 22, "SHA256:"+strings.Repeat("B", 43))
	if err := e.st.DeleteBinding(e.ctx, state.KindTarget, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Park(e.ctx, store.Parked{Target: "wiki", AgentID: "ag", GuestRef: "150", Node: "pve", Since: e.now}); err != nil {
		t.Fatal(err)
	}
	v, err := e.s.Rotate(e.ctx, false, "user:admin")
	if err != nil || v.Previous == nil {
		t.Fatalf("rotate: %+v %v", v, err)
	}
	if strings.Join(v.Previous.Targets, ",") != "web1" {
		t.Fatalf("previous.targets %v: a deleted Target is listed", v.Previous.Targets)
	}
	if strings.Join(v.Previous.LeftBehind, ",") != "wiki (VMID 150)" {
		t.Fatalf("previous.left_behind %v", v.Previous.LeftBehind)
	}
}

// Review U2 of #309: a forgotten guest stays in previous.left_behind
// until every key it may hold a line of is revoked.
func TestRepairKeyViewForgottenUntilRevoke(t *testing.T) {
	e := newEnv(t)
	old := e.currentID()
	if err := e.st.Park(e.ctx, store.Parked{Target: "wiki", AgentID: "ag", GuestRef: "150", Since: e.now, KeyIDs: []string{old}}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Forget(e.ctx, "wiki"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.st.IsParkedTarget(e.ctx, "wiki"); ok {
		t.Fatal("a forgotten agent is still parked")
	}
	v, err := e.s.Rotate(e.ctx, false, "user:admin")
	if err != nil || strings.Join(v.Previous.LeftBehind, ",") != "wiki (VMID 150, agent forgotten)" {
		t.Fatalf("rotate: %+v %v", v.Previous, err)
	}
	// The new key does not free it; the revoke of the old one does.
	if _, err = e.s.Rotate(e.ctx, true, "user:admin"); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.st.LeftBehind(e.ctx)
	if len(rows) != 0 {
		t.Fatalf("forgotten row after the revoke of its key: %+v", rows)
	}
}

// A forgotten row that still holds a valid key stays after a revoke.
func TestRepairKeyForgottenWithValidKeyStays(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Rotate(e.ctx, false, "user:admin"); err != nil {
		t.Fatal(err)
	}
	cur := e.currentID()
	e.st.Park(e.ctx, store.Parked{Target: "wiki", AgentID: "ag", GuestRef: "150", Since: e.now, KeyIDs: []string{cur}})
	e.st.Forget(e.ctx, "wiki")
	if _, err := e.s.Rotate(e.ctx, true, "user:admin"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.st.LeftBehind(e.ctx); len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
}

// pinsOf returns the pins of target's guest ref (T-209: pins belong to a
// binding generation). For a ref that is not the current one it returns
// the pins of every generation so far, so a test sees leftovers.
func (e *env) pinsOf(target, ref string) []string {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
	if err == nil && b.Ref == ref {
		pins, _ := e.st.HostKeyPins(e.ctx, target, b.GuestKey())
		return pins
	}
	var all []string
	for g := int64(0); g <= b.Gen+10; g++ {
		p, _ := e.st.HostKeyPins(e.ctx, target, fmt.Sprintf("%s@%d", ref, g))
		all = append(all, p...)
	}
	return all
}

func (e *env) guestKey(target string) string {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
	if err != nil {
		e.t.Fatal(err)
	}
	return b.GuestKey()
}

// T-209 (host finding F10): the host case. The adopted guest 9100 is
// deleted as "wiki" and adopted again as "wiki2": its keys are pinned for
// wiki2, and repair is not off for host-key-shared or no-pin. A reused
// name with another guest starts with that guest's first report.
func TestRepairPinsAfterReadopt(t *testing.T) {
	e := newEnv(t)
	fp := "SHA256:" + strings.Repeat("K", 43)
	id := e.currentID()
	e.bind("wiki", "9100")
	e.report("wiki", []string{id}, 22, fp)
	if pins := e.pinsOf("wiki", "9100"); len(pins) != 1 {
		t.Fatalf("first pins %v", pins)
	}
	// The delete step: revoke, then drop the binding.
	if err := e.st.RevokeAgent(e.ctx, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.DeleteBinding(e.ctx, state.KindTarget, "wiki"); err != nil {
		t.Fatal(err)
	}
	e.bind("wiki2", "9100")
	e.report("wiki2", []string{id}, 22, fp)
	if pins := e.pinsOf("wiki2", "9100"); len(pins) != 1 || pins[0] != fp {
		t.Fatalf("pins after re-adopt %v", pins)
	}
	st, err := e.s.Status(e.ctx, "wiki2")
	if err != nil || st == nil {
		t.Fatalf("status %+v %v", st, err)
	}
	for _, r := range st.Reasons {
		if r == "host-key-shared" || r == "no-pin" {
			t.Fatalf("reasons %v", st.Reasons)
		}
	}
	// The name wiki again, now for another guest: its own first report pins.
	other := "SHA256:" + strings.Repeat("O", 43)
	e.bind("wiki", "9200")
	e.report("wiki", []string{id}, 22, other)
	if pins := e.pinsOf("wiki", "9200"); len(pins) != 1 || pins[0] != other {
		t.Fatalf("pins of the reused name %v", pins)
	}
}

// T-217 (ROADMAP row 32): the parked and forgotten guests show in the
// view also when there is no previous key.
func TestRepairKeyViewLeftBehindWithoutPrevious(t *testing.T) {
	e := newEnv(t)
	if err := e.st.Park(e.ctx, store.Parked{Target: "wiki", AgentID: "ag", GuestRef: "150", Since: e.now}); err != nil {
		t.Fatal(err)
	}
	v, err := e.s.View(e.ctx)
	if err != nil || v.Previous != nil || strings.Join(v.LeftBehind, ",") != "wiki (VMID 150)" {
		t.Fatalf("view %+v %v", v, err)
	}
}
