package secret

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

type env struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	db  *sql.DB // raw access, to play the attacker who edits the database
	key Key
	s   *Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "plumb.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); st.Close() })
	k := NewKey()
	s, err := Open(ctx, st, k)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, ctx: ctx, st: st, db: db, key: k, s: s}
}

func (e *env) put(name, value string) Info {
	e.t.Helper()
	i, err := e.s.Put(e.ctx, name, value, "user:admin")
	if err != nil {
		e.t.Fatal(err)
	}
	return i
}

func (e *env) resolve(names ...string) (map[string]Value, error) {
	return e.s.Resolve(e.ctx, names)
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.ExecContext(e.ctx, q, args...); err != nil {
		e.t.Fatal(err)
	}
}

type row struct {
	version   int64
	nonce, ct []byte
}

func (e *env) row(name string) row {
	e.t.Helper()
	var r row
	if err := e.db.QueryRowContext(e.ctx, `SELECT version, nonce, ciphertext FROM secret WHERE name = ?`, name).
		Scan(&r.version, &r.nonce, &r.ct); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func TestRoundTrip(t *testing.T) {
	e := newEnv(t)
	a := e.put("app-db", "hunter2")
	b := e.put("deploy-key", "ключ ✓")
	if a.Version < 1 || b.Version <= a.Version {
		t.Fatalf("versions %d, %d", a.Version, b.Version)
	}
	got, err := e.resolve("app-db", "deploy-key", "app-db")
	if err != nil {
		t.Fatal(err)
	}
	if got["app-db"] != (Value{a.Version, "hunter2"}) || got["deploy-key"] != (Value{b.Version, "ключ ✓"}) {
		t.Fatalf("resolve: %+v", got)
	}
	// Replace: a new version, the new value.
	a2 := e.put("app-db", "new")
	if a2.Version <= b.Version {
		t.Fatalf("version after replace %d", a2.Version)
	}
	got, _ = e.resolve("app-db")
	if got["app-db"].Text != "new" {
		t.Fatalf("replace: %+v", got)
	}
	// The database holds no clear value.
	for _, v := range []string{"hunter2", "new", "ключ ✓"} {
		var n int
		e.db.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM secret WHERE instr(ciphertext, ?) > 0`, []byte(v)).Scan(&n)
		if n != 0 {
			t.Errorf("clear value %q in the database", v)
		}
	}
	list, err := e.s.List(e.ctx)
	if err != nil || len(list) != 2 || list[0].Name != "app-db" || list[1].Name != "deploy-key" || list[0].UsedBy == nil {
		t.Fatalf("list: %+v %v", list, err)
	}
	// Missing names.
	var me *MissingError
	if _, err := e.resolve("app-db", "nope", "also-nope"); !errors.As(err, &me) || strings.Join(me.Names, ",") != "also-nope,nope" {
		t.Fatalf("missing: %v", err)
	}
	// Delete.
	if err := e.s.Delete(e.ctx, "app-db"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Delete(e.ctx, "app-db"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := e.resolve("app-db"); !errors.As(err, &me) {
		t.Fatalf("resolve deleted: %v", err)
	}
}

func TestWrongKey(t *testing.T) {
	e := newEnv(t)
	e.put("app-db", "v")
	if _, err := Open(e.ctx, e.st, NewKey()); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := Open(e.ctx, e.st, e.key); err != nil {
		t.Fatalf("right key: %v", err)
	}
	// Secrets without a key check value: refuse, never write a new check.
	e2 := newEnv(t)
	e2.put("x", "v")
	e2.exec(`DROP TRIGGER key_check_no_delete`)
	e2.exec(`DELETE FROM key_check`)
	if _, err := Open(e2.ctx, e2.st, NewKey()); err == nil || errors.Is(err, ErrWrongKey) {
		t.Fatalf("no key check: %v", err)
	}
	if _, err := e2.st.KeyCheck(e2.ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a key check value was written")
	}
}

// Every edit of a row makes it fail to open.
func TestTamper(t *testing.T) {
	flip := func(b []byte) []byte { c := bytes.Clone(b); c[len(c)-1] ^= 1; return c }
	cases := map[string]func(e *env){
		"ciphertext": func(e *env) { e.exec(`UPDATE secret SET ciphertext = ? WHERE name = 'a'`, flip(e.row("a").ct)) },
		"nonce":      func(e *env) { e.exec(`UPDATE secret SET nonce = ? WHERE name = 'a'`, flip(e.row("a").nonce)) },
		"version":    func(e *env) { e.exec(`UPDATE secret SET version = 999 WHERE name = 'a'`) },
		"name": func(e *env) {
			e.exec(`DELETE FROM secret WHERE name = 'b'`)
			e.exec(`UPDATE secret SET name = 'b' WHERE name = 'a'`)
		},
		"swapped rows": func(e *env) {
			ra, rb := e.row("a"), e.row("b")
			e.exec(`UPDATE secret SET nonce = ?, ciphertext = ? WHERE name = 'a'`, rb.nonce, rb.ct)
			e.exec(`UPDATE secret SET nonce = ?, ciphertext = ? WHERE name = 'b'`, ra.nonce, ra.ct)
		},
		"swapped rows with versions": func(e *env) {
			ra, rb := e.row("a"), e.row("b")
			e.exec(`UPDATE secret SET version = 999999 WHERE name = 'a'`)
			e.exec(`UPDATE secret SET version = ?, nonce = ?, ciphertext = ? WHERE name = 'b'`, ra.version, ra.nonce, ra.ct)
			e.exec(`UPDATE secret SET version = ?, nonce = ?, ciphertext = ? WHERE name = 'a'`, rb.version, rb.nonce, rb.ct)
		},
		// A deleted secret's row put back under a new secret of the same name.
		"replay after delete": func(e *env) {
			old := e.row("a")
			if err := e.s.Delete(e.ctx, "a"); err != nil {
				e.t.Fatal(err)
			}
			e.put("a", "new value")
			e.exec(`UPDATE secret SET nonce = ?, ciphertext = ? WHERE name = 'a'`, old.nonce, old.ct)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.put("a", "value of a")
			e.put("b", "value of b")
			edit(e)
			if _, err := e.resolve("a", "b"); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("tampered row: %v", err)
			}
		})
	}
}

func TestVersionNeverGoesBack(t *testing.T) {
	e := newEnv(t)
	v1 := e.put("a", "x").Version
	if err := e.s.Delete(e.ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if v2 := e.put("a", "x").Version; v2 <= v1 {
		t.Fatalf("version after delete: %d, before %d", v2, v1)
	}
	if _, err := e.db.ExecContext(e.ctx, `UPDATE secret_counter SET value = 0`); err == nil {
		t.Fatal("the counter went back")
	}
	if _, err := e.db.ExecContext(e.ctx, `DELETE FROM secret_counter`); err == nil {
		t.Fatal("the counter was deleted")
	}
}

func TestAdditionalDataUnique(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"a", "a1", "a.1", "ab", "b", strings.Repeat("x", 63)} {
		for _, v := range []int64{1, 2, 10, 11, 256, 1 << 40} {
			k := string(additionalData(name, v))
			if prev, ok := seen[k]; ok {
				t.Fatalf("%s/%d and %s give the same additional data", name, v, prev)
			}
			seen[k] = name
		}
	}
	if !bytes.Contains(additionalData("a", 1), []byte(PurposeSecretStore)) {
		t.Fatal("no purpose")
	}
}

func TestLimits(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"", "-a", "a-", "A", "a_b", "a/b", strings.Repeat("x", 64), "a b"} {
		if _, err := e.s.Put(e.ctx, name, "v", "user:admin"); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, name := range []string{"a", "0", "a.b-c", strings.Repeat("x", 63)} {
		if _, err := e.s.Put(e.ctx, name, "v", "user:admin"); err != nil {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, v := range []string{"", strings.Repeat("x", MaxValueBytes+1), "\xff\xfe", "ok\x80"} {
		if _, err := e.s.Put(e.ctx, "v", v, "user:admin"); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("value of %d bytes: %v", len(v), err)
		}
	}
	max := strings.Repeat("é", MaxValueBytes/2)
	e.put("max", max)
	if got, _ := e.resolve("max"); got["max"].Text != max {
		t.Fatal("max value round trip")
	}
	// Nonces differ for the same value.
	e.put("n1", "same")
	e.put("n2", "same")
	if bytes.Equal(e.row("n1").nonce, e.row("n2").nonce) || len(e.row("n1").nonce) != 12 {
		t.Fatal("nonce reuse or wrong size")
	}
	// Errors never hold the value.
	for _, err := range []error{ErrInvalidValue, ErrCorrupt, ErrWrongKey, ErrInvalidName} {
		if strings.Contains(err.Error(), "same") {
			t.Fatal("value in error")
		}
	}
}

func TestInUse(t *testing.T) {
	e := newEnv(t)
	e.put("a", "x")
	e.s.SetUsage(func(context.Context) (map[string]Usage, error) {
		return map[string]Usage{"a": {Targets: []string{"web2", "web1"}, Plans: []int64{4}}, "ghost": {Targets: []string{"web1"}}}, nil
	})
	var iu *InUseError
	if err := e.s.Delete(e.ctx, "a"); !errors.As(err, &iu) || iu.Name != "a" || len(iu.Plans) != 1 {
		t.Fatalf("in use: %v", err)
	}
	if err := e.s.Delete(e.ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing in-use secret: %v", err)
	}
	list, _ := e.s.List(e.ctx)
	if strings.Join(list[0].UsedBy, ",") != "web1,web2" {
		t.Fatalf("used_by: %+v", list)
	}
}

func TestRecoveryKey(t *testing.T) {
	k := NewKey()
	rec := k.Recovery()
	if groups := strings.Split(rec, "-"); len(groups) != 14 || len(groups[13]) != 4 {
		t.Fatalf("format: %s", rec)
	}
	for _, in := range []string{rec, strings.ToLower(rec), strings.ReplaceAll(rec, "-", " ") + "\n", strings.ReplaceAll(rec, "-", "")} {
		got, err := ParseRecovery(in)
		if err != nil || got != k {
			t.Fatalf("parse %q: %v", in, err)
		}
	}
	// One changed character: bad checksum (or bad base32).
	b := []byte(rec)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	for _, bad := range []string{string(b), rec[:len(rec)-1], rec + "A", "", strings.Repeat("A", 56)} {
		if _, err := ParseRecovery(bad); !errors.Is(err, ErrRecoveryKey) {
			t.Errorf("bad recovery key %q: %v", bad, err)
		} else if bad != "" && strings.Contains(err.Error(), bad) {
			t.Error("error repeats the input")
		}
	}
}

func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	k := NewKey()
	if _, err := LoadKeyFile(path); !errors.Is(err, ErrNoKeyFile) {
		t.Fatalf("missing: %v", err)
	}
	if err := WriteKeyFile(path, k); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 || info.Size() != KeySize {
		t.Fatalf("mode %v size %d", info.Mode(), info.Size())
	}
	if got, err := LoadKeyFile(path); err != nil || got != k {
		t.Fatalf("load: %v", err)
	}
	if err := WriteKeyFile(path, NewKey()); !errors.Is(err, ErrKeyFileExists) {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := LoadKeyFile(path); got != k {
		t.Fatal("the key changed")
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660, 0o644, 0o610} {
		os.Chmod(path, mode)
		if _, err := LoadKeyFile(path); err == nil {
			t.Errorf("mode %v accepted", mode)
		}
	}
	os.Chmod(path, 0o400)
	if _, err := LoadKeyFile(path); err != nil {
		t.Errorf("mode 0400: %v", err)
	}
	// Wrong size, symbolic link, directory.
	short := filepath.Join(dir, "short.key")
	os.WriteFile(short, []byte("abc"), 0o600)
	link := filepath.Join(dir, "link.key")
	os.Symlink(path, link)
	for _, p := range []string{short, link, dir} {
		if _, err := LoadKeyFile(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	// The derived keys differ by purpose and differ from the master key.
	if bytes.Equal(k.derive(PurposeSecretStore), k.derive(PurposeKeyCheck)) || bytes.Equal(k.derive(PurposeKeyCheck), k.b[:]) {
		t.Fatal("purpose keys are not separate")
	}
}
