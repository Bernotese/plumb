package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"github.com/Bernotese/plumb/web"
)

type serveFlags struct {
	dev         bool
	plainHTTP   bool
	driver      string
	data        string
	listen      string
	agentListen string
	hosts       []string
}

// errUsage marks a flag error: exit code 2.
var errUsage = errors.New("usage")

func parseServe(args []string, stderr io.Writer) (serveFlags, error) {
	var f serveFlags
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&f.dev, "dev", false, "development mode: fake driver, simulated agents, seeded data, plain HTTP (D-039)")
	fs.BoolVar(&f.plainHTTP, "plain-http", false, "serve the API without TLS (only with --dev)")
	fs.StringVar(&f.driver, "driver", "", "hypervisor driver: proxmox, or fake (only with --dev); default proxmox, with --dev fake")
	fs.StringVar(&f.data, "data", "", "data directory (default /var/lib/plumb; with --dev a new temp directory)")
	fs.StringVar(&f.listen, "listen", ":8443", "API and UI address")
	fs.StringVar(&f.agentListen, "agent-listen", ":8444", "agent listener address (always TLS)")
	hosts := fs.String("hosts", "localhost,127.0.0.1", "comma-separated host names and IPs for the certificates")
	if err := fs.Parse(args); err != nil {
		return f, errUsage
	}
	for _, h := range strings.Split(*hosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			f.hosts = append(f.hosts, h)
		}
	}
	if f.driver == "" {
		f.driver = "proxmox"
		if f.dev {
			f.driver = "fake"
		}
	}
	if f.dev {
		f.plainHTTP = true
	}
	// D-039: dev mode never touches a real host; without dev, no plain HTTP
	// and no fake driver.
	switch {
	case f.dev && f.driver != "fake":
		return f, fmt.Errorf("--dev uses the fake driver only; it never talks to a real Proxmox host")
	case !f.dev && f.plainHTTP:
		return f, fmt.Errorf("plain HTTP is refused without --dev")
	case !f.dev && f.driver == "fake":
		return f, fmt.Errorf("the fake driver is for --dev only")
	case f.driver != "fake" && f.driver != "proxmox":
		return f, fmt.Errorf("unknown driver %q", f.driver)
	}
	return f, nil
}

// serve runs the controller until ctx ends.
func serve(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseServe(args, stderr)
	if errors.Is(err, errUsage) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "plumb serve:", err)
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if err := runServer(ctx, f, stdout, log); err != nil {
		fmt.Fprintln(stderr, "plumb serve:", err)
		return 1
	}
	return 0
}

func runServer(ctx context.Context, f serveFlags, stdout io.Writer, log *slog.Logger) error {
	var drv driver.Driver
	switch f.driver {
	case "fake":
		drv = fake.New()
	default:
		return errors.New("the Proxmox driver is not in this build yet (T-012); use --dev to try Plumb")
	}
	if f.data == "" {
		if !f.dev {
			f.data = "/var/lib/plumb"
		} else {
			dir, err := os.MkdirTemp("", "plumb-dev-*")
			if err != nil {
				return err
			}
			f.data = dir
		}
	}
	if err := os.MkdirAll(f.data, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(f.data, "plumb.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	events := event.New(st)

	var eng *plan.Engine
	var iss *issue.Service
	lk, err := link.New(link.Config{
		Dir: filepath.Join(f.data, "link"), Hosts: f.hosts, Store: st, Events: events, Logger: log,
		AgentBinary: agentBinary(),
		OnReport:    func(ctx context.Context, d link.Delivery) error { return eng.HandleReport(ctx, d) },
		Sensitive:   func(ctx context.Context, t, id string) ([]string, bool, error) { return eng.Sensitive(ctx, t, id) },
	})
	if err != nil {
		return err
	}
	eng, err = plan.New(plan.Config{
		Store: st, Events: events, Agents: lk, Driver: drv, Logger: log,
		OnDrift:    func(ctx context.Context, target string, rep wire.Report) error { return iss.Intake(ctx, target, rep) },
		OnPlanDone: func(ctx context.Context, id int64, ok bool) { iss.PlanDone(ctx, id, ok) },
	})
	if err != nil {
		return err
	}
	defer eng.Close()
	iss = issue.New(st, events, eng)
	// Close what a crash left open before agents connect (review T-010 B1).
	if err := eng.Recover(ctx); err != nil {
		return err
	}
	if password, created, err := api.BootstrapAdmin(ctx, st, api.DefaultPasswordParams); err != nil {
		return err
	} else if created {
		fmt.Fprintf(stdout, "First start. User: admin  Password: %s\nThis password is shown once. The first login must change it.\n", password)
	}
	var ui fs.FS
	if web.Built() {
		ui = web.FS()
	} else {
		log.Warn("the web UI is not built into this binary (run make build)")
	}
	srv := api.New(api.Config{Store: st, Events: events, Plans: eng, Issues: iss, UI: ui, Dev: f.dev, Logger: log})

	errc := make(chan error, 2)
	go func() { errc <- lk.Serve(ctx, f.agentListen) }()
	go eng.PollGuests(ctx, time.Minute)

	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // the event stream clears its own deadline
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	scheme := "https"
	if f.plainHTTP {
		scheme = "http"
	} else {
		certPEM, key, err := lk.CA().Issue(f.hosts, time.Now(), 365*24*time.Hour)
		if err != nil {
			return err
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return err
		}
		pair, err := tls.X509KeyPair(append(certPEM, lk.CA().CertPEM...), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
		if err != nil {
			return err
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})
	}
	fmt.Fprintf(stdout, "Plumb listens on %s://%s (agents: %s, data: %s)\n", scheme, ln.Addr(), f.agentListen, f.data)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shut)
	}()
	if f.dev {
		go func() {
			if err := seedDev(ctx, st, eng, lk, drv.(*fake.Driver), log); err != nil && ctx.Err() == nil {
				log.Error("dev seed", "err", err)
			}
		}()
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		errc <- nil
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return nil
	}
}
