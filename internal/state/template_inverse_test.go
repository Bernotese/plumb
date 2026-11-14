package state

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestInverseShape(t *testing.T) {
	for _, c := range []struct {
		text, param, prefix, suffix string
		ok                          bool
	}{
		{"{{ .text }}", "text", "", "", true},
		{"{{.text}}\n", "text", "", "\n", true},
		{"Welcome to {{ .host }}!\n", "host", "Welcome to ", "!\n", true},
		{"a{{ .x }}", "x", "a", "", true},
		{"{{ .a }}{{ .a }}", "", "", "", false},       // two actions
		{"{{ .a }}-{{ .b }}", "", "", "", false},      // two parameters
		{"{{ .a | upper }}", "", "", "", false},       // pipeline
		{"{{ upper .a }}", "", "", "", false},         // function
		{"{{ default \"x\" .a }}", "", "", "", false}, // function
		{"{{ .a.b }}", "", "", "", false},             // nested field
		{"{{ . }}", "", "", "", false},                // dot
		{"{{ $x := .a }}{{ $x }}", "", "", "", false}, // variable
		{"x {{- .a }}", "", "", "", false},            // trim marker
		{"{{ .a -}} y", "", "", "", false},            // trim marker
		{"{{/* c */}}{{ .a }}", "", "", "", false},    // comment
		{"{{ if .a }}y{{ end }}", "", "", "", false},  // branch
		{"{{ range .a }}{{ . }}{{ end }}", "", "", "", false},
		{"{{ \"lit\" }}", "", "", "", false}, // no field
		{"{{ .a }} }}", "", "", "", false},   // second close
		{"plain", "", "", "", false},
	} {
		p, pre, suf, ok := inverseShape(c.text)
		if ok != c.ok || p != c.param || pre != c.prefix || suf != c.suffix {
			t.Errorf("%q: got %q %q %q %v, want %q %q %q %v", c.text, p, pre, suf, ok, c.param, c.prefix, c.suffix, c.ok)
		}
	}
}

// FuzzInverseShape: whenever the shape is accepted, rendering the
// template with any string value gives prefix + value + suffix, so the
// inverse (strip prefix and suffix) gives the value back.
func FuzzInverseShape(f *testing.F) {
	for _, s := range []string{"{{ .text }}", "Welcome {{ .h }}!\n", "{{.a}}x", "{{ .a | upper }}", "a{{- .b }}"} {
		f.Add(s, "value")
	}
	f.Fuzz(func(t *testing.T, text, value string) {
		if len(text) > 200 || !utf8.ValidString(value) {
			return
		}
		p, pre, suf, ok := inverseShape(text)
		if !ok {
			return
		}
		b := newBudget(DefaultLimits, nil)
		out, err := execTemplate(text, map[string]any{p: value}, b)
		if err != nil {
			t.Fatalf("%q: accepted shape does not render: %v", text, err)
		}
		if out != pre+value+suf {
			t.Fatalf("%q with %q: render %q, want %q", text, value, out, pre+value+suf)
		}
	})
}

const (
	accImage = `{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + "0000000000000000000000000000000000000000000000000000000000000000" + `"}`
	accGuest = `"guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"}`
	// greet: one parameter between fixed text; the mode is fixed text.
	accGreet = `{"params":{"type":"object","properties":{"host":{"type":"string","default":"box","maxLength":20},"n":{"type":"integer","default":3},"pw":{"type":"string","writeOnly":true}}},` +
		`"resources":[{"file":{"path":"/etc/motd","content":"Welcome to {{ .host }}!\n","mode":"0644"}},{"file":{"path":"/etc/count","content":"{{ .n }}"}}]}`
	// twice: one parameter feeds two files.
	accTwice = `{"params":{"type":"object","properties":{"v":{"type":"string","default":"a"}}},` +
		`"resources":[{"file":{"path":"/a","content":"{{ .v }}"}},{"file":{"path":"/b","content":"{{ .v }}"}}]}`
	// pair: two attributes of one resource from one parameter.
	accPair = `{"params":{"type":"object","properties":{"u":{"type":"string","default":"x"}}},` +
		`"resources":[{"file":{"path":"/p","content":"{{ .u }}","owner":"{{ .u }}"}}]}`
)

func accSet(t *testing.T, targetBody string, extra ...Doc) *Set {
	t.Helper()
	docs := append([]Doc{
		doc(KindImage, "img", accImage),
		doc(KindBlock, "greet", accGreet),
		doc(KindBlock, "twice", accTwice),
		doc(KindBlock, "pair", accPair),
		doc(KindTarget, "t1", `{"type":"vm","image":"img",`+accGuest+`,`+targetBody+`}`),
	}, extra...)
	s, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ch(attr string, from any) wire.Change { return wire.Change{Attr: attr, From: from} }

func TestAcceptParams(t *testing.T) {
	greet := `"blocks":[{"use":"greet"}]`
	for _, c := range []struct {
		name, body, id string
		changes        []wire.Change
		mode, param    string
		value          any
		reason         string
	}{
		{"prefix and suffix", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to web1!\n")}, AcceptParam, "host", "web1", ""},
		{"restart ignored", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to a!\n"), {Attr: wire.AttrRestart, From: true}}, AcceptParam, "host", "a", ""},
		{"empty middle", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to !\n")}, AcceptParam, "host", "", ""},
		{"integer", greet, "file:/etc/count", []wire.Change{ch("content", "7")}, AcceptParam, "n", json.Number("7"), ""},
		{"integer leading zero", greet, "file:/etc/count", []wire.Change{ch("content", "07")}, AcceptRaw, "", nil, reasonType},
		{"integer text", greet, "file:/etc/count", []wire.Change{ch("content", "seven")}, AcceptRaw, "", nil, reasonType},
		{"prefix missing", greet, "file:/etc/motd", []wire.Change{ch("content", "Hello web1!\n")}, AcceptRaw, "", nil, reasonNoValue},
		{"suffix missing", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to web1")}, AcceptRaw, "", nil, reasonNoValue},
		{"schema", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to "+strings.Repeat("x", 21)+"!\n")}, AcceptRaw, "", nil, reasonType},
		{"fixed attribute", greet, "file:/etc/motd", []wire.Change{ch("content", "Welcome to a!\n"), ch("mode", "0600")}, AcceptRaw, "", nil, reasonNotTemplate},
		{"sensitive", greet, "file:/etc/motd", []wire.Change{{Attr: "content", Sensitive: true}}, AcceptRaw, "", nil, reasonSecret},
		{"no observed value", greet, "file:/etc/motd", []wire.Change{ch("content", nil)}, AcceptRaw, "", nil, reasonNoChange},
		{"override", `"blocks":[{"use":"greet"}],"overrides":{"file:/etc/motd":{"content":"x"}}`, "file:/etc/motd", []wire.Change{ch("content", "Welcome to a!\n")}, AcceptRaw, "", nil, reasonOverride},
		{"feeds another resource", `"blocks":[{"use":"twice"}]`, "file:/a", []wire.Change{ch("content", "new")}, AcceptRaw, "", nil, reasonSideEffect},
		{"same value twice", `"blocks":[{"use":"pair"}]`, "file:/p", []wire.Change{ch("content", "bob"), ch("owner", "bob")}, AcceptParam, "u", "bob", ""},
		{"one of two attributes", `"blocks":[{"use":"pair"}]`, "file:/p", []wire.Change{ch("content", "bob")}, AcceptRaw, "", nil, reasonSideEffect},
		{"conflict", `"blocks":[{"use":"pair"}]`, "file:/p", []wire.Change{ch("content", "bob"), ch("owner", "eve")}, AcceptRaw, "", nil, reasonConflict},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := accSet(t, c.body)
			got := s.AcceptParams("t1", c.id, c.changes)
			if got.Mode != c.mode {
				t.Fatalf("mode %q (%s), want %q", got.Mode, got.Reason, c.mode)
			}
			if c.mode == AcceptRaw {
				if !strings.Contains(got.Reason, c.reason) || len(got.Params) != 0 {
					t.Fatalf("raw: %+v, want reason %q", got, c.reason)
				}
				return
			}
			if len(got.Params) != 1 || got.Params[0].Param != c.param || got.Params[0].Value != c.value || got.Params[0].Index != 0 {
				t.Fatalf("params %+v, want %s = %#v", got.Params, c.param, c.value)
			}
		})
	}
}

// The use index and id are those of the resource's use; the proof render
// does not change the stored Target.
func TestAcceptParamsUseIndex(t *testing.T) {
	s := accSet(t, `"blocks":[{"use":"pair"},{"use":"greet","id":"g","params":{"host":"old"}}]`)
	got := s.AcceptParams("t1", "file:/etc/motd", []wire.Change{ch("content", "Welcome to new!\n")})
	if got.Mode != AcceptParam || len(got.Params) != 1 {
		t.Fatalf("%+v", got)
	}
	if p := got.Params[0]; p.Use != "g" || p.Index != 1 || p.Param != "host" || p.Value != "new" {
		t.Fatalf("%+v", p)
	}
	// The stored Target is unchanged by the proof render.
	again, _ := s.Render("t1")
	for _, r := range again.Resources {
		if r.ID == "file:/etc/motd" && r.Attrs["content"] != "Welcome to old!\n" {
			t.Fatalf("set changed: %v", r.Attrs)
		}
	}
}

func TestAcceptParamsUnknown(t *testing.T) {
	s := accSet(t, `"blocks":[{"use":"greet"}]`)
	for _, c := range []struct{ target, id string }{{"nope", "file:/etc/motd"}, {"t1", "file:/nope"}, {"t1", wire.GuestConfigID}} {
		if got := s.AcceptParams(c.target, c.id, []wire.Change{ch("content", "x")}); got.Mode != AcceptRaw {
			t.Errorf("%v: %+v", c, got)
		}
	}
}

// Property: for any value that the schema takes, render → observed →
// AcceptParams gives the value back, and a render with it gives the
// observed text again.
func FuzzAcceptParamsRoundTrip(f *testing.F) {
	for _, v := range []string{"web1", "", "a b", "Welcome to x!", "!\n", "{{ .host }}"} {
		f.Add(v)
	}
	s, err := Validate([]Doc{
		doc(KindImage, "img", accImage),
		doc(KindBlock, "greet", accGreet),
		doc(KindTarget, "t1", `{"type":"vm","image":"img",`+accGuest+`,"blocks":[{"use":"greet"}]}`),
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, v string) {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 20 || v == "box" || strings.ContainsRune(v, 0) {
			return
		}
		observed := "Welcome to " + v + "!\n"
		got := s.AcceptParams("t1", "file:/etc/motd", []wire.Change{ch("content", observed)})
		if got.Mode != AcceptParam {
			return // the schema or the file rules refused it: raw is safe
		}
		if len(got.Params) != 1 || got.Params[0].Value != v {
			t.Fatalf("%q: %+v", v, got.Params)
		}
		next, ok := s.renderWith("t1", 0, map[string]any{"host": v})
		if !ok {
			t.Fatalf("%q: no render", v)
		}
		for _, r := range next.Resources {
			if r.ID == "file:/etc/motd" && r.Attrs["content"] != observed {
				t.Fatalf("%q: render %q", v, r.Attrs["content"])
			}
		}
	})
}
