package link

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/link/agentinstall"
	"github.com/Bernotese/plumb/internal/wire"
)

// TestRepairTokenKeepsCredential (review R1 of #290): a repair token
// leaves the current credential working; an expired or dropped token
// changes nothing; only the enrollment with the token replaces it.
func TestRepairTokenKeepsCredential(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	old := f.agent("web1")
	hello := wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "1"}}

	// A dropped token (the Run sent nothing): the old credential works,
	// the token is gone.
	rt, err := f.link.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Token) < 43 || !rt.Expires.Equal(f.clock.now().Add(TokenTTL)) {
		t.Fatalf("token %d bytes, expires %v", len(rt.Token), rt.Expires)
	}
	if code, _ := f.do("POST", wire.PathReport, old, hello); code != 204 {
		t.Fatalf("old credential after a repair token: %d", code)
	}
	if err := f.link.DropRepairToken(ctx, "web1", rt); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.enroll(rt.Token); code != 401 {
		t.Fatalf("dropped token enrolled: %d", code)
	}
	if code, _ := f.do("GET", wire.PathPoll, old, nil); code != 204 {
		t.Fatalf("old credential after a drop: %d", code)
	}

	// An expired token: the same.
	rt, err = f.link.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.add(TokenTTL + time.Second)
	if code, _, _ := f.enroll(rt.Token); code != 401 {
		t.Fatalf("expired token enrolled: %d", code)
	}
	if code, _ := f.do("POST", wire.PathReport, old, hello); code != 204 {
		t.Fatalf("old credential after an expired repair: %d", code)
	}

	// A used token: the new credential works, the old one is refused.
	rt, err = f.link.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	code, resp, _ := f.enroll(rt.Token)
	if code != 200 {
		t.Fatalf("enroll with the repair token: %d", code)
	}
	if code, _ := f.do("POST", wire.PathReport, resp.Credential, hello); code != 204 {
		t.Fatalf("new credential: %d", code)
	}
	if code, _ := f.do("GET", wire.PathPoll, old, nil); code != 401 {
		t.Fatalf("old credential after the enrollment: %d, want 401", code)
	}
	// A drop after the use removes nothing and is no error.
	if err := f.link.DropRepairToken(ctx, "web1", rt); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.enroll(rt.Token); code != 401 {
		t.Fatal("a used token enrolled twice")
	}
}

// A drop does not remove a newer token of the target.
func TestDropRepairTokenKeepsNewer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.agent("web1")
	first, err := f.link.NewRepairToken(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	second := f.token("web1")
	if err := f.link.DropRepairToken(ctx, "web1", first); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.enroll(second); code != 200 {
		t.Fatalf("the newer token after a drop: %d", code)
	}
}

func TestInstallScriptRoute(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", wire.PathInstallScript, "", nil)
	if code != 200 || !bytes.Equal(body, agentinstall.Script()) {
		t.Fatalf("install.sh: %d, %d bytes", code, len(body))
	}
	limited := false
	for i := 0; i < 10; i++ {
		if code, _ := f.do("GET", wire.PathInstallScript, "", nil); code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("install.sh is not rate-limited")
	}
}
