package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

type auditSafeResource struct {
	resource.Resource
	cancel    context.CancelFunc
	commitErr error
	restored  bool
}

func (*auditSafeResource) Prepare(context.Context) error { return nil }
func (s *auditSafeResource) Apply(context.Context, []wire.Change) error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
func (s *auditSafeResource) Commit(context.Context) error { return s.commitErr }
func (s *auditSafeResource) Restore(ctx context.Context) error {
	s.restored = ctx.Err() == nil
	return ctx.Err()
}

func TestSafeApplyFailureRecovery(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok")
	ctl.mu.Lock()
	ctl.creds["audit"] = true
	ctl.mu.Unlock()
	client, err := newClient(Config{URL: ctl.srv.URL, CA: ca.pem}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, why := range []string{"cancelled apply", "commit error"} {
		t.Run(why, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sa := &auditSafeResource{commitErr: errors.New("commit failed")}
			if why == "cancelled apply" {
				sa.cancel = cancel
				sa.commitErr = nil
			}
			a := &Agent{client: client, cred: "audit", state: auditState(t), opt: Options{ConfirmWindow: time.Second}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			it := a.safeApply(ctx, "c1", fwResource(nil), sa, nil)
			if it.Status != wire.StatusFailed || !it.RolledBack || !sa.restored {
				t.Fatalf("restore did not run with a live context: %+v, restored %v", it, sa.restored)
			}
			if _, err := os.Stat(a.state.path(safeApplyMarker)); !os.IsNotExist(err) {
				t.Fatalf("marker after recovery: %v", err)
			}
		})
	}
}

func TestSafeApplyStartupFailures(t *testing.T) {
	for _, failRestore := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown version", true: "restore fails"}[failRestore], func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "etc/plumb-agent"), 0700); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, resource.FirewallBootFile), "table inet plumb\ndelete table inet plumb\n")
			nft := &nftRunner{failRestore: failRestore}
			a := &Agent{state: auditState(t), env: resource.NewEnv(root, nft), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			version := 2
			if failRestore {
				version = 1
			}
			if err := a.writeMarker(safeMarker{Version: version, Type: "firewall", Resource: "firewall:main", Command: "stale"}); err != nil {
				t.Fatal(err)
			}
			if rep := a.restoreAtStart(context.Background()); rep != nil {
				t.Fatalf("unexpected restart result: %+v", rep)
			}
			_, err := os.Stat(a.state.path(safeApplyMarker))
			if failRestore && err != nil || !failRestore && !os.IsNotExist(err) {
				t.Fatalf("marker retention after restore: %v", err)
			}
			if nft.count("nft", "-f") != 1 {
				t.Fatal("startup did not attempt the confirmed state")
			}
		})
	}
}

func TestSafeApplyDefaultWindow(t *testing.T) {
	ca := newCA(t)
	ta := startAgent(t, newController(t, ca, "tok"), ca, "tok")
	if ta.a.opt.ConfirmWindow != wire.SafeApplySeconds*time.Second {
		t.Fatalf("default window: %s", ta.a.opt.ConfirmWindow)
	}
}

func auditState(t *testing.T) stateDir {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return stateDir{dir}
}
