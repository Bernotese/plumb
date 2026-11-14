package repair

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// hostKeySet is one host key per type, as ssh-keygen -A makes them.
type hostKeySet map[string]ssh.Signer

func newHostKeySet(t *testing.T) hostKeySet {
	t.Helper()
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := hostKeySet{}
	for _, k := range []any{ed, ec, rk} {
		signer, err := ssh.NewSignerFromKey(k)
		if err != nil {
			t.Fatal(err)
		}
		s[signer.PublicKey().Type()] = signer
	}
	return s
}

func (s hostKeySet) fp(typ string) string { return ssh.FingerprintSHA256(s[typ].PublicKey()) }

// serve makes the fake sshd show exactly these keys.
func (s hostKeySet) serve(f *fakeSSHD, types ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostKey, f.more = s[types[0]], nil
	for _, typ := range types[1:] {
		f.more = append(f.more, s[typ])
	}
}

// report sends a repair report with these keys; withTypes false is an
// agent before T-207.
func (s hostKeySet) report(e *env, f *fakeSSHD, withTypes bool, types ...string) {
	e.t.Helper()
	r := wire.RepairReport{Lines: []string{e.currentID()}, SSHD: wire.RepairSSHD{Status: wire.SSHDOk, Port: f.port()}}
	if withTypes {
		r.HostKeyTypes = map[string]string{}
	}
	for _, typ := range types {
		r.HostKeys = append(r.HostKeys, s.fp(typ))
		if withTypes {
			r.HostKeyTypes[s.fp(typ)] = typ
		}
	}
	slices.Sort(r.HostKeys)
	if err := e.s.OnReport(e.ctx, "web1", r); err != nil {
		e.t.Fatal(err)
	}
}

var allTypes = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSA}

// TestRepairAcceptEachHostKeyType (T-207, host finding F9): after new host
// keys of all three types, the state is off with host-key-changed, a plain
// repair is refused before anything is sent, an unknown fingerprint is
// refused, and accepting the key of any one type repairs and pins it. The
// Go client prefers ECDSA, so before T-207 only that accept worked.
func TestRepairAcceptEachHostKeyType(t *testing.T) {
	for _, accept := range allTypes {
		t.Run(accept, func(t *testing.T) {
			e := newEnv(t)
			f := newFakeSSHD(t)
			e.allow(f)
			e.bind("web1", "vm/101")
			first := newHostKeySet(t)
			first.serve(f, allTypes...)
			first.report(e, f, true, allTypes...)
			if st, _ := e.s.Status(e.ctx, "web1"); st.State != plan.RepairReady {
				t.Fatalf("first: %+v", st)
			}
			// ssh-keygen -A after the keys were removed.
			second := newHostKeySet(t)
			second.serve(f, allTypes...)
			second.report(e, f, true, allTypes...)
			st, _ := e.s.Status(e.ctx, "web1")
			if st.State != plan.RepairOff || !contains(st.Reasons, plan.ReasonHostKeyChanged) || !st.KeysChangedOnly || len(st.HostKeysSeen) != 3 {
				t.Fatalf("after new keys: %+v", st)
			}
			if err := e.s.Ready(e.ctx, "web1"); err != nil {
				t.Fatalf("ready for an accept: %v", err)
			}
			if _, sent, _, err := send(e, ""); !errors.Is(err, ErrHostKey) || sent || len(f.stdin) != 0 {
				t.Fatalf("plain repair: sent %v err %v", sent, err)
			}
			if err := e.s.CheckAcceptHostKey(e.ctx, "web1", first.fp(accept)); err == nil {
				t.Fatal("an old, unreported key was accepted")
			}
			if err := e.s.CheckAcceptHostKey(e.ctx, "web1", "SHA256:"+strings.Repeat("A", 43)); err == nil {
				t.Fatal("a fingerprint that the agent never reported was accepted")
			}
			fp := second.fp(accept)
			if err := e.s.CheckAcceptHostKey(e.ctx, "web1", fp); err != nil {
				t.Fatalf("accept %s: %v", accept, err)
			}
			exit, sent, _, err := send(e, fp)
			if err != nil || !sent || exit != 0 || len(f.stdin) != 1 {
				t.Fatalf("repair with %s accepted: exit %d sent %v err %v", accept, exit, sent, err)
			}
			pins, _ := e.st.HostKeyPins(e.ctx, "web1", "vm/101")
			if !slices.Contains(pins, fp) {
				t.Fatalf("the accepted key is not pinned: %v", pins)
			}
			// The two other new keys stay unpinned: a warning, not off.
			st, _ = e.s.Status(e.ctx, "web1")
			if st.State != plan.RepairReady || !contains(st.Reasons, plan.ReasonHostKeyNew) || len(st.HostKeysSeen) != 2 {
				t.Fatalf("after the accept: %+v", st)
			}
			// The next repair passes with the pin alone.
			if _, sent, _, err := send(e, ""); err != nil || !sent {
				t.Fatalf("repair with the new pin: sent %v err %v", sent, err)
			}
		})
	}
}

// TestRepairOffersOnlyPinnedTypes (T-207): the guest shows all three key
// types, but only the key of one type is pinned. The client offers only
// that type, so the pinned key is the one checked, for every type; a pin
// of unknown type (an agent before T-207) is found by trying each type.
func TestRepairOffersOnlyPinnedTypes(t *testing.T) {
	for _, pinned := range allTypes {
		for _, withTypes := range []bool{true, false} {
			name := pinned
			if !withTypes {
				name += "/old-agent"
			}
			t.Run(name, func(t *testing.T) {
				e := newEnv(t)
				f := newFakeSSHD(t)
				e.allow(f)
				e.bind("web1", "vm/101")
				keys := newHostKeySet(t)
				keys.serve(f, allTypes...)
				keys.report(e, f, withTypes, pinned) // only this key is reported, so pinned
				if pins, _ := e.st.HostKeyPins(e.ctx, "web1", "vm/101"); len(pins) != 1 {
					t.Fatalf("pins %v", pins)
				}
				exit, sent, _, err := send(e, "")
				if err != nil || !sent || exit != 0 {
					t.Fatalf("repair with the %s pin: exit %d sent %v err %v", pinned, exit, sent, err)
				}
			})
		}
	}
}

// TestRepairUnpinnedTypeNeverPasses (T-207): the guest has only an
// ed25519 key, the pin is the guest's old ECDSA key: no type passes.
func TestRepairUnpinnedTypeNeverPasses(t *testing.T) {
	e := newEnv(t)
	f := newFakeSSHD(t)
	e.allow(f)
	e.bind("web1", "vm/101")
	old := newHostKeySet(t)
	old.serve(f, allTypes...)
	old.report(e, f, true, ssh.KeyAlgoECDSA256)
	old.serve(f, ssh.KeyAlgoED25519)
	if _, sent, _, err := send(e, ""); !errors.Is(err, ErrHostKey) || sent || len(f.stdin) != 0 {
		t.Fatalf("sent %v err %v", sent, err)
	}
}

func TestHostKeyTypesToTry(t *testing.T) {
	a, b := "SHA256:"+strings.Repeat("A", 43), "SHA256:"+strings.Repeat("B", 43)
	types := map[string]string{a: "ssh-rsa", b: "ssh-ed25519"}
	if got := hostKeyTypesToTry([]string{a}, b, types); !slices.Equal(got, []string{"ssh-ed25519", "ssh-rsa"}) {
		t.Fatalf("known: %v", got)
	}
	if got := hostKeyTypesToTry([]string{a}, "", types); !slices.Equal(got, []string{"ssh-rsa"}) {
		t.Fatalf("one pin: %v", got)
	}
	if got := hostKeyTypesToTry([]string{a}, b, map[string]string{a: "ssh-rsa"}); !slices.Equal(got, wire.HostKeyTypeList) {
		t.Fatalf("unknown type: %v", got)
	}
	if got := hostKeyTypesToTry(nil, "", nil); len(got) != 0 {
		t.Fatalf("nothing: %v", got)
	}
	for _, typ := range wire.HostKeyTypeList {
		if len(hostKeyAlgorithms(typ)) == 0 {
			t.Fatalf("no algorithm for %s", typ)
		}
	}
}
