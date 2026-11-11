package plan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestReportedAddrsCallers keeps D-216: the stored address list of a guest
// is display data. Only the status view (Live, through reported) and the
// address check read it. A new caller fails this test; it must not use the
// list to authorize a call or as the target of a connection.
func TestReportedAddrsCallers(t *testing.T) {
	allowed := map[string]bool{
		"internal/plan/addrs.go": true, // reported (for Live) and checkAddress
	}
	root := "../.."
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "web" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReportedAddrs" {
					found = append(found, rel)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no caller of ReportedAddrs found; the test does not see the code")
	}
	for _, f := range found {
		if !allowed[f] {
			t.Errorf("%s reads the reported address list (store.ReportedAddrs); only the status view and the address check may (D-216)", f)
		}
	}
}
