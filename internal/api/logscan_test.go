package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/proxmox"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"go.yaml.in/yaml/v3"
)

// D-431 (T-243): the controller log holds no secret value. Every env of
// this package writes its whole log (debug level) to e.logs as well, and
// its cleanup fails the test when the log holds a secret value of the
// env. logSecretKinds is the one list of the kinds: the cleanup takes the
// values of every kind, and a full run of the package fails when a kind
// had no value in any test (TestMain, logScanCoverage), so a new kind
// fails until a test makes a value of it. The agent side has its own list
// (internal/agent/logscan_test.go).
//
// Values shorter than logScanMin bytes are not looked for: the log holds
// random ids, and a short needle can occur in them by chance (the T-213
// rule). The tests with short values check them where they are made
// (noValue, TestQASecretValuesAndErrors).
const logScanMin = rawScanMin

type logKind string

const (
	kindUserPassword   logKind = "user password"
	kindOneTime        logKind = "one-time password"
	kindSessionCookie  logKind = "session cookie"
	kindBearer         logKind = "API token bearer"
	kindCredential     logKind = "agent credential"
	kindEnrollToken    logKind = "enrollment token"
	kindRepairToken    logKind = "one-shot repair token"
	kindSecretValue    logKind = "secret store value"
	kindDesired        logKind = "sensitive value of the desired list"
	kindPassphrase     logKind = "export or import passphrase"
	kindRecoveryKey    logKind = "recovery key"
	kindRepairKey      logKind = "repair SSH private key"
	kindCAKey          logKind = "link CA private key"
	kindTLSKey         logKind = "link TLS private key"
	kindProxmoxSecret  logKind = "Proxmox API token secret"
	kindBackupPassword logKind = "backup passphrase"
)

// logSecretKinds: every kind of D-431, with where its values come from.
// elsewhere is set for a kind that no env of this package can make; it
// names the test that stands for it.
var logSecretKinds = []struct {
	kind      logKind
	from      string
	elsewhere string
}{
	{kind: kindUserPassword, from: "e.password and the request fields password, current_password, new_password"},
	{kind: kindOneTime, from: "the response field password (OneTimePassword, shown once)"},
	{kind: kindSessionCookie, from: "the session cookie of every login"},
	{kind: kindBearer, from: "e.token and the response field bearer"},
	{kind: kindCredential, from: "the credential of every test agent"},
	{kind: kindEnrollToken, from: "the fake driver's bootstrap tokens and the install command"},
	{kind: kindRepairToken, from: "every token of plan.Config.Repair.Tokens"},
	{kind: kindSecretValue, from: "secret.Store.All and the request field value of /secrets"},
	{kind: kindDesired, from: "plan.Engine.SecretValues of every target with an agent"},
	{kind: kindPassphrase, from: "the request field passphrase (export, import)"},
	{kind: kindRecoveryKey, from: "e.key.Recovery, with and without its dashes"},
	{kind: kindRepairKey, from: "the opened repair key rows (seed and private key, base64)"},
	{kind: kindCAKey, from: "link/ca.key (each line of the PEM body)"},
	{kind: kindTLSKey, from: "link/agent-link.key (each line of the PEM body)"},
	{kind: kindProxmoxSecret, elsewhere: "TestProxmoxDriverHasNoLogger: the driver logs nothing; " +
		"internal/driver/proxmox leakscan_test.go scans its errors, and ClientConfig redacts itself in a log"},
	{kind: kindBackupPassword, elsewhere: "Plumb has no backup passphrase: a backup is sealed with a key " +
		"derived from the master key (secret.Key.BackupDataKey), whose form for people is the recovery key"},
}

// secretFields are the JSON fields of API bodies that hold a secret. The
// contract test TestLogScanKnowsContractSecrets keeps it complete.
var secretFields = map[string]logKind{
	"password":         kindUserPassword, // request; in a response a one-time password
	"current_password": kindUserPassword,
	"new_password":     kindUserPassword,
	"passphrase":       kindPassphrase,
	"value":            kindSecretValue, // only on /secrets
	"bearer":           kindBearer,
	"command":          kindEnrollToken, // the install command holds a one-time token
}

// logScanSeen counts the values of each kind over the whole package run.
var logScanSeen = struct {
	sync.Mutex
	n map[logKind]int
}{n: map[logKind]int{}}

// note records secret values of the env.
func (e *env) note(kind logKind, values ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.secrets == nil {
		e.secrets = map[logKind][]string{}
	}
	for _, v := range values {
		if v != "" {
			e.secrets[kind] = append(e.secrets[kind], v)
		}
	}
}

// noteBody records the secret fields of a request or response body.
func (e *env) noteBody(path string, body []byte, response bool) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return
	}
	for field, kind := range secretFields {
		v, ok := m[field].(string)
		if !ok || (field == "value" && !strings.Contains(path, "/secrets")) {
			continue
		}
		switch {
		case field == "password" && response:
			kind = kindOneTime
		case field == "command":
			_, after, ok := strings.Cut(v, "<<'PLUMB_TOKEN'\n")
			if !ok {
				continue
			}
			v, _, _ = strings.Cut(after, "\n")
		}
		e.note(kind, v)
	}
}

// pemLines are the lines of the PEM bodies of a key file.
func pemLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for {
		var blk *pem.Block
		if blk, b = pem.Decode(b); blk == nil {
			return out
		}
		enc := base64.StdEncoding.EncodeToString(blk.Bytes)
		for i := 0; i < len(enc); i += 64 {
			out = append(out, enc[i:min(i+64, len(enc))])
		}
	}
}

// logSecrets collects the values of every kind. It runs in the cleanup
// before the store closes.
func (e *env) logSecrets() map[logKind][]string {
	ctx := context.Background()
	e.note(kindUserPassword, e.password)
	for _, b := range e.bearers {
		e.note(kindBearer, b)
	}
	targets := make([]string, 0, len(e.agents))
	for name, a := range e.agents {
		e.note(kindCredential, a.Credential())
		targets = append(targets, name)
	}
	for _, id := range e.drv.Guests() {
		if boot, ok := e.drv.Bootstrap(id); ok {
			e.note(kindEnrollToken, boot.Token)
		}
	}
	if all, err := e.sec.All(ctx); err == nil {
		for _, v := range all {
			e.note(kindSecretValue, v)
		}
	}
	for _, tg := range targets {
		if vs, err := e.eng.SecretValues(ctx, tg); err == nil {
			e.note(kindDesired, vs...)
		}
	}
	rec := e.key.Recovery()
	e.note(kindRecoveryKey, rec, strings.ReplaceAll(rec, "-", ""))
	if rows, err := e.st.RepairKeys(ctx); err == nil {
		for _, r := range rows {
			seed, err := e.sec.OpenBlob(secret.PurposeRepairKey, "repair-key/"+r.ID, r.Nonce, r.Sealed)
			if err != nil {
				continue
			}
			priv := ed25519.NewKeyFromSeed(seed)
			e.note(kindRepairKey, string(seed), base64.StdEncoding.EncodeToString(seed), base64.RawStdEncoding.EncodeToString(seed),
				base64.StdEncoding.EncodeToString(priv))
		}
	}
	e.note(kindCAKey, pemLines(filepath.Join(e.dir, "link", "ca.key"))...)
	e.note(kindTLSKey, pemLines(filepath.Join(e.dir, "link", "agent-link.key"))...)
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[logKind][]string, len(e.secrets))
	for k, vs := range e.secrets {
		out[k] = slices.Clone(vs)
	}
	return out
}

type logScanT interface {
	Helper()
	Errorf(string, ...any)
}

// scanLog fails for every value of logScanMin bytes or more in the log
// and counts the values for the coverage of TestMain.
func scanLog(t logScanT, log []byte, values map[logKind][]string) {
	t.Helper()
	logScanSeen.Lock()
	for k, vs := range values {
		logScanSeen.n[k] += len(vs)
	}
	logScanSeen.Unlock()
	for _, k := range slices.Sorted(func(yield func(logKind) bool) {
		for k := range values {
			if !yield(k) {
				return
			}
		}
	}) {
		for _, v := range values[k] {
			if len(v) >= logScanMin && bytes.Contains(log, []byte(v)) {
				t.Errorf("the controller log holds a value of the secret kind %q (D-431)", k)
				break
			}
		}
	}
}

// logScanCoverage is called by TestMain after a passing full run: every
// kind without elsewhere had a value in some test.
func logScanCoverage() error {
	if f := flag.Lookup("test.run"); f == nil || f.Value.String() != "" {
		return nil // a part of the package: the coverage is not known
	}
	if f := flag.Lookup("test.skip"); f != nil && f.Value.String() != "" {
		return nil
	}
	logScanSeen.Lock()
	defer logScanSeen.Unlock()
	var missing []string
	for _, k := range logSecretKinds {
		if k.elsewhere == "" && logScanSeen.n[k.kind] == 0 {
			missing = append(missing, string(k.kind))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("D-431 log scan: no test made a value of %s", strings.Join(missing, ", "))
	}
	return nil
}

// teeHandler writes every record to both handlers.
type teeHandler struct{ a, b slog.Handler }

func (h teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.a.Enabled(ctx, l) || h.b.Enabled(ctx, l)
}

func (h teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, x := range []slog.Handler{h.a, h.b} {
		if x.Enabled(ctx, r.Level) {
			errs = append(errs, x.Handle(ctx, r.Clone()))
		}
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (h teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return teeHandler{h.a.WithAttrs(as), h.b.WithAttrs(as)}
}
func (h teeHandler) WithGroup(n string) slog.Handler {
	return teeHandler{h.a.WithGroup(n), h.b.WithGroup(n)}
}

// envLogger is the logger of a new env: the test's envLog and the scan
// buffer at debug level.
func envLogger(buf io.Writer) *slog.Logger {
	return slog.New(teeHandler{envLog.Handler(), slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})})
}

// noteTokens records every repair token that the engine makes.
type noteTokens struct {
	plan.RepairTokens
	e *env
}

func (n noteTokens) NewRepairToken(ctx context.Context, target string) (link.RepairToken, error) {
	t, err := n.RepairTokens.NewRepairToken(ctx, target)
	if err == nil {
		n.e.note(kindRepairToken, t.Token)
	}
	return t, err
}

type logScanProbe struct{ errs []string }

func (*logScanProbe) Helper()                     {}
func (p *logScanProbe) Errorf(f string, a ...any) { p.errs = append(p.errs, fmt.Sprintf(f, a...)) }

// The scan finds a value of each kind that a component logs, skips a
// short one, and the env cleanup scans the env's own log.
func TestLogScanFindsPlant(t *testing.T) {
	e := newEnv(t, true)
	values := e.logSecrets()
	for _, k := range []logKind{kindUserPassword, kindRecoveryKey, kindCAKey, kindTLSKey} {
		if len(values[k]) == 0 {
			t.Fatalf("a new env has no value of %s", k)
		}
	}
	for _, k := range []logKind{kindUserPassword, kindRecoveryKey, kindCAKey, kindTLSKey} {
		buf := &lockedBuf{}
		envLogger(buf).Debug("plant", "v", values[k][len(values[k])-1])
		p := &logScanProbe{}
		scanLog(p, []byte(buf.String()), values)
		if len(p.errs) != 1 || !strings.Contains(p.errs[0], string(k)) {
			t.Errorf("planted %s: %v", k, p.errs)
		}
	}
	p := &logScanProbe{}
	scanLog(p, []byte("msg=x v=short"), map[logKind][]string{kindSecretValue: {"short"}})
	if len(p.errs) != 0 {
		t.Errorf("a value under logScanMin: %v", p.errs)
	}
	// The env's components log through the scan buffer.
	e.srv.cfg.Logger.Info("plant", "v", values[kindRecoveryKey][0])
	if !strings.Contains(e.logs.String(), values[kindRecoveryKey][0]) {
		t.Fatal("the API logger does not write to the scan buffer")
	}
	e.logs.Reset() // the cleanup scan would fail this test
}

// secretFields holds every secret field of the contract: a property with
// format password or writeOnly, and the shown-once fields bearer and
// command. A new secret field fails here until the scan knows it.
func TestLogScanKnowsContractSecrets(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{"bearer": true, "command": true}
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name, p := range props {
					pm, _ := p.(map[string]any)
					if pm["format"] == "password" || pm["writeOnly"] == true {
						found[name] = true
					}
				}
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(doc)
	var names []string
	for n := range found {
		names = append(names, n)
		if _, ok := secretFields[n]; !ok {
			t.Errorf("the secret field %q of the contract is not in secretFields", n)
		}
	}
	sort.Strings(names)
	if len(names) < 7 {
		t.Fatalf("only %v found: the walk is broken", names)
	}
	for _, k := range logSecretKinds {
		if (k.from == "") == (k.elsewhere == "") {
			t.Errorf("kind %q needs exactly one of from and elsewhere", k.kind)
		}
	}
}

// The Proxmox token secret cannot reach the log through the driver while
// the driver has no logger. A logger field fails here until the kind
// gets a value in an env and an entry without elsewhere.
func TestProxmoxDriverHasNoLogger(t *testing.T) {
	logger := reflect.TypeFor[*slog.Logger]()
	handler := reflect.TypeFor[slog.Handler]()
	writer := reflect.TypeFor[io.Writer]()
	seen := map[reflect.Type]bool{}
	var check func(reflect.Type, string)
	check = func(ty reflect.Type, path string) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		if ty == logger || ty == handler || ty == writer {
			t.Errorf("%s is a logger: the log scan must make a Proxmox token secret (%s)", path, kindProxmoxSecret)
			return
		}
		switch ty.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			check(ty.Elem(), path)
		case reflect.Struct:
			for i := range ty.NumField() {
				f := ty.Field(i)
				check(f.Type, path+"."+f.Name)
			}
		case reflect.Func:
			// a callback could log; none takes a logger today
			for i := range ty.NumIn() {
				check(ty.In(i), path+"(in)")
			}
		}
	}
	check(reflect.TypeFor[proxmox.Config](), "proxmox.Config")
	check(reflect.TypeFor[proxmox.ClientConfig](), "proxmox.ClientConfig")
	var _ driver.Driver = (*proxmox.Driver)(nil)
}

// Reset empties the buffer.
func (l *lockedBuf) Reset() { l.mu.Lock(); defer l.mu.Unlock(); l.b.Reset() }
