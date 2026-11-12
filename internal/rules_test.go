// Package internal has no code. This test enforces the structure rules of
// docs/ARCHITECTURE.md §2 and §9 for everything below internal/ and cmd/.
package internal

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/Bernotese/plumb"

// mayImport is the "May import" column of ARCHITECTURE §2, copied 1:1.
// (api may import store: sessions and API tokens live there.)
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
	"github.com/klauspost/compress", // zstd decoder only, internal/driver/proxmox/ctpack (D-225)
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
			if imp == "database/sql" || strings.HasPrefix(imp, "modernc.org/") {
				t.Errorf("cmd/%s imports %s; only internal/store may use the database", pkg, imp)
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

// The agent binary runs as root in every guest. It must stay small: no
// controller code and no database, also not through an indirect import.
func TestAgentBinaryDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../cmd/plumb-agent").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	allowedInternal := cmdMayImport["plumb-agent"]
	deps := strings.Fields(string(out))
	if len(deps) < 3 {
		t.Fatalf("go list -deps returned too little: %q", out)
	}
	for _, dep := range deps {
		switch {
		case dep == "database/sql" || strings.HasPrefix(dep, "modernc.org/"):
			t.Errorf("plumb-agent links %s", dep)
		case dep == module+"/web":
			t.Errorf("plumb-agent links the web UI")
		case strings.HasPrefix(dep, module+"/internal/"):
			if pkg := top(strings.TrimPrefix(dep, module+"/internal/")); !allowed(allowedInternal, pkg) {
				t.Errorf("plumb-agent links internal/%s", pkg)
			}
		}
	}
}

// Each schema file is self-contained, so the OpenAPI file can refer to it
// (D-043). The price is that some patterns are copied. This test keeps the
// copies equal: every group must hold one and the same value.
func TestSharedSchemaPatterns(t *testing.T) {
	groups := map[string][]string{
		"document name": {
			"state/schema/image.json#/properties/name/pattern",
			"state/schema/block.json#/properties/name/pattern",
			"state/schema/target.json#/properties/name/pattern",
			"state/schema/target.json#/properties/spec/properties/image/pattern",
			"state/schema/target.json#/properties/spec/properties/blocks/items/properties/use/pattern",
			"state/schema/target.json#/properties/spec/properties/blocks/items/properties/id/pattern",
		},
		"unix user or group name": {
			"resource/schema/user.json#/properties/name/pattern",
			"resource/schema/user.json#/properties/groups/items/pattern",
			"resource/schema/file.json#/properties/owner/pattern",
			"resource/schema/file.json#/properties/group/pattern",
			"resource/schema/directory.json#/properties/owner/pattern",
			"resource/schema/directory.json#/properties/group/pattern",
			"state/schema/target.json#/properties/spec/properties/guest/properties/user/pattern",
		},
		"ssh public key": {
			"resource/schema/user.json#/properties/ssh_keys/items/pattern",
			"state/schema/target.json#/properties/spec/properties/guest/properties/ssh_keys/items/pattern",
		},
		"absolute path": {
			"resource/schema/file.json#/properties/path/pattern",
			"resource/schema/directory.json#/properties/path/pattern",
			"resource/schema/user.json#/properties/shell/pattern",
			"resource/schema/user.json#/properties/home/pattern",
		},
		"absolute path, forbidden parts": {
			"resource/schema/file.json#/properties/path/not/pattern",
			"resource/schema/directory.json#/properties/path/not/pattern",
			"resource/schema/user.json#/properties/shell/not/pattern",
			"resource/schema/user.json#/properties/home/not/pattern",
		},
		"resource id": {
			"resource/schema/service.json#/properties/restart_on/items/pattern",
			"state/schema/target.json#/properties/spec/properties/overrides/propertyNames/pattern",
		},
	}
	files := map[string]any{}
	lookup := func(ref string) string {
		file, pointer, _ := strings.Cut(ref, "#/")
		if _, ok := files[file]; !ok {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			files[file] = doc
		}
		node := files[file]
		for _, key := range strings.Split(pointer, "/") {
			obj, ok := node.(map[string]any)
			if !ok {
				t.Fatalf("%s: nothing at %q", ref, key)
			}
			node = obj[key]
		}
		value, ok := node.(string)
		if !ok || value == "" {
			t.Fatalf("%s: no pattern there", ref)
		}
		return value
	}
	for name, refs := range groups {
		want := lookup(refs[0])
		for _, ref := range refs[1:] {
			if got := lookup(ref); got != want {
				t.Errorf("%s: %s has %q, but %s has %q", name, ref, got, refs[0], want)
			}
		}
	}
}
