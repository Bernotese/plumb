package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"github.com/Bernotese/plumb/web"
)

type serveFlags struct {
	dev         bool
	devDemo     bool
	plainHTTP   bool
	driver      string
	data        string
	keyFile     string
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
	fs.BoolVar(&f.devDemo, "dev-demo", false, "with --dev: demo data and a one-time dev login link for screenshots (D-080)")
	fs.BoolVar(&f.plainHTTP, "plain-http", false, "serve the API without TLS (only with --dev)")
	fs.StringVar(&f.driver, "driver", "", "hypervisor driver: proxmox, or fake (only with --dev); default proxmox, with --dev fake")
	fs.StringVar(&f.data, "data", "", "data directory (default /var/lib/plumb; with --dev a new temp directory)")
	fs.StringVar(&f.keyFile, "key-file", "", "master key file (default <data>/key/master.key); made only by plumb key init, with --dev made at start")
	fs.StringVar(&f.listen, "listen", "", "API and UI address (default :8443; with --dev 127.0.0.1:8443)")
	fs.StringVar(&f.agentListen, "agent-listen", "", "agent listener address, always TLS (default :8444; with --dev 127.0.0.1:8444)")
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
	// Dev mode is plain HTTP with a cookie without Secure: it stays on
	// loopback unless the user names another address (review T-011 m3).
	if f.listen == "" {
		f.listen = ":8443"
		if f.dev {
			f.listen = "127.0.0.1:8443"
		}
	}
	if f.agentListen == "" {
		f.agentListen = ":8444"
		if f.dev {
			f.agentListen = "127.0.0.1:8444"
		}
	}
	// D-039: dev mode never touches a real host; without dev, no plain HTTP
	// and no fake driver.
	switch {
	case f.devDemo && !f.dev:
		return f, fmt.Errorf("--dev-demo needs --dev")
	case f.dev && f.keyFile != "":
		// The dev key is a throwaway inside the dev data directory; only
		// plumb key init makes a key anywhere else (D-086).
		return f, fmt.Errorf("--key-file is refused with --dev; the dev key lives in the dev data directory")
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
	if f.data == "" && !f.dev {
		f.data = defaultData
	}
	// Without --dev the key file must exist before anything else happens:
	// a missing key never makes a new one and creates no file (D-086).
	var key secret.Key
	if !f.dev {
		if f.keyFile == "" {
			f.keyFile = defaultKeyFile(f.data)
		}
		var err error
		if key, err = loadServeKey(f.keyFile); err != nil {
			return err
		}
	}
	var drv driver.Driver
	switch f.driver {
	case "fake":
		drv = fake.New()
	default:
		return errors.New("the Proxmox driver is not in this build yet (T-012); use --dev to try Plumb")
	}
	if f.data == "" {
		dir, err := os.MkdirTemp("", "plumb-dev-*")
		if err != nil {
			return err
		}
		f.data = dir
	}
	if err := os.MkdirAll(f.data, 0o700); err != nil {
		return err
	}
	if err := checkMode(f.data, f.dev); err != nil {
		return err
	}
	if f.dev {
		var err error
		if key, err = devKey(f); err != nil {
			return err
		}
	}
	if f.dev && !loopback(f.listen) {
		log.Warn("dev mode listens on a non-loopback address: plain HTTP, cookie without Secure, simulated guests", "listen", f.listen)
	}
	st, err := store.Open(ctx, filepath.Join(f.data, "plumb.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	events := event.New(st)
	secrets, err := secret.Open(ctx, st, key)
	if errors.Is(err, secret.ErrWrongKey) {
		return fmt.Errorf("%w. Use the key file of this database, or write it with plumb key restore", err)
	}
	if err != nil {
		return err
	}
	if f.devDemo {
		if err := seedDemoSecret(ctx, secrets); err != nil {
			return err
		}
	}

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
	// Close what a crash left open. This must run before the agent
	// listener, the API and the guest poll start (review T-010 B1, n3).
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
	// The dev login token (D-080): 256 random bits, new at every start,
	// in memory only. It is printed once below.
	var devToken string
	if f.devDemo {
		// Create the login user before printing its URL, even on a seeded store.
		// "!" is not a password hash; only the dev login route can log it in.
		if _, err := st.CreateUser(ctx, store.User{Name: api.DevUser, PasswordHash: "!"}); err != nil && !errors.Is(err, store.ErrExists) {
			return err
		}
		devToken = randomToken()
	}
	srv := api.New(api.Config{Store: st, Events: events, Plans: eng, Issues: iss, Secrets: secrets, UI: ui, Dev: f.dev, DevLoginToken: devToken, Logger: log})

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
	if devToken != "" {
		fmt.Fprintf(stdout, "dev login: %s://%s/dev/login?token=%s&next=/\n", scheme, ln.Addr(), devToken)
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shut)
	}()
	if f.dev {
		// The simulated agents stop before runServer returns and the store
		// closes (review T-024 m1).
		actx, stop := context.WithCancel(ctx)
		agents := newDevAgents(st, lk, drv.(*fake.Driver), log)
		agentsDone := make(chan struct{})
		go func() { agents.run(actx, events); close(agentsDone) }()
		defer func() { stop(); <-agentsDone }()
		go func() {
			if err := seedDev(actx, st, eng, lk, agents, f.devDemo, log); err != nil && actx.Err() == nil {
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

// loadServeKey reads the production key file. A missing file always stops
// the start, also with no stored secret (D-086).
func loadServeKey(path string) (secret.Key, error) {
	k, err := secret.LoadKeyFile(path)
	if errors.Is(err, secret.ErrNoKeyFile) {
		return k, fmt.Errorf("no master key at %s. New install: run \"plumb key init\" on a terminal. "+
			"Restore from a backup: run \"plumb key restore\" with the recovery key. "+
			"If the key volume is not mounted, mount it first", path)
	}
	return k, err
}

// devKey returns the throwaway key of a dev data directory (D-109). It is
// made at the first start and no recovery key is shown.
func devKey(f serveFlags) (secret.Key, error) {
	path := defaultKeyFile(f.data)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return secret.Key{}, err
	}
	k, err := secret.LoadKeyFile(path)
	if errors.Is(err, secret.ErrNoKeyFile) {
		k = secret.NewKey()
		err = secret.WriteKeyFile(path, k)
	}
	return k, err
}

// demoSecret is the secret of --dev-demo (D-080, D-109).
const demoSecret = "demo-db-password"

func seedDemoSecret(ctx context.Context, secrets *secret.Store) error {
	list, err := secrets.List(ctx)
	if err != nil {
		return err
	}
	for _, i := range list {
		if i.Name == demoSecret {
			return nil
		}
	}
	_, err = secrets.Put(ctx, demoSecret, randomToken(), "system")
	return err
}

// modeFile marks a data directory as dev or production. A dev server never
// opens a production database: the fake driver would change it (review
// T-011 m3).
const modeFile = "mode"

func checkMode(dir string, dev bool) error {
	want := "production"
	if dev {
		want = "dev"
	}
	path := filepath.Join(dir, modeFile)
	got, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(dir, "plumb.db")); err == nil {
			// An old directory without a marker: never assume it is dev.
			if dev {
				return fmt.Errorf("%s has a database but no mode marker; --dev uses only its own data directories", dir)
			}
		}
		return os.WriteFile(path, []byte(want+"\n"), 0o600)
	}
	if err != nil {
		return err
	}
	if have := strings.TrimSpace(string(got)); have != want {
		return fmt.Errorf("%s is a %s data directory; this server runs in %s mode", dir, have, want)
	}
	return nil
}

// loopback reports whether a listen address binds to loopback only.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// randomToken returns 256 random bits as base64url.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
