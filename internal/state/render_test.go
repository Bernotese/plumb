package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		wantErr(t, fixture(doc(KindBlock, "bad", `{"resources":[{"command":{"run":"x"}}]}`)), "/spec/resources/0/command", "unknown resource type")
	})
	t.Run("bad default", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "bad", `{"params":{"type":"object","properties":{"n":{"type":"integer","default":"x"}}},"resources":[{"file":{"path":"/x"}}]}`)),
			"/spec/params/properties/n/default", "integer")
	})
	t.Run("duplicate document", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "motd", motdB)), "", "twice")
	})
	t.Run("external schema ref is not loaded", func(t *testing.T) {
		wantErr(t, fixture(doc(KindBlock, "bad", `{"params":{"type":"object","properties":{"x":{"$ref":"file:///etc/passwd"}}},"resources":[{"file":{"path":"/x"}}]}`)),
			"/spec/params", "not allowed")
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
