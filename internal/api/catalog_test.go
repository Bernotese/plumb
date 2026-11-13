package api

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Bernotese/plumb/blocks"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

type catalogPage struct {
	Items []catalogBlock `json:"items"`
}

func getCatalog(t *testing.T, c *client) catalogPage {
	t.Helper()
	code, body := c.do("GET", "/api/v1/catalog/blocks", nil)
	if code != 200 {
		t.Fatalf("GET /catalog/blocks: %d %s", code, body)
	}
	var p catalogPage
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func catalogDoc(t *testing.T, it catalogBlock) state.Doc {
	t.Helper()
	var d state.Doc
	if err := json.Unmarshal(it.Document, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// T-146 (D-328): the catalogue lists every embedded file, sorted by name,
// with the shipped bytes, the hash of the canonical document and the kinds.
func TestCatalogListsEmbeddedBlocks(t *testing.T) {
	e := newEnv(t, true)
	p := getCatalog(t, e.token(TierRead, ""))
	files, err := fs.Glob(blocks.FS, "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) == 0 || len(p.Items) != len(files) {
		t.Fatalf("catalogue has %d items, %d files", len(p.Items), len(files))
	}
	for i, it := range p.Items {
		if i > 0 && p.Items[i-1].Name >= it.Name {
			t.Errorf("not sorted by name: %s before %s", p.Items[i-1].Name, it.Name)
		}
		raw, err := fs.ReadFile(blocks.FS, it.Name+".yaml")
		if err != nil {
			t.Errorf("%s: no file of that name: %v", it.Name, err)
			continue
		}
		if it.YAML != string(raw) {
			t.Errorf("%s: yaml is not the shipped file", it.Name)
		}
		body, err := state.Canonical(catalogDoc(t, it))
		if err != nil || state.Hash(body) != it.SHA256 || string(body) != string(it.Document) {
			t.Errorf("%s: sha256 or document not canonical: %v", it.Name, err)
		}
		if it.Stored != "none" || it.Title == "" || it.Description == "" || len(it.Kinds) == 0 {
			t.Errorf("%s: %+v", it.Name, it)
		}
	}
}

// T-146 acceptance: every catalogue Block passes the same server check as
// a user's Block: a proposal that puts it is accepted (201), alone and all
// together. A proposal never changes the head.
func TestCatalogBlocksPassServerCheck(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	p := getCatalog(t, adm)
	var all []state.Doc
	for _, it := range p.Items {
		d := catalogDoc(t, it)
		all = append(all, d)
		if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "add " + it.Name, "put": []state.Doc{d}}); code != 201 {
			t.Errorf("catalogue Block %s refused: %d %s", it.Name, code, body)
		}
	}
	if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "add all", "put": all}); code != 201 {
		t.Errorf("all catalogue Blocks together refused: %d %s", code, body)
	}
	if _, err := e.st.Head(e.ctx); err == nil {
		t.Error("a proposal of catalogue Blocks made a revision")
	}
}

// T-146 acceptance (D-328): stored is none, same or differs against the
// head, and listing the catalogue never puts or overwrites a stored Block.
func TestCatalogStoredNeverOverwritten(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	p := getCatalog(t, adm)
	if len(p.Items) < 2 {
		t.Fatal("the test needs two catalogue Blocks")
	}
	same, changed := catalogDoc(t, p.Items[0]), catalogDoc(t, p.Items[1])
	changed.Spec = json.RawMessage(`{"title":"Mine","resources":[{"file":{"path":"/etc/mine","content":"mine\n"}}]}`)
	set, err := state.Validate([]state.Doc{same, changed})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.Rows(set)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.st.CommitRevision(e.ctx, store.NewRevision{Author: "user:admin", Reason: "mine", Docs: rows})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{same.Name: "same", changed.Name: "differs"}
	for range 2 { // listing twice changes nothing
		for _, it := range getCatalog(t, e.token(TierRead, "")).Items {
			w, ok := want[it.Name]
			if !ok {
				w = "none"
			}
			if it.Stored != w {
				t.Errorf("%s: stored %q, want %q", it.Name, it.Stored, w)
			}
		}
	}
	head, err := e.st.Head(e.ctx)
	if err != nil || head.ID != rev.ID {
		t.Fatalf("head moved: %+v %v", head, err)
	}
	got, err := e.st.RevisionDoc(e.ctx, head.ID, state.KindBlock, changed.Name)
	if err != nil || !strings.Contains(string(got.Body), "/etc/mine") {
		t.Fatalf("the user's Block was changed: %s %v", got.Body, err)
	}
	if n := strings.Count(string(mustList(t, adm)), `"kind":"Block"`); n != 2 {
		t.Errorf("head holds %d Blocks, want 2 (nothing was added)", n)
	}
	// An update from the catalogue is a normal proposal: it passes the check
	// and stays a pending Plan until a person approves it.
	if code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "update", "put": []state.Doc{catalogDoc(t, p.Items[1])}}); code != 201 ||
		!strings.Contains(string(body), `"pending"`) {
		t.Fatalf("update from the catalogue: %d %s", code, body)
	}
	if head, _ := e.st.Head(e.ctx); head.ID != rev.ID {
		t.Fatal("an update proposal made a revision")
	}
}

func mustList(t *testing.T, c *client) []byte {
	t.Helper()
	code, body := c.do("GET", "/api/v1/blocks", nil)
	if code != 200 {
		t.Fatalf("GET /blocks: %d %s", code, body)
	}
	return body
}

func TestCatalogKinds(t *testing.T) {
	fsys := fstest.MapFS{
		"a.yaml": {Data: []byte("kind: Block\nname: a\nspec:\n  title: A\n  description: d\n  resources:\n    - file: {path: /x}\n    - sysctl: {name: vm.swappiness, value: \"10\"}\n")},
		"b.yaml": {Data: []byte("kind: Block\nname: b\nspec:\n  title: B\n  description: d\n  resources:\n    - package: {name: curl}\n")},
	}
	items, err := loadCatalog(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(items[0].Kinds, ",") != "vm" || strings.Join(items[1].Kinds, ",") != "vm,lxc" {
		t.Fatalf("kinds: %v %v", items[0].Kinds, items[1].Kinds)
	}
}

func TestCatalogRefusesBadFiles(t *testing.T) {
	for name, data := range map[string]string{
		"two docs":       "kind: Block\nname: a\nspec: {resources: [{file: {path: /x}}]}\n---\nkind: Block\nname: b\nspec: {resources: [{file: {path: /x}}]}\n",
		"not a Block":    "kind: Rule\nname: a\nspec: {}\n",
		"anchor":         "kind: Block\nname: a\nspec: &s {resources: [{file: {path: /x}}]}\n",
		"unknown type":   "kind: Block\nname: a\nspec: {resources: [{nope: {name: x}}]}\n",
		"invalid YAML":   "kind: [\n",
		"duplicate name": "",
	} {
		fsys := fstest.MapFS{"a.yaml": {Data: []byte(data)}}
		if name == "duplicate name" {
			one := []byte("kind: Block\nname: a\nspec: {resources: [{file: {path: /x}}]}\n")
			fsys = fstest.MapFS{"a.yaml": {Data: one}, "b.yaml": {Data: one}}
		}
		if _, err := loadCatalog(fsys); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
