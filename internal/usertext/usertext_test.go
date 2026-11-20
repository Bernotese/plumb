// Package usertext holds the guard that the texts of the server, the
// agent, the CLI and the installer name no decision, task or ARCHITECTURE
// number: those are for the team, not for the user. Code comments and test
// names keep them.
package usertext

import (
	"bufio"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// teamWord is a decision (D-123), a task (T-123), an ARCHITECTURE
// section (§ 5) or a slice ("slice 3").
var teamWord = regexp.MustCompile(`\b[DT]-\d{2,4}\b|§ ?\d+|\b[Ss]lice \d+\b`)

// literalHits returns every string literal of the Go source that holds a
// team word, as "file:line: text".
func literalHits(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution) // comments are not parsed
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			s = lit.Value
		}
		// Every line, also a "#" line: an embedded script or config file
		// lands in a guest or on the host with its comments.
		for _, line := range strings.Split(s, "\n") {
			if w := teamWord.FindString(line); w != "" {
				hits = append(hits, fset.Position(lit.Pos()).String()+": "+w)
			}
		}
		return true
	})
	return hits
}

// TestGoTextsNameNoTeamNumber reads every non-test Go file of internal/
// and cmd/.
func TestGoTextsNameNoTeamNumber(t *testing.T) {
	var hits []string
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			hits = append(hits, literalHits(t, p, src)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Errorf("a text names a team number (remove it; a comment may keep it):\n%s", strings.Join(hits, "\n"))
	}
}

// The parser sees literals only: a comment keeps its number, a literal
// does not.
func TestLiteralHitsSkipsComments(t *testing.T) {
	src := "package x\n// D-123 in a comment\nvar a = \"fine\" // T-45\nvar b = `#!/bin/sh\n# a script comment (D-99)\necho ok`\nvar c = \"x (D-777)\"\n"
	got := literalHits(t, "x.go", []byte(src))
	// The Go comments go; the comment line inside the script literal b
	// stays a hit (it lands in a guest).
	if len(got) != 2 || !strings.HasSuffix(got[0], ": D-99") || !strings.HasSuffix(got[1], ": D-777") {
		t.Fatalf("hits %v, want the literals b and c", got)
	}
}

// shellOutput matches a shell line that prints: say, die, warn, echo or
// printf, at the start or after ||, &&, ;, { , then or else. The --help text is the here-document of usage().
// hereDoc finds the start of a here-document and its end word.
var hereDoc = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

var shellOutput = regexp.MustCompile(`(^\s*|[;&|{]\s*|\bthen\s+|\belse\s+)(say|die|warn|echo|printf)\b`)

// TestInstallerOutputNamesNoTeamNumber reads install/install.sh: the
// lines that print, and every here-document, all of its lines: the --help
// text, and the files that the script writes on the host or into a guest
// (the vendor snippet, the copy script and its units), whose comments land
// there too. A comment line of the script itself keeps its number.
// It reads the agent's install script too: a person runs it in a guest
// and reads its plumb-install: lines.
func TestInstallerOutputNamesNoTeamNumber(t *testing.T) {
	for _, p := range []string{"../../install/install.sh", "../../internal/link/agentinstall/install.sh"} {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		hits := installerHits(filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), bufio.NewScanner(f))
		f.Close()
		if len(hits) > 0 {
			t.Errorf("%s prints a team number:\n%s", p, strings.Join(hits, "\n"))
		}
	}
}

func installerHits(name string, sc *bufio.Scanner) []string {
	var hits []string
	end := "" // the end word of the here-document that is open
	for n := 1; sc.Scan(); n++ {
		l := sc.Text()
		switch {
		case end != "" && strings.TrimSpace(l) == end:
			end = ""
			continue
		case end != "":
			// a line of a here-document: read it all
		default:
			if m := hereDoc.FindStringSubmatch(l); m != nil && !strings.HasPrefix(strings.TrimSpace(l), "#") {
				end = m[1]
			}
			if !shellOutput.MatchString(l) {
				continue
			}
			if i := strings.Index(l, " # "); i >= 0 {
				l = l[:i] // a trailing comment
			}
		}
		if w := teamWord.FindString(l); w != "" {
			hits = append(hits, name+":"+strconv.Itoa(n)+": "+w)
		}
	}
	return hits
}

func TestInstallerHitsSkipsComments(t *testing.T) {
	src := "# D-1 comment\nsay \"fine\" # T-22\ndie \"bad (D-333)\"\nx=1 # D-4\n[ -n \"$x\" ] || die \"bad (D-444)\"\nusage() {\n\tcat <<'EOF'\n  --x  a flag (D-55)\nEOF\n}\n" +
		"\tcat >\"$f\" <<COPY_EOF\n# a comment of a written file (D-66)\nCOPY_EOF\ny=2 # D-7\n"
	got := installerHits("install.sh", bufio.NewScanner(strings.NewReader(src)))
	if len(got) != 4 || got[0] != "install.sh:3: D-333" || got[1] != "install.sh:5: D-444" || got[2] != "install.sh:8: D-55" || got[3] != "install.sh:12: D-66" {
		t.Fatalf("hits %v", got)
	}
}

// schemaHits returns the description and title texts of a JSON schema
// that hold a team word (T-279: the UI shows them as field hints).
func schemaHits(t *testing.T, name string, src []byte) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var hits []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if s, ok := c.(string); ok && (k == "description" || k == "title") {
					if w := teamWord.FindString(s); w != "" {
						hits = append(hits, name+":"+path+"/"+k+": "+w)
					}
					continue
				}
				walk(path+"/"+k, c)
			}
		case []any:
			for i, c := range x {
				walk(path+"/"+strconv.Itoa(i), c)
			}
		}
	}
	walk("", doc)
	return hits
}

// TestSchemaTextsNameNoTeamNumber reads the JSON schemas of the documents
// and the resources. The web imports these files (no copy) and shows
// their texts as field hints.
func TestSchemaTextsNameNoTeamNumber(t *testing.T) {
	var hits []string
	for _, dir := range []string{"../../internal/state/schema", "../../internal/resource/schema"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no schema in %s: %v", dir, err)
		}
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			hits = append(hits, schemaHits(t, filepath.Base(f), src)...)
		}
	}
	if len(hits) > 0 {
		t.Errorf("a schema text names a team number:\n%s", strings.Join(hits, "\n"))
	}
}

func TestSchemaHitsReadsOnlyTexts(t *testing.T) {
	src := `{"title":"T","description":"a hint (D-12)","properties":{"x":{"description":"fine","pattern":"^D-1$","examples":["T-99"]}},"oneOf":[{"title":"see T-277"}]}`
	got := schemaHits(t, "x.json", []byte(src))
	if len(got) != 2 {
		t.Fatalf("hits %v, want the two texts", got)
	}
}
