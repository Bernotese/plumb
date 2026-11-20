// Package usertext holds the guard that the texts of the server, the
// agent, the CLI and the installer name no decision, task or ARCHITECTURE
// number: those are for the team, not for the user. Code comments and test
// names keep them.
package usertext

import (
	"bufio"
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
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // a comment line of an embedded script or config file
			}
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
	src := "package x\n// D-123 in a comment\nvar a = \"fine\" // T-45\nvar b = `#!/bin/sh\n# a script comment (D-9)\necho ok`\nvar c = \"x (D-777)\"\n"
	got := literalHits(t, "x.go", []byte(src))
	if len(got) != 1 || !strings.HasSuffix(got[0], ": D-777") {
		t.Fatalf("hits %v, want only the literal c", got)
	}
}

// shellOutput matches an install.sh line that prints: say, die, warn,
// echo, printf. The --help text is the here-document of usage().
var shellOutput = regexp.MustCompile(`^\s*(say|die|warn|echo|printf)\b`)

// TestInstallerOutputNamesNoTeamNumber reads install/install.sh: the
// lines that print, and the --help text (the usage here-document). A
// comment line or a trailing comment keeps its number.
func TestInstallerOutputNamesNoTeamNumber(t *testing.T) {
	f, err := os.Open("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hits := installerHits(bufio.NewScanner(f))
	if len(hits) > 0 {
		t.Errorf("install.sh prints a team number:\n%s", strings.Join(hits, "\n"))
	}
}

func installerHits(sc *bufio.Scanner) []string {
	var hits []string
	inHelp := false
	for n := 1; sc.Scan(); n++ {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "usage() {"):
			inHelp = true // its here-document is the --help text
			continue
		case inHelp && l == "EOF":
			inHelp = false
			continue
		}
		if !inHelp && !shellOutput.MatchString(l) {
			continue
		}
		if i := strings.Index(l, " # "); i >= 0 && !inHelp {
			l = l[:i] // a trailing comment
		}
		if w := teamWord.FindString(l); w != "" {
			hits = append(hits, "install.sh:"+strconv.Itoa(n)+": "+w)
		}
	}
	return hits
}

func TestInstallerHitsSkipsComments(t *testing.T) {
	src := "# D-1 comment\nsay \"fine\" # T-22\ndie \"bad (D-333)\"\nx=1 # D-4\nusage() {\n\tcat <<'EOF'\n  --x  a flag (D-55)\nEOF\n}\n"
	got := installerHits(bufio.NewScanner(strings.NewReader(src)))
	if len(got) != 2 || got[0] != "install.sh:3: D-333" || got[1] != "install.sh:7: D-55" {
		t.Fatalf("hits %v", got)
	}
}
