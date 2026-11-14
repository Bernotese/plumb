package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// T-203: a data dir with a URI character opens the file with exactly
// that name, with the pragmas on, and two such dirs never share a DB.
func TestOpenPathEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	names := []string{"seed#0", "seed#1", "q?x", "q?y", "p%41", "p%42", "a b", "grün", "semi;colon&amp=1", "x%3Fy"}
	for i, name := range names {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "plumb.db")
		s, err := Open(ctx, path) // checks foreign_keys and WAL
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if _, err := s.db.ExecContext(ctx, `CREATE TABLE t203 (n INTEGER)`); err != nil {
			t.Fatalf("%q: %v (another dir's DB?)", name, err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO t203 VALUES (?)`, i); err != nil {
			t.Fatal(err)
		}
		var file string
		if err := s.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file); err != nil {
			t.Fatal(err)
		}
		if file != path {
			t.Fatalf("%q: SQLite opened %q, want %q", name, file, path)
		}
		s.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
	}
	// No file was made outside the dirs (for example "seed" for "seed#0").
	entries, _ := os.ReadDir(root)
	if len(entries) != len(names) {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Fatalf("entries %q, want only the %d dirs", got, len(names))
	}
}

func TestFileURI(t *testing.T) {
	for in, want := range map[string]string{
		"/var/lib/plumb/plumb.db": "file:///var/lib/plumb/plumb.db",
		"//x/y.db":                "file:////x/y.db",
		"rel/a b.db":              "file:rel/a%20b.db",
		"/d/seed#0?q%41.db":       "file:///d/seed%230%3Fq%2541.db",
		"/ü.db":                   "file:///%C3%BC.db",
	} {
		if got := fileURI(in); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", in, got, want)
		}
	}
}
