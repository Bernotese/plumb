package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const imgSHA = "0000000000000000000000000000000000000000000000000000000000000000"

// builtImage is an Image named "web" that builds with the given Block uses.
func builtImage(t *testing.T, mut func(spec map[string]any)) Doc {
	t.Helper()
	spec := map[string]any{
		"type": "vm", "url": "https://images.invalid/u.img", "sha256": imgSHA,
		"build": map[string]any{
			"blocks": []any{map[string]any{"use": "motd"}},
			"guest":  map[string]any{"disk": 10, "bridge": "vmbr0"},
		},
	}
	if mut != nil {
		mut(spec)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return Doc{Kind: KindImage, Name: "web", Spec: raw}
}

func motdBlock(content string) Doc {
	raw, _ := json.Marshal(map[string]any{
		"params":    map[string]any{"type": "object", "properties": map[string]any{"who": map[string]any{"type": "string", "default": "you"}}},
		"resources": []any{map[string]any{"file": map[string]any{"path": "/etc/motd", "content": content}}},
	})
	return Doc{Kind: KindBlock, Name: "motd", Spec: raw}
}

func diskTarget(disk int) Doc {
	raw, _ := json.Marshal(map[string]any{"type": "vm", "image": "web",
		"guest": map[string]any{"cores": 1, "memory": 1024, "disk": disk, "bridge": "vmbr0"}})
	return Doc{Kind: KindTarget, Name: "web1", Spec: raw}
}

func buildBlock(t *testing.T, name string, resources ...map[string]any) Doc {
	t.Helper()
	list := make([]any, len(resources))
	for i, r := range resources {
		list[i] = r
	}
	raw, _ := json.Marshal(map[string]any{"resources": list})
	return Doc{Kind: KindBlock, Name: name, Spec: raw}
}

func recipeHash(t *testing.T, docs ...Doc) string {
	t.Helper()
	set, err := Validate(docs)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r, err := set.RenderImage("web")
	if err != nil {
		t.Fatalf("RenderImage: %v", err)
	}
	return r.RecipeSHA256
}

func TestRenderImage(t *testing.T) {
	set, err := Validate([]Doc{builtImage(t, nil), motdBlock("hi {{ .who }}")})
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.RenderImage("web")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Resources) != 1 || r.Resources[0].ID != "file:/etc/motd" || r.Resources[0].Attrs["content"] != "hi you" {
		t.Fatalf("resources = %+v", r.Resources)
	}
	g := r.Guest.Attrs
	if n, _ := intOf(g["cores"]); n != 1 {
		t.Errorf("build guest cores = %v, want the default 1", g["cores"])
	}
	if n, _ := intOf(g["memory"]); n != 1024 || g["ip"] != "dhcp" {
		t.Errorf("build guest = %v, want memory 1024 and ip dhcp by default", g)
	}
	if !r.Recipe.Upgrade || r.Recipe.DiskGB != 10 || r.Recipe.SHA256 != imgSHA || r.Recipe.Type != "vm" {
		t.Errorf("recipe = %+v", r.Recipe)
	}
	if got := set.BuiltImages(); len(got) != 1 || got[0] != "web" {
		t.Errorf("BuiltImages = %v", got)
	}
	// A stock Image has no build.
	set, _ = Validate([]Doc{{Kind: KindImage, Name: "web", Spec: json.RawMessage(secImage)}})
	if _, err := set.RenderImage("web"); !errors.Is(err, ErrNotBuilt) {
		t.Errorf("RenderImage of a stock Image = %v, want ErrNotBuilt", err)
	}
	if _, err := set.RenderImage("nope"); err == nil {
		t.Error("RenderImage of a missing Image: no error")
	}
}

func TestRecipeHash(t *testing.T) {
	base := recipeHash(t, builtImage(t, nil), motdBlock("hi"))
	if again := recipeHash(t, motdBlock("hi"), builtImage(t, nil)); again != base {
		t.Fatalf("hash depends on the document order: %s != %s", again, base)
	}
	build := func(spec map[string]any) map[string]any { return spec["build"].(map[string]any) }
	guest := func(spec map[string]any) map[string]any { return build(spec)["guest"].(map[string]any) }
	changes := map[string][]Doc{
		"url":     {builtImage(t, func(s map[string]any) { s["url"] = "https://images.invalid/v.img" }), motdBlock("hi")},
		"sha256":  {builtImage(t, func(s map[string]any) { s["sha256"] = strings.Repeat("1", 64) }), motdBlock("hi")},
		"upgrade": {builtImage(t, func(s map[string]any) { build(s)["upgrade"] = false }), motdBlock("hi")},
		"disk":    {builtImage(t, func(s map[string]any) { guest(s)["disk"] = 12 }), motdBlock("hi")},
		"block":   {builtImage(t, nil), motdBlock("hello")},
		"params": {builtImage(t, func(s map[string]any) {
			build(s)["blocks"] = []any{map[string]any{"use": "motd", "params": map[string]any{"who": "me"}}}
		}), motdBlock("{{ .who }}")},
	}
	for name, docs := range changes {
		if name == "params" {
			if recipeHash(t, docs...) == recipeHash(t, builtImage(t, nil), motdBlock("{{ .who }}")) {
				t.Errorf("a change of %s keeps the recipe hash", name)
			}
			continue
		}
		if recipeHash(t, docs...) == base {
			t.Errorf("a change of %s keeps the recipe hash", name)
		}
	}
	// The other settings of the build guest do not change the template.
	same := recipeHash(t, builtImage(t, func(s map[string]any) {
		guest(s)["cores"] = 4
		guest(s)["memory"] = 4096
		build(s)["upgrade"] = true // the default, written out
		s["retention"] = map[string]any{"keep": 5}
		s["schedule"] = map[string]any{"every": "day", "at": "01:00"}
	}), motdBlock("hi"))
	if same != base {
		t.Error("cores, memory, an explicit default upgrade, retention or schedule change the recipe hash")
	}
}

func TestImageRefusesSecrets(t *testing.T) {
	uses := func(params map[string]any) func(map[string]any) {
		return func(s map[string]any) {
			use := map[string]any{"use": "app"}
			if params != nil {
				use["params"] = params
			}
			s["build"].(map[string]any)["blocks"] = []any{use}
		}
	}
	// A secret reference as the value.
	_, err := Validate([]Doc{builtImage(t, uses(map[string]any{"db_password": ref("db")})), secBlock("{{ .db_password }}")})
	errAt(t, err, KindImage, "/spec/build/blocks/0/use", "cannot use secrets")
	// A Block with a secret parameter, also without a value.
	_, err = Validate([]Doc{builtImage(t, uses(nil)), secBlock("x")})
	errAt(t, err, KindImage, "/spec/build/blocks/0/use", "cannot use secrets")
	// A reference inside a plain parameter.
	_, err = Validate([]Doc{builtImage(t, func(s map[string]any) {
		s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "motd", "params": map[string]any{"who": ref("db")}}}
	}), motdBlock("{{ .who }}")})
	errAt(t, err, KindImage, "/spec/build/blocks/0/params/who", "takes no secret reference")
}

// n3: a Target and an Image share a Block. A later change gives the Block
// a secret parameter for the Target. The whole set is checked, so the
// Image fails.
func TestSharedBlockChangeReachesImage(t *testing.T) {
	share := func(s map[string]any) {
		s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "app", "params": map[string]any{}}}
	}
	plain := Doc{Kind: KindBlock, Name: "app", Spec: json.RawMessage(`{"resources":[{"file":{"path":"/etc/app.env","content":"x"}}]}`)}
	target := Doc{Kind: KindTarget, Name: "web1", Spec: json.RawMessage(`{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"app"}]}`)}
	if _, err := Validate([]Doc{builtImage(t, share), plain, target}); err != nil {
		t.Fatalf("before the change: %v", err)
	}
	target.Spec = json.RawMessage(`{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"app","params":{"db_password":{"secret":"db"}}}]}`)
	_, err := Validate([]Doc{builtImage(t, share), secBlock("{{ .db_password }}"), target})
	errAt(t, err, KindImage, "/spec/build/blocks/0/use", "cannot use secrets")
}

func TestTargetDiskNotSmallerThanImage(t *testing.T) {
	for _, tt := range []struct {
		disk int
		ok   bool
	}{{8, false}, {9, false}, {10, true}, {20, true}} {
		_, err := Validate([]Doc{builtImage(t, nil), motdBlock("hi"), diskTarget(tt.disk)})
		if tt.ok && err != nil {
			t.Errorf("disk %d: %v", tt.disk, err)
		}
		if !tt.ok {
			errAt(t, err, KindTarget, "/spec/guest/disk", "cannot shrink")
		}
	}
	// A stock Image has no rule.
	if _, err := Validate([]Doc{{Kind: KindImage, Name: "web", Spec: json.RawMessage(secImage)}, diskTarget(1)}); err != nil {
		t.Errorf("stock image: %v", err)
	}
}

func TestAgentPathsRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"file":      {"file": map[string]any{"path": "/var/lib/plumb-agent/credential", "content": "x"}},
		"directory": {"directory": map[string]any{"path": "/etc/plumb-agent"}},
		"symlink":   {"symlink": map[string]any{"path": "/tmp/l", "target": "/etc/plumb-agent/agent.json"}},
		"unit":      {"file": map[string]any{"path": "/etc/systemd/system/plumb-agent.service.d/x.conf", "content": "x"}},
	}
	for name, res := range cases {
		// On a Target.
		target := Doc{Kind: KindTarget, Name: "web1", Spec: json.RawMessage(`{"type":"vm","image":"ubuntu","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"bad"}]}`)}
		_, err := Validate([]Doc{{Kind: KindImage, Name: "ubuntu", Spec: json.RawMessage(secImage)}, buildBlock(t, "bad", res), target})
		errAt(t, err, KindTarget, "/spec/blocks", "path of the Plumb agent")
		// In an Image build.
		_, err = Validate([]Doc{builtImage(t, func(s map[string]any) {
			s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "bad"}}
		}), buildBlock(t, "bad", res)})
		errAt(t, err, KindImage, "/spec/build/blocks", "path of the Plumb agent")
		_ = name
	}
	// A stored revision from before the rule still loads (D-207).
	target := Doc{Kind: KindTarget, Name: "web1", Spec: json.RawMessage(`{"type":"vm","image":"ubuntu","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"bad"}]}`)}
	if _, err := validate([]Doc{{Kind: KindImage, Name: "ubuntu", Spec: json.RawMessage(secImage)}, buildBlock(t, "bad", cases["file"]), target}, DefaultLimits, true); err != nil {
		t.Errorf("stored revision: %v", err)
	}
}
