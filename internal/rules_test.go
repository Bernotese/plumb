// Package internal has no code. This test enforces the structure rules of
// docs/ARCHITECTURE.md §2 and §9 for everything below internal/ and cmd/.
package internal

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/Bernotese/plumb"

// mayImport is the "May import" column of ARCHITECTURE §2.
// Every package may also import wire and its own sub-packages.
// A new package or a new arrow needs a change of ARCHITECTURE §2 first.
var mayImport = map[string][]string{
	"api":      {"plan", "issue", "state", "secret", "event", "store"},
	"plan":     {"state", "link", "driver", "secret", "event", "store"},
	"issue":    {"plan", "state", "event", "store"},
	"state":    {"resource", "store"},
	"link":     {"event", "store"},
	"driver":   {},
	"secret":   {"store"},
	"event":    {"store"},
	"store":    {},
	"resource": {},
	"agent":    {"resource"},
	"wire":     {},
}

// cmdMayImport lists what each binary may link. The agent binary must stay
// small and must not contain controller code.
var cmdMayImport = map[string][]string{
	"plumb":       {"api", "plan", "issue", "state", "link", "driver", "secret", "event", "store", "resource", "wire"},
	"plumb-agent": {"agent", "resource", "wire"},
}

// directDeps is the dependency allowlist of D-035. A new direct dependency
// needs a decision line in docs/DECISIONS.md first.
var directDeps = []string{
	"modernc.org/sqlite",
	"go.yaml.in/yaml/",
	"github.com/santhosh-tekuri/jsonschema/",
	"golang.org/x/crypto",
	"golang.org/x/sys",
}

// imports returns the import paths of all non-test Go files below dir,
// grouped by the directory of the file (relative to dir, slash-separated).
func imports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, imp := range file.Imports {
			result[rel] = append(result[rel], strings.Trim(imp.Path.Value, `"`))
		}
		if _, ok := result[rel]; !ok {
			result[rel] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func top(pkg string) string {
	first, _, _ := strings.Cut(pkg, "/")
	return first
}

func allowed(list []string, pkg string) bool {
	for _, a := range list {
		if a == pkg {
			return true
		}
	}
	return false
}

func TestInternalImportRules(t *testing.T) {
	pkgs := imports(t, ".")
	if len(pkgs) < len(mayImport) {
		t.Errorf("found %d packages, want at least the %d of ARCHITECTURE §2", len(pkgs), len(mayImport))
	}
	for pkg, imps := range pkgs {
		self := top(pkg)
		rules, known := mayImport[self]
		if !known {
			t.Errorf("internal/%s: package %q is not in ARCHITECTURE §2", pkg, self)
			continue
		}
		for _, imp := range imps {
			switch {
			case imp == "database/sql" && self != "store":
				t.Errorf("internal/%s imports database/sql; only store may do that", pkg)
			case imp == module+"/web" && self != "api":
				t.Errorf("internal/%s imports the web package; only api may do that", pkg)
			case strings.HasPrefix(imp, module+"/internal/"):
				other := top(strings.TrimPrefix(imp, module+"/internal/"))
				if other != self && other != "wire" && !allowed(rules, other) {
					t.Errorf("internal/%s must not import internal/%s (ARCHITECTURE §2)", pkg, other)
				}
			case strings.HasPrefix(imp, module+"/cmd/"):
				t.Errorf("internal/%s imports a main package: %s", pkg, imp)
			}
		}
	}
	for name := range mayImport {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("package internal/%s of ARCHITECTURE §2 is missing", name)
		}
	}
}

func TestCommandImportRules(t *testing.T) {
	for pkg, imps := range imports(t, "../cmd") {
		rules, known := cmdMayImport[top(pkg)]
		if !known {
			t.Errorf("cmd/%s: unknown binary; ARCHITECTURE §9 has plumb and plumb-agent", pkg)
			continue
		}
		for _, imp := range imps {
			if imp == module+"/web" && top(pkg) == "plumb" {
				continue
			}
			if !strings.HasPrefix(imp, module+"/") {
				continue
			}
			other := top(strings.TrimPrefix(imp, module+"/internal/"))
			if !strings.HasPrefix(imp, module+"/internal/") || !allowed(rules, other) {
				t.Errorf("cmd/%s must not import %s", pkg, imp)
			}
		}
	}
}

func TestDependencyAllowlist(t *testing.T) {
	gomod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(gomod), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "require"))
		fields := strings.Fields(line)
		// A requirement is "<module path> v<version>", without "// indirect".
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") || strings.Contains(line, "// indirect") {
			continue
		}
		ok := false
		for _, dep := range directDeps {
			if fields[0] == dep || strings.HasPrefix(fields[0], dep) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("go.mod: direct dependency %s is not on the allowlist (D-035)", fields[0])
		}
	}
}
