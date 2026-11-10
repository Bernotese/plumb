package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// bs is a backslash. Tests build escapes at run time, so no tool or editor
// turns them into the characters they stand for.
var bs = string(rune(92))

const secImage = `{"type":"vm","url":"https://images.invalid/u.img","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`

// secBlock is a Block with a secret parameter and one file whose content
// is the template tmpl.
func secBlock(tmpl string) Doc {
	spec := map[string]any{
		"params": map[string]any{"type": "object", "required": []any{"db_password"}, "properties": map[string]any{
			"db_password": map[string]any{"type": "string", "writeOnly": true},
			"user":        map[string]any{"type": "string", "default": "app"},
			"opts":        map[string]any{"type": "object"},
		}},
		"resources": []any{map[string]any{"file": map[string]any{"path": "/etc/app.env", "content": tmpl, "mode": "0600"}}},
	}
	raw, _ := json.Marshal(spec)
	return Doc{Kind: KindBlock, Name: "app", Spec: raw}
}

func secTarget(params map[string]any, extra ...string) Doc {
	use := map[string]any{"use": "app", "params": params}
	spec := map[string]any{"type": "vm", "image": "ubuntu",
		"guest": map[string]any{"cores": 1, "memory": 1024, "disk": 10, "bridge": "vmbr0"}, "blocks": []any{use}}
	if len(extra) > 0 {
		var over map[string]any
		json.Unmarshal([]byte(extra[0]), &over)
		spec["overrides"] = over
	}
	raw, _ := json.Marshal(spec)
	return Doc{Kind: KindTarget, Name: "web1", Spec: raw}
}

func ref(name string) map[string]any { return map[string]any{"secret": name} }

func secDocs(tmpl string, params map[string]any, extra ...string) []Doc {
	return []Doc{{Kind: KindImage, Name: "ubuntu", Spec: json.RawMessage(secImage)}, secBlock(tmpl), secTarget(params, extra...)}
}

// errAt returns the validation error with the given pointer, or fails.
func errAt(t *testing.T, err error, kind, path, msg string) {
	t.Helper()
	var es Errors
	if !errors.As(err, &es) {
		t.Fatalf("want an error at %s, got %v", path, err)
	}
	for _, e := range es {
		if e.Kind == kind && e.Path == path && strings.Contains(e.Error(), msg) {
			return
		}
	}
	t.Fatalf("no error %s %s %q in:\n%v", kind, path, msg, err)
}

func renderOne(t *testing.T, tmpl string) (*Rendered, error) {
	t.Helper()
	set, err := Validate(secDocs(tmpl, map[string]any{"db_password": ref("app-db")}))
	if err != nil {
		return nil, err
	}
	return set.Render("web1")
}

func TestSecretReferenceRules(t *testing.T) {
	const tmpl = "DB_PASSWORD={{ .db_password }}\n"
	// A literal is refused, with the pointer of the parameter.
	_, err := Validate(secDocs(tmpl, map[string]any{"db_password": "hunter2"}))
	errAt(t, err, KindTarget, "/spec/blocks/0/params/db_password", "never a literal")
	// A broken reference.
	for _, bad := range []any{map[string]any{"secret": "Bad_Name"}, map[string]any{"secret": "a", "x": 1}, map[string]any{"secret": 1}} {
		_, err := Validate(secDocs(tmpl, map[string]any{"db_password": bad}))
		errAt(t, err, KindTarget, "/spec/blocks/0/params/db_password", "secret reference")
	}
	// A reference on another parameter.
	_, err = Validate(secDocs(tmpl, map[string]any{"db_password": ref("a"), "opts": ref("b")}))
	errAt(t, err, KindTarget, "/spec/blocks/0/params/opts", "takes no secret reference")
	_, err = Validate(secDocs(tmpl, map[string]any{"db_password": ref("a"), "user": ref("b")}))
	errAt(t, err, KindTarget, "/spec/blocks/0/params/user", "takes no secret reference")
	// Missing secret parameter: the schema says required.
	_, err = Validate(secDocs(tmpl, map[string]any{}))
	if err == nil {
		t.Fatal("a missing required secret parameter was accepted")
	}

	// Block rules: writeOnly must be a string without default, top level.
	block := func(prop map[string]any, nested bool) error {
		props := map[string]any{"p": prop}
		if nested {
			props = map[string]any{"o": map[string]any{"type": "object", "properties": map[string]any{"p": prop}}}
		}
		raw, _ := json.Marshal(map[string]any{"params": map[string]any{"type": "object", "properties": props},
			"resources": []any{map[string]any{"file": map[string]any{"path": "/x", "content": "x"}}}})
		_, err := Validate([]Doc{{Kind: KindBlock, Name: "b", Spec: raw}})
		return err
	}
	errAt(t, block(map[string]any{"type": "integer", "writeOnly": true}, false), KindBlock, "/spec/params/properties/p", "type string")
	errAt(t, block(map[string]any{"type": "string", "writeOnly": true, "default": "x"}, false), KindBlock, "/spec/params/properties/p/default", "cannot have a default")
	errAt(t, block(map[string]any{"type": "string", "writeOnly": true}, true), KindBlock, "/spec/params/properties/o/properties/p/writeOnly", "top-level")
	if err := block(map[string]any{"type": "string", "writeOnly": true}, false); err != nil {
		t.Fatalf("a plain secret parameter: %v", err)
	}

	// A marker in the key attribute or in restart_on.
	keyBlock := secBlock("x")
	var spec map[string]any
	json.Unmarshal(keyBlock.Spec, &spec)
	spec["resources"] = []any{map[string]any{"file": map[string]any{"path": "/etc/{{ .db_password }}", "content": "x"}}}
	keyBlock.Spec, _ = json.Marshal(spec)
	docs := secDocs("x", map[string]any{"db_password": ref("a")})
	docs[1] = keyBlock
	_, err = Validate(docs)
	errAt(t, err, KindTarget, "/spec/blocks/0", "path cannot hold a secret")
	spec["resources"] = []any{
		map[string]any{"file": map[string]any{"path": "/etc/a", "content": "x"}},
		map[string]any{"service": map[string]any{"name": "app", "restart_on": []any{"file:{{ .db_password }}"}}}}
	keyBlock.Spec, _ = json.Marshal(spec)
	docs[1] = keyBlock
	_, err = Validate(docs)
	errAt(t, err, KindTarget, "/spec/blocks/0", "restart_on cannot hold a secret")

	// An override cannot hold a reference.
	_, err = Validate(secDocs(tmpl, map[string]any{"db_password": ref("a")}, `{"file:/etc/app.env":{"content":{"secret":"a"}}}`))
	errAt(t, err, KindTarget, "/spec/overrides/file:~1etc~1app.env/content", "override cannot hold a secret reference")

	// Marker characters in any document value or key are refused.
	_, err = Validate(secDocs(tmpl, map[string]any{"db_password": ref("a"), "user": "x" + string(MarkStart) + "a" + string(MarkEnd)}))
	errAt(t, err, KindTarget, "/spec/blocks/0/params/user", "reserved for secret markers")
	_, err = Validate(secDocs(tmpl, map[string]any{"db_password": ref("a"), "opts": map[string]any{string(MarkEnd): 1}}))
	errAt(t, err, KindTarget, "/spec/blocks/0/params/opts/"+string(MarkEnd), "reserved for secret markers")
	_, err = Validate(secDocs("a"+string(MarkStart), map[string]any{"db_password": ref("a")}))
	errAt(t, err, KindBlock, "/spec/resources/0/file/content", "reserved for secret markers")
}

func TestSecretRender(t *testing.T) {
	r1, err := renderOne(t, "DB_PASSWORD={{ .db_password }}\nUSER={{ .user }}\n")
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := renderOne(t, "DB_PASSWORD={{ .db_password }}\nUSER={{ .user }}\n")
	res := r1.Resources[0]
	want := "DB_PASSWORD=" + Marker("app-db") + "\nUSER=app\n"
	if res.Attrs["content"] != want {
		t.Fatalf("content %q, want %q", res.Attrs["content"], want)
	}
	// The stored render is stable: equal for two renders (another nonce).
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("two renders differ")
	}
	if !reflect.DeepEqual(res.Sensitive, []string{"content"}) || len(r1.Guest.Sensitive) != 0 {
		t.Fatalf("sensitive %v", res.Sensitive)
	}
	if got := SecretNames(r1); !reflect.DeepEqual(got, []string{"app-db"}) {
		t.Fatalf("names %v", got)
	}
	set, _ := Validate(secDocs("{{ .db_password }}", map[string]any{"db_password": ref("app-db")}))
	if refs := set.SecretRefs("web1"); len(refs) != 1 || refs[0] != (SecretRef{"app-db", "/spec/blocks/0/params/db_password"}) {
		t.Fatalf("refs %+v", refs)
	}

	// WithSecrets replaces markers in a copy; the render stays as it was.
	body, err := WithSecrets(r1.Resources, map[string]string{"app-db": "hunter2"})
	if err != nil || body[0].Attrs["content"] != "DB_PASSWORD=hunter2\nUSER=app\n" || res.Attrs["content"] != want {
		t.Fatalf("with secrets: %v %v", body, err)
	}
	for _, vals := range []map[string]string{nil, {"other": "x"}, {"app-db": ""}} {
		if _, err := WithSecrets(r1.Resources, vals); err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("missing value: %v", err)
		}
	}

	// An override with a literal makes the attribute plain again.
	set, err = Validate(secDocs("{{ .db_password }}", map[string]any{"db_password": ref("a")}, `{"file:/etc/app.env":{"content":"plain"}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := set.Render("web1")
	if len(r.Resources[0].Sensitive) != 0 || len(SecretNames(r)) != 0 {
		t.Fatalf("override: %+v", r.Resources[0])
	}
	var _ = wire.Resource{}
}

// Every way a template could read a secret gives an error or the unchanged
// marker; the few that give something else give a constant that does not
// depend on the secret (it is never in the render at all).
func TestSecretTemplateTable(t *testing.T) {
	marker := Marker("app-db")
	errCases := []string{
		`{{ lower .db_password }}`, `{{ upper .db_password }}`, `{{ trim .db_password }}`,
		`{{ trimPrefix "a" .db_password }}`, `{{ trimPrefix .db_password "a" }}`, `{{ trimSuffix "a" .db_password }}`,
		`{{ contains "a" .db_password }}`, `{{ hasPrefix "a" .db_password }}`, `{{ hasSuffix "a" .db_password }}`,
		`{{ replace "a" "b" .db_password }}`, `{{ replace .db_password "b" "c" }}`, `{{ join "," .db_password }}`,
		`{{ len .db_password }}`, `{{ index .db_password 0 }}`, `{{ index .db_password "x" }}`,
		`{{ slice .db_password 1 }}`, `{{ slice .db_password }}`,
		`{{ eq .db_password "x" }}`, `{{ eq .db_password .db_password }}`, `{{ ne .db_password "x" }}`,
		`{{ lt .db_password "x" }}`, `{{ le .db_password "x" }}`, `{{ gt .db_password "x" }}`, `{{ ge .db_password "x" }}`,
		`{{ range .db_password }}x{{ end }}`, `{{ .db_password.X }}`, `{{ .db_password.marker }}`, `{{ .db_password.Format }}`,
		`{{ print .db_password }}`, `{{ printf "%s" .db_password }}`, `{{ println .db_password }}`,
		`{{ html .db_password }}`, `{{ js .db_password }}`, `{{ urlquery .db_password }}`, `{{ call .db_password }}`,
		`{{ .db_password | lower }}`, `{{ with $x := .db_password }}{{ len $x }}{{ end }}`,
	}
	for _, tmpl := range errCases {
		r, err := renderOne(t, tmpl)
		if err == nil {
			t.Errorf("%s: no error, content %q", tmpl, r.Resources[0].Attrs["content"])
		}
	}
	markerCases := []string{
		`{{ .db_password }}`, `{{ default "d" .db_password }}`, `{{ or .db_password "x" }}`, `{{ index .db_password }}`,
		`{{ with .db_password }}{{ . }}{{ end }}`, `{{ $x := .db_password }}{{ $x }}`, `{{ if .db_password }}{{ .db_password }}{{ end }}`,
	}
	for _, tmpl := range markerCases {
		r, err := renderOne(t, tmpl)
		if err != nil || r.Resources[0].Attrs["content"] != marker {
			t.Errorf("%s: %v %q", tmpl, err, contentOf(r))
		}
	}
	constCases := map[string]string{
		`{{ if .db_password }}yes{{ else }}no{{ end }}`: "yes", // a struct is always true
		`{{ and .db_password "x" }}`:                    "x",
		`{{ not .db_password }}`:                        "false",
	}
	for tmpl, want := range constCases {
		r, err := renderOne(t, tmpl)
		if err != nil || r.Resources[0].Attrs["content"] != want {
			t.Errorf("%s: %v %q, want the constant %q", tmpl, err, contentOf(r), want)
		}
	}
}

func contentOf(r *Rendered) any {
	if r == nil {
		return nil
	}
	return r.Resources[0].Attrs["content"]
}

// A template cannot forge a marker (D-088).
func TestSecretMarkerForgery(t *testing.T) {
	start, end := bs+"uE000", bs+"uE001"
	for name, tmpl := range map[string]string{
		"string constant with escapes": `{{ "` + start + `prod-db` + end + `" }}`,
		"marker from two actions":      `{{ "` + start + `" }}prod-db{{ "` + end + `" }}`,
		"end only":                     `{{ .db_password }}{{ "` + end + `" }}`,
		"start inside a real marker":   `{{ "` + start + `" }}{{ .db_password }}`,
		"constant next to a marker":    `{{ .db_password }}{{ "` + start + `x` + end + `" }}`,
		"printf would be needed":       `{{ "` + start + `" }}`,
	} {
		r, err := renderOne(t, tmpl)
		if err == nil {
			t.Errorf("%s: accepted, content %q", name, contentOf(r))
		} else if !strings.Contains(err.Error(), "marker") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A marker built from a guessed nonce does not work either: the nonce
	// is new for every render.
	r, err := renderOne(t, `{{ "`+start+`000000000000000000000000app-db`+end+`" }}`)
	if err == nil {
		t.Errorf("guessed nonce accepted: %q", contentOf(r))
	}
}
