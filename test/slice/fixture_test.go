package slice_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/agent"
	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type runner struct{ restarts atomic.Int32 }

func (r *runner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name + " " + strings.Join(args, " ") {
	case "dpkg-query -W -f=${db:Status-Abbrev}|${Version}\n -- nginx":
		return []byte("ii |1.0\n"), nil
	case "systemctl show --property=LoadState,ActiveState,UnitFileState -- nginx.service":
		return []byte("LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n"), nil
	case "systemctl restart -- nginx.service":
		r.restarts.Add(1)
		return nil, nil
	case "systemctl daemon-reload":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected guest command: %s %v", name, args)
	}
}

// Only controller wiring, token provisioning, and fake guest bootstrap bypass HTTP.
// Accounts and the package/service are preinstalled; file and key apply are real.
func fixture(t *testing.T) (*httptest.Server, string, *runner, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir, root := t.TempDir(), t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "plumb.db"))
	must(t, err)
	ev, drv := event.New(st), fake.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var eng *plan.Engine
	var issues *issue.Service
	lnk, err := link.New(link.Config{Dir: filepath.Join(dir, "link"), Store: st, Events: ev, Logger: log,
		OnReport: func(ctx context.Context, d link.Delivery) error { return eng.HandleReport(ctx, d) },
		Sensitive: func(ctx context.Context, target, id string) ([]string, bool, error) {
			return eng.Sensitive(ctx, target, id)
		},
	})
	must(t, err)
	eng, err = plan.New(plan.Config{Store: st, Events: ev, Agents: lnk, Driver: drv, Logger: log,
		OnDrift: func(ctx context.Context, target string, rep wire.Report) error {
			return issues.Intake(ctx, target, rep)
		},
		OnPlanDone:    func(ctx context.Context, id int64, ok bool) { issues.PlanDone(ctx, id, ok) },
		EnrollTimeout: 10 * time.Second, ObserveTimeout: 5 * time.Second, ApplyTimeout: 5 * time.Second,
	})
	must(t, err)
	issues = issue.New(st, ev, eng)
	bearer, _, err := api.CreateToken(ctx, st, "slice", api.TierApply, wire.RiskDisruptive, "user:admin")
	must(t, err)
	srv := httptest.NewServer(api.New(api.Config{Store: st, Events: ev, Plans: eng, Issues: issues, Logger: log}).Handler())
	tls := httptest.NewUnstartedServer(lnk.Handler())
	tls.TLS = lnk.TLSConfig()
	tls.StartTLS()
	t.Cleanup(func() { cancel(); eng.Close(); tls.Close(); srv.Close(); must(t, st.Close()) })
	write(t, root, "etc/passwd", fmt.Sprintf("root:x:%d:%d:root:/root:/bin/bash\ndeploy:x:%d:%d::/home/deploy:/bin/bash\n", os.Getuid(), os.Getgid(), max(os.Getuid(), 1), os.Getgid()))
	write(t, root, "etc/group", fmt.Sprintf("root:x:%d:\n", os.Getgid()))
	must(t, os.MkdirAll(filepath.Join(root, "home/deploy"), 0755))
	must(t, os.MkdirAll(filepath.Join(root, "etc/nginx/sites-available"), 0755))
	r := &runner{}
	done := make(chan error, 1)
	go func() {
		// The fake driver's public bootstrap accessor supplies the one-time guest token.
		for {
			binding, _ := st.GetBinding(ctx, "Target", "web1")
			boot, ok := drv.Bootstrap(driver.GuestID(binding.Ref))
			if ok && boot.Token != "" {
				if err := os.WriteFile(filepath.Join(root, "token"), []byte(boot.Token), 0600); err != nil {
					done <- err
					return
				}
				a, err := agent.New(agent.Options{Runner: r, Root: root, Hostname: "web1", Version: "slice-test", Log: log,
					Config:   agent.Config{URL: tls.URL, CA: string(lnk.CA().CertPEM), TokenFile: filepath.Join(root, "token"), StateDir: filepath.Join(root, "state"), FullCheck: agent.Duration(time.Hour)},
					PollWait: 1, Debounce: 20 * time.Millisecond, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond})
				if err != nil {
					done <- err
					return
				}
				done <- a.Run(ctx)
				return
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	return srv, bearer, r, root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func write(t *testing.T, root, path, value string) {
	t.Helper()
	p := filepath.Join(root, path)
	must(t, os.MkdirAll(filepath.Dir(p), 0755))
	must(t, os.WriteFile(p, []byte(value), 0644))
}
