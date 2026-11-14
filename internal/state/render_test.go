package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

func readDoc(t *testing.T, path string) Doc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return d
}

// baseDocs returns the Images and Blocks of testdata.
func baseDocs(t *testing.T) []Doc {
	t.Helper()
	raw, err := os.ReadFile("testdata/images.json")
	if err != nil {
		t.Fatal(err)
	}
	var docs []Doc
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatal(err)
	}
	blocks, _ := filepath.Glob("testdata/blocks/*.json")
	if len(blocks) < 3 {
		t.Fatalf("want at least three Blocks in testdata, have %d", len(blocks))
	}
	for _, p := range blocks {
		docs = append(docs, readDoc(t, p))
	}
	return docs
}

// TestRenderGolden renders every Target in testdata/targets and compares
// the result with testdata/golden. Run with -update to rewrite them.
func TestRenderGolden(t *testing.T) {
	targets, _ := filepath.Glob("testdata/targets/*.json")
	docs := baseDocs(t)
	for _, p := range targets {
		docs = append(docs, readDoc(t, p))
	}
	set, err := Validate(docs)
	if err != nil {
		t.Fatalf("testdata is not valid:\n%v", err)
	}
	used := map[string]bool{}
	for _, name := range set.Targets() {
		t.Run(name, func(t *testing.T) {
			r, err := set.Render(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, res := range r.Resources {
				used[res.Block] = true
			}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(r); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata/golden", name+".json")
			if *update {
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("render of %s differs from %s:\n%s", name, golden, buf.String())
			}
		})
	}
	for _, b := range []string{"deploy-user", "nginx", "motd"} {
		if !used[b] {
			t.Errorf("golden tests do not render Block %s", b)
		}
	}
}

// fixture builds a document set from small JSON specs.
func doc(kind, name, spec string) Doc {
	return Doc{Kind: kind, Name: name, Spec: json.RawMessage(spec)}
}

const (
	image  = `{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + "0000000000000000000000000000000000000000000000000000000000000000" + `"}`
	guest  = `"guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"}`
	motdB  = `{"params":{"type":"object","properties":{"text":{"type":"string","default":"hi"},"path":{"type":"string","default":"/etc/motd"}}},"resources":[{"file":{"path":"{{ .path }}","content":"{{ .text }}"}}]}`
	keyedB = `{"params":{"type":"object","required":["key"],"properties":{"key":{"type":"string"},"n":{"type":"integer","default":3}}},"resources":[{"file":{"path":"/k","content":"{{ .key }}"}}]}`
)

func target(body string) Doc {
	return doc(KindTarget, "t1", `{"type":"vm","image":"img",`+guest+`,`+body+`}`)
}

// set returns the base fixture plus extra documents.
func fixture(extra ...Doc) []Doc {
	return append([]Doc{
		doc(KindImage, "img", image),
		doc(KindBlock, "motd", motdB),
		doc(KindBlock, "keyed", keyedB),
	}, extra...)
}

// wantErr validates docs and checks that one error has the path and
// contains the text.
func wantErr(t *testing.T, docs []Doc, path, text string) {
	t.Helper()
	_, err := Validate(docs)
	var es Errors
	if !errors.As(err, &es) {
		t.Fatalf("want Errors, got %v", err)
	}
	for _, e := range es {
		if e.Path == path && strings.Contains(e.Msg, text) {
			return
		}
	}
	t.Fatalf("want error at %s containing %q, got:\n%v", path, text, err)
}

func TestValidateReferences(t *testing.T) {
	t.Run("unknown block", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"nope"}]`)), "/spec/blocks/0/use", `Block "nope" does not exist`)
	})
	t.Run("unknown image", func(t *testing.T) {
		wantErr(t, fixture(doc(KindTarget, "t1", `{"type":"vm","image":"other",`+guest+`}`)), "/spec/image", `Image "other" does not exist`)
	})
	t.Run("image of other kind", func(t *testing.T) {
		wantErr(t, fixture(doc(KindTarget, "t1", `{"type":"lxc","image":"img",`+guest+`}`)), "/spec/image", "is for vm")
	})
	t.Run("unknown parameter", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd","params":{"txt":"x"}}]`)), "/spec/blocks/0/params/txt", `no parameter "txt"`)
	})
	t.Run("missing required parameter", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"keyed"}]`)), "/spec/blocks/0/params", "key")
	})
	t.Run("wrong parameter type", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"keyed","params":{"key":"a","n":"x"}}]`)), "/spec/blocks/0/params/n", "integer")
	})
	t.Run("schema error has a path", func(t *testing.T) {
		wantErr(t, fixture(doc(KindTarget, "t1", `{"type":"vm","image":"img","guest":{"cores":0,"memory":512,"disk":4,"bridge":"vmbr0"}}`)), "/spec/guest/cores", "minimum")
	})
	t.Run("unknown resource type", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "bad", `{"resources":[{"shell":{"run":"x"}}]}`)), "/spec/resources/0/shell", "unknown resource type")
	})
	t.Run("bad default", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "bad", `{"params":{"type":"object","properties":{"n":{"type":"integer","default":"x"}}},"resources":[{"file":{"path":"/x"}}]}`)),
			"/spec/params/properties/n/default", "integer")
	})
	t.Run("use of an invalid block", func(t *testing.T) {
		bad := doc(KindBlock, "bad", `{"resources":[{"file":{"path":"/x","content":"{{ print 1 }}"}}]}`)
		docs := fixture(bad, target(`"blocks":[{"use":"bad"}]`))
		wantErr(t, docs, "/spec/blocks/0/use", `Block "bad" is invalid (see its own errors)`)
		_, err := Validate(docs)
		if strings.Contains(err.Error(), "does not exist") {
			t.Errorf("an invalid Block is reported as missing:\n%v", err)
		}
	})
	t.Run("invalid image", func(t *testing.T) {
		docs := []Doc{doc(KindImage, "img", `{"type":"vm","url":"http://x","sha256":"0"}`), target(`"blocks":[]`)}
		wantErr(t, docs, "/spec/image", `Image "img" is invalid`)
	})
	t.Run("duplicate document", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "motd", motdB)), "", "twice")
	})
	t.Run("external schema ref is not loaded", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "bad", `{"params":{"type":"object","properties":{"x":{"$ref":"file:///etc/passwd"}}},"resources":[{"file":{"path":"/x"}}]}`)),
			"/spec/params", "not allowed")
	})
}

// Blocks for the order tests (D-167 … D-173): a small nginx and a site that
// comes after its package and restarts its service.
const (
	nginxB = `{"resources":[{"package":{"name":"nginx"}},{"file":{"path":"/etc/nginx/default","content":"d"}},` +
		`{"service":{"name":"nginx","restart_on":["file:/etc/nginx/default"]}}]}`
	siteB = `{"params":{"type":"object","properties":{"name":{"type":"string","default":"site"}}},` +
		`"after":["package:nginx"],"restarts":["service:nginx"],"resources":[` +
		`{"file":{"path":"/etc/nginx/{{ .name }}","content":"s"}},` +
		`{"symlink":{"path":"/etc/nginx/on-{{ .name }}","target":"/etc/nginx/{{ .name }}"}}]}`
	sitesAB = `{"use":"site","id":"site-a","params":{"name":"a"}},{"use":"site","id":"site-b","params":{"name":"b"}}`
)

func orderFixture(extra ...Doc) []Doc {
	return append(fixture(doc(KindBlock, "nginx", nginxB), doc(KindBlock, "site", siteB)), extra...)
}

func renderT1(t *testing.T, docs []Doc) *Rendered {
	t.Helper()
	set, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Render("t1")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func resourceOf(t *testing.T, r *Rendered, id string) wire.Resource {
	t.Helper()
	for _, res := range r.Resources {
		if res.ID == id {
			return res
		}
	}
	t.Fatalf("no resource %s", id)
	return wire.Resource{}
}

// D-167, D-169, D-172: after goes to the first resource of each use,
// restarts go into restart_on of the service, and the Target order of the
// uses does not change the apply order.
func TestAfterAndRestartsRender(t *testing.T) {
	for name, blocks := range map[string]string{
		"nginx first": `"blocks":[{"use":"nginx"},` + sitesAB + `]`,
		"sites first": `"blocks":[` + sitesAB + `,{"use":"nginx"}]`,
	} {
		r := renderT1(t, orderFixture(target(blocks)))
		for _, res := range r.Resources {
			var want []string
			if res.ID == "file:/etc/nginx/a" || res.ID == "file:/etc/nginx/b" {
				want = []string{"package:nginx"}
			}
			if !reflect.DeepEqual(res.After, want) {
				t.Errorf("%s: %s has after %v, want %v", name, res.ID, res.After, want)
			}
		}
		// The JSON form ([]any), as from a Block: the restart_on check
		// and plan.plannedRestarts read it (review S2).
		got, isAny := resourceOf(t, r, "service:nginx").Attrs["restart_on"].([]any)
		want := []any{"file:/etc/nginx/default", "file:/etc/nginx/a", "symlink:/etc/nginx/on-a", "file:/etc/nginx/b", "symlink:/etc/nginx/on-b"}
		if !isAny || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: restart_on = %#v, want %#v", name, resourceOf(t, r, "service:nginx").Attrs["restart_on"], want)
		}
		edges, err := wire.Order(r.Resources)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, res := range r.Resources {
			if res.ID != "file:/etc/nginx/a" {
				continue
			}
			if len(edges[i]) != 1 || edges[i][0].Kind != wire.EdgeAfter || r.Resources[edges[i][0].To].ID != "package:nginx" {
				t.Errorf("%s: the site waits for %+v, want only after package:nginx", name, edges[i])
			}
		}
	}
	// The template of the Block is not changed by a render: a second
	// Target gets its own list.
	set, err := Validate(orderFixture(
		target(`"blocks":[{"use":"nginx"},`+sitesAB+`]`),
		doc(KindTarget, "t2", `{"type":"vm","image":"img",`+guest+`,"blocks":[{"use":"nginx"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r, err := set.Render("t2")
		if err != nil {
			t.Fatal(err)
		}
		if got := wire.RestartOn(resourceOf(t, r, "service:nginx")); !reflect.DeepEqual(got, []string{"file:/etc/nginx/default"}) {
			t.Errorf("t2 restart_on = %v", got)
		}
	}
}

func TestRestartsRules(t *testing.T) {
	t.Run("service without restart_on gets the list", func(t *testing.T) {
		plain := doc(KindBlock, "nginx", `{"resources":[{"package":{"name":"nginx"}},{"service":{"name":"nginx"}}]}`)
		r := renderT1(t, append(fixture(plain, doc(KindBlock, "site", siteB)), target(`"blocks":[{"use":"nginx"},{"use":"site"}]`)))
		want := []string{"file:/etc/nginx/site", "symlink:/etc/nginx/on-site"}
		if got := wire.RestartOn(resourceOf(t, r, "service:nginx")); !reflect.DeepEqual(got, want) {
			t.Errorf("restart_on = %v, want %v", got, want)
		}
	})
	t.Run("skips the service itself and ids that are in the list", func(t *testing.T) {
		own := doc(KindBlock, "own", `{"restarts":["service:x"],"resources":[{"file":{"path":"/a","content":"a"}},{"file":{"path":"/b","content":"b"}},`+
			`{"service":{"name":"x","restart_on":["file:/b"]}}]}`)
		r := renderT1(t, fixture(own, target(`"blocks":[{"use":"own"}]`)))
		if got := wire.RestartOn(resourceOf(t, r, "service:x")); !reflect.DeepEqual(got, []string{"file:/b", "file:/a"}) {
			t.Errorf("restart_on = %v", got)
		}
	})
	t.Run("an override of restart_on replaces the full list", func(t *testing.T) {
		r := renderT1(t, orderFixture(target(`"blocks":[{"use":"nginx"},`+sitesAB+`],"overrides":{"service:nginx":{"restart_on":["file:/etc/nginx/a"]}}`)))
		if got := wire.RestartOn(resourceOf(t, r, "service:nginx")); !reflect.DeepEqual(got, []string{"file:/etc/nginx/a"}) {
			t.Errorf("restart_on = %v", got)
		}
	})
	t.Run("the service is not on the target", func(t *testing.T) {
		wantErr(t, orderFixture(target(`"blocks":[{"use":"motd"},{"use":"site","id":"site-a"}]`)), "/spec/blocks/1",
			`Block "site" (use "site-a") restarts service:nginx, but service:nginx is not on this target; add the Block that provides it`)
	})
	t.Run("a bad id in the merged list still fails the restart_on check", func(t *testing.T) {
		bad := doc(KindBlock, "nginx", `{"resources":[{"package":{"name":"nginx"}},{"service":{"name":"nginx","restart_on":["file:/nope"]}}]}`)
		wantErr(t, append(fixture(bad, doc(KindBlock, "site", siteB)), target(`"blocks":[{"use":"nginx"},{"use":"site"}]`)),
			"/spec/blocks", `restart_on/0: "file:/nope" is not another resource`)
	})
	t.Run("restart_on that is no list is a schema error, not a panic", func(t *testing.T) {
		bad := doc(KindBlock, "nginx", `{"resources":[{"package":{"name":"nginx"}},{"service":{"name":"nginx","restart_on":"file:/x"}}]}`)
		wantErr(t, append(fixture(bad, doc(KindBlock, "site", siteB)), target(`"blocks":[{"use":"nginx"},{"use":"site"}]`)),
			"/spec/blocks", "restart_on")
	})
}

// The save-time errors of after (D-168, plan section 2).
func TestAfterRules(t *testing.T) {
	block := func(name, spec string) Doc { return doc(KindBlock, name, spec) }
	t.Run("unknown resource type", func(t *testing.T) {
		wantErr(t, fixture(block("b", `{"after":["nosuch:x"],"resources":[{"file":{"path":"/b","content":"b"}}]}`)),
			"/spec/after/0", `after: "nosuch:x" has no known resource type`)
	})
	t.Run("guest:config is no agent resource", func(t *testing.T) {
		wantErr(t, fixture(block("b", `{"after":["file:/etc/motd","guest:config"],"resources":[{"file":{"path":"/b","content":"b"}}]}`)),
			"/spec/after/1", `after: "guest:config" has no known resource type`)
	})
	t.Run("the id is not on the target", func(t *testing.T) {
		wantErr(t, orderFixture(
			doc(KindBlock, "plain", `{"resources":[{"service":{"name":"nginx"}}]}`),
			target(`"blocks":[{"use":"plain"},{"use":"site","id":"site-a"}]`)), "/spec/blocks/1",
			`Block "site" (use "site-a") comes after package:nginx, but package:nginx is not on this target; add the Block that provides it`)
	})
	t.Run("the id is a resource of the same use", func(t *testing.T) {
		wantErr(t, fixture(
			block("x", `{"after":["file:/a"],"resources":[{"file":{"path":"/b","content":"b"}},{"file":{"path":"/a","content":"a"}}]}`),
			target(`"blocks":[{"use":"x"}]`)), "/spec/blocks/0",
			`Block "x" (use "x") comes after file:/a, which is its own resource; resources of one Block already apply in the listed order`)
	})
	t.Run("a Block that no Target uses needs no resource", func(t *testing.T) {
		if _, err := Validate(orderFixture()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("after the service that the Block restarts is a cycle", func(t *testing.T) {
		// Why after names package:nginx and not the nginx use (D-167).
		bad := block("site", `{"after":["service:nginx"],"restarts":["service:nginx"],"resources":[{"file":{"path":"/etc/nginx/s","content":"s"}}]}`)
		wantErr(t, append(fixture(block("nginx", nginxB), bad), target(`"blocks":[{"use":"nginx"},{"use":"site"}]`)), "/spec/blocks",
			`apply order has a cycle: service:nginx (use "nginx") waits for file:/etc/nginx/s (use "site"), which waits for service:nginx; check after, restarts, restart_on and the order of the resources in each Block`)
	})
	t.Run("two Blocks after each other are a cycle", func(t *testing.T) {
		wantErr(t, fixture(
			block("a", `{"after":["file:/b"],"resources":[{"file":{"path":"/a","content":"a"}}]}`),
			block("b", `{"after":["file:/a"],"resources":[{"file":{"path":"/b","content":"b"}}]}`),
			target(`"blocks":[{"use":"a"},{"use":"b"}]`)), "/spec/blocks",
			`apply order has a cycle: file:/a (use "a") waits for file:/b (use "b"), which waits for file:/a`)
	})
	t.Run("restart_on of a later resource in its own Block is a cycle at save time", func(t *testing.T) {
		// Until D-168 only the agent refused this, at the apply (D-055).
		wantErr(t, fixture(
			block("svc", `{"resources":[{"service":{"name":"x","restart_on":["file:/late"]}},{"file":{"path":"/late","content":"l"}}]}`),
			target(`"blocks":[{"use":"svc"}]`)), "/spec/blocks", "apply order has a cycle: service:x")
	})
	t.Run("an override cannot set after", func(t *testing.T) {
		wantErr(t, orderFixture(target(`"blocks":[{"use":"nginx"},{"use":"site"}],"overrides":{"file:/etc/nginx/site":{"after":["file:/etc/motd"]}}`)),
			"/spec/blocks", "has overrides")
	})
}

func TestParamDefaults(t *testing.T) {
	set, err := Validate(fixture(target(`"blocks":[{"use":"motd"},{"use":"motd","id":"m2","params":{"path":"/etc/issue","text":"yo"}}]`)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Render("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Resources) != 2 {
		t.Fatalf("got %d resources", len(r.Resources))
	}
	if got := r.Resources[0]; got.ID != "file:/etc/motd" || got.Attrs["content"] != "hi" || got.Block != "motd" {
		t.Errorf("defaults not applied: %+v", got)
	}
	if got := r.Resources[1]; got.ID != "file:/etc/issue" || got.Attrs["content"] != "yo" || got.Block != "m2" {
		t.Errorf("second use: %+v", got)
	}
	if r.Guest.ID != "guest:config" || r.Guest.Attrs["ip"] != "dhcp" || r.Guest.Attrs["user"] != "plumb" {
		t.Errorf("guest defaults not applied: %+v", r.Guest.Attrs)
	}
}

func TestRenderRules(t *testing.T) {
	t.Run("two uses of one block need ids", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"},{"use":"motd","params":{"path":"/b"}}]`)), "/spec/blocks/1/id", "D-048")
	})
	t.Run("use id clash with another block", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"},{"use":"keyed","id":"motd","params":{"key":"k"}}]`)), "/spec/blocks/1/id", "already used")
	})
	t.Run("duplicate resource id", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"},{"use":"motd","id":"m2"}]`)), "/spec/blocks/1", "file:/etc/motd")
	})
	t.Run("restart_on unknown id", func(t *testing.T) {
		svc := doc(KindBlock, "svc", `{"resources":[{"service":{"name":"x","restart_on":["file:/nope"]}}]}`)
		wantErr(t, fixture(svc, target(`"blocks":[{"use":"svc"}]`)), "/spec/blocks", `"file:/nope" is not another resource`)
	})
	t.Run("restart_on guest config", func(t *testing.T) {
		svc := doc(KindBlock, "svc", `{"resources":[{"service":{"name":"x","restart_on":["guest:config"]}}]}`)
		wantErr(t, fixture(svc, target(`"blocks":[{"use":"svc"}]`)), "/spec/blocks", "guest:config")
	})
	t.Run("restart_on in another block is fine", func(t *testing.T) {
		svc := doc(KindBlock, "svc", `{"resources":[{"service":{"name":"x","restart_on":["file:/etc/motd"]}}]}`)
		if _, err := Validate(fixture(svc, target(`"blocks":[{"use":"motd"},{"use":"svc"}]`))); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rendered attrs must match the resource schema", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd","params":{"path":"relative"}}]`)), "/spec/blocks", "file:relative")
	})
	t.Run("override unknown resource", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"}],"overrides":{"file:/x":{"mode":"0600"}}`)), "/spec/overrides/file:~1x", "not on the target")
	})
	t.Run("override cannot change the key", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"}],"overrides":{"file:/etc/motd":{"path":"/other"}}`)), "/spec/overrides/file:~1etc~1motd/path", "key attribute")
	})
	t.Run("override must match the schema", func(t *testing.T) {
		wantErr(t, fixture(target(`"blocks":[{"use":"motd"}],"overrides":{"file:/etc/motd":{"mode":"999"}}`)), "/spec/blocks", "has overrides")
	})
	t.Run("override of guest config", func(t *testing.T) {
		set, err := Validate(fixture(target(`"blocks":[{"use":"motd"}],"overrides":{"guest:config":{"cores":4},"file:/etc/motd":{"content":"{{ raw }}"}}`)))
		if err != nil {
			t.Fatal(err)
		}
		r, _ := set.Render("t1")
		if r.Guest.Attrs["cores"] != json.Number("4") {
			t.Errorf("guest override: %v", r.Guest.Attrs["cores"])
		}
		if r.Resources[0].Attrs["content"] != "{{ raw }}" {
			t.Errorf("an override is not a template: %v", r.Resources[0].Attrs["content"])
		}
	})
}

func TestTemplateSandbox(t *testing.T) {
	for _, tc := range []struct{ tmpl, text string }{
		{`{{ printf "%s" .text }}`, `"printf" is not allowed`},
		{`{{ call .text }}`, `"call" is not allowed`},
		{`{{ print .text }}`, `"print" is not allowed`},
		{`{{ html .text }}`, `"html" is not allowed`},
		{`{{ _plumb_tick }}`, `"_plumb_tick" is not allowed`},
		{`{{ env "HOME" }}`, `not defined`},
		{`{{ readFile "/etc/passwd" }}`, `not defined`},
		{`{{ define "x" }}a{{ end }}{{ template "x" }}`, `not allowed`},
		{`{{ template "x" }}`, `not allowed`},
		{`{{ .text`, `unclosed action`},
	} {
		blk := doc(KindBlock, "bad", `{"params":{"type":"object","properties":{"text":{"type":"string"}}},"resources":[{"file":{"path":"/x","content":`+mustJSON(tc.tmpl)+`}}]}`)
		wantErr(t, fixture(blk), "/spec/resources/0/file/content", tc.text)
	}
	// A typo in a parameter name fails at render time.
	blk := doc(KindBlock, "typo", `{"params":{"type":"object","properties":{"text":{"type":"string","default":"a"}}},"resources":[{"file":{"path":"/x","content":"{{ .txt }}"}}]}`)
	wantErr(t, fixture(blk, target(`"blocks":[{"use":"typo"}]`)), "/spec/blocks/0", "txt")

	// Numbers have no methods in templates (D-050: never through float64).
	num := doc(KindBlock, "num", `{"params":{"type":"object","properties":{"n":{"type":"number","default":2048.0}}},"resources":[{"file":{"path":"/x","content":"{{ .n.Float64 }}"}}]}`)
	wantErr(t, fixture(num, target(`"blocks":[{"use":"num"}]`)), "/spec/blocks/0", "Float64")
	for _, tmpl := range []string{`{{ .n.String }}`, `{{ $x := .n }}{{ $x.Int64 }}`, `{{ (index . "n").Float64 }}`, `{{ .s.Len }}`} {
		m := doc(KindBlock, "num", `{"params":{"type":"object","properties":{"n":{"type":"number","default":1},"s":{"type":"string","default":"a"}}},"resources":[{"file":{"path":"/x","content":`+mustJSON(tmpl)+`}}]}`)
		if _, err := Validate(fixture(m, target(`"blocks":[{"use":"num"}]`))); err == nil {
			t.Errorf("%s: a method call on data must fail", tmpl)
		}
	}
	numOK := doc(KindBlock, "num", `{"params":{"type":"object","properties":{"n":{"type":"number","default":2048.0},"l":{"type":"array","default":[1.50,2]}}},"resources":[{"file":{"path":"/x","content":"{{ .n }} {{ join \",\" .l }}"}}]}`)
	set0, err := Validate(fixture(numOK, target(`"blocks":[{"use":"num"}]`)))
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := set0.Render("t1"); r.Resources[0].Attrs["content"] != "2048 1.5,2" {
		t.Errorf("numbers print in canonical form: %q", r.Resources[0].Attrs["content"])
	}

	// The allowed functions work.
	fn := doc(KindBlock, "fn", `{"params":{"type":"object","properties":{"l":{"type":"array","default":["a","b"]},"s":{"type":"string","default":" Hi "}}},"resources":[{"file":{"path":"/x","content":`+
		mustJSON(`{{ join "," .l }}|{{ upper (trim .s) }}|{{ replace "a" "b" "aa" }}|{{ default "d" "" }}|{{ len .l }}|{{ if eq (index .l 0) "a" }}y{{ end }}`)+`}}]}`)
	set, err := Validate(fixture(fn, target(`"blocks":[{"use":"fn"}]`)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := set.Render("t1")
	if got, want := r.Resources[0].Attrs["content"], "a,b|HI|bb|d|2|y"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// renderLoop renders a Block whose content is tmpl with the given limits.
func renderLoop(t *testing.T, tmpl string, lim Limits) error {
	t.Helper()
	blk := doc(KindBlock, "loop", `{"params":{"type":"object","properties":{"l":{"type":"array","default":[1,2,3,4,5,6,7,8,9,10]}}},"resources":[{"file":{"path":"/x","content":`+mustJSON(tmpl)+`}}]}`)
	_, err := ValidateWithLimits(fixture(blk, target(`"blocks":[{"use":"loop"}]`)), lim)
	return err
}

func TestRenderLimits(t *testing.T) {
	lim := Limits{Timeout: 200 * time.Millisecond, MaxOutput: 64 << 10}
	for _, tc := range []struct {
		name, tmpl, text string
	}{
		// A loop that writes nothing must stop at the time limit.
		{"huge silent range", `{{ range 1000000000000 }}{{ end }}`, "time limit"},
		{"nested silent range", `{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ range $.l }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}{{ end }}`, "time limit"},
		// A loop that writes must stop at the size limit.
		{"huge writing range", `{{ range 1000000000000 }}xxxxxxxx{{ end }}`, "larger than"},
		// A value that doubles in a variable must stop at the size limit,
		// before it is ever written.
		{"doubling variable", `{{ $s := "xxxxxxxx" }}{{ range 100 }}{{ $s = replace "x" "xx" $s }}{{ end }}`, "larger than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := renderLoop(t, tc.tmpl, lim)
			took := time.Since(start)
			if !errors.Is(err, ErrRenderLimit) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("want a render limit error with %q, got %v", tc.text, err)
			}
			if took > 2*time.Second {
				t.Errorf("render took %v; the limit is %v", took, lim.Timeout)
			}
		})
	}
	// The budget is per Target: two values that fit alone can exceed it together.
	if err := renderLoop(t, `{{ range 990 }}x{{ end }}`, Limits{Timeout: time.Second, MaxOutput: 1000}); err != nil {
		t.Fatalf("992 bytes (with the path) must fit a 1000-byte limit: %v", err)
	}
	if err := renderLoop(t, `{{ range 1001 }}x{{ end }}`, Limits{Timeout: time.Second, MaxOutput: 1000}); !errors.Is(err, ErrRenderLimit) {
		t.Fatalf("1001 bytes must not fit: %v", err)
	}
}

// targets returns n Targets t00…, each using one Block.
func manyTargets(n int, block string) []Doc {
	var out []Doc
	for i := 0; i < n; i++ {
		out = append(out, doc(KindTarget, fmt.Sprintf("t%02d", i), `{"type":"vm","image":"img",`+guest+`,"blocks":[{"use":"`+block+`"}]}`))
	}
	return out
}

func countErrors(err error, target error) (n, all int) {
	var es Errors
	errors.As(err, &es)
	for _, e := range es {
		if errors.Is(e, target) {
			n++
		}
	}
	return n, len(es)
}

// D-052: the cost of one Validate does not grow with the number of Targets.
func TestTotalRenderBudget(t *testing.T) {
	t.Run("time", func(t *testing.T) {
		slow := doc(KindBlock, "slow", `{"resources":[{"file":{"path":"/x","content":"{{ range 1000000000000 }}{{ end }}"}}]}`)
		lim := Limits{Timeout: 200 * time.Millisecond, TotalTimeout: 300 * time.Millisecond}
		start := time.Now()
		_, err := ValidateWithLimits(fixture(append([]Doc{slow}, manyTargets(10, "slow")...)...), lim)
		took := time.Since(start)
		if n, _ := countErrors(err, ErrTotalBudget); n != 1 {
			t.Fatalf("want exactly one total budget error, got %d:\n%v", n, err)
		}
		if !strings.Contains(err.Error(), "total render budget exceeded") {
			t.Errorf("message does not name the total budget: %v", err)
		}
		if took < 250*time.Millisecond || took > time.Second {
			t.Errorf("Validate took %v, want about 300ms", took)
		}
	})
	t.Run("output", func(t *testing.T) {
		// Each Target writes 992 bytes, just below its own limit of 1000.
		big := doc(KindBlock, "big", `{"resources":[{"file":{"path":"/x","content":"{{ range 990 }}x{{ end }}"}}]}`)
		lim := Limits{MaxOutput: 1000, TotalOutput: 3500}
		_, err := ValidateWithLimits(fixture(append([]Doc{big}, manyTargets(10, "big")...)...), lim)
		n, all := countErrors(err, ErrTotalBudget)
		if n != 1 || all != 1 {
			t.Fatalf("want one total budget error and nothing else, got %d of %d:\n%v", n, all, err)
		}
		if errors.Is(err, ErrRenderLimit) {
			t.Errorf("a total budget error must not be a per-render limit error: %v", err)
		}
		if !strings.Contains(err.Error(), `"t03"`) {
			t.Errorf("the fourth Target should hit the total: %v", err)
		}
		// Three Targets fit.
		if _, err := ValidateWithLimits(fixture(append([]Doc{big}, manyTargets(3, "big")...)...), lim); err != nil {
			t.Errorf("three Targets must fit: %v", err)
		}
	})
}

func TestGroupRequirementsAtSave(t *testing.T) {
	const user = `{"user":{"name":"person","groups":["team"]}}`
	const group = `{"group":{"name":"team"}}`
	for _, tc := range []struct{ name, resources, message string }{
		{"user before group", user + "," + group, "user person needs group team first: list the group before the user"},
		{"absent group", `{"group":{"name":"team","state":"absent"}},` + user, "user person needs group team, but the managed group is absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := doc(KindBlock, "accounts", `{"resources":[`+tc.resources+`]}`)
			wantErr(t, fixture(b, target(`"blocks":[{"use":"accounts"}]`)), "/spec/blocks", tc.message)
		})
	}
	renderT1(t, fixture(doc(KindBlock, "accounts", `{"resources":[`+group+","+user+`]}`), target(`"blocks":[{"use":"accounts"}]`)))
}
