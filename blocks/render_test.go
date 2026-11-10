package blocks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

func blockDoc(t *testing.T, path string) state.Doc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Kind string         `yaml:"kind"`
		Name string         `yaml:"name"`
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	spec, err := json.Marshal(doc.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return state.Doc{Kind: doc.Kind, Name: doc.Name, Spec: spec}
}

// T-020: one Target enables the default site and two more sites, and
// Nginx restarts when any of them changes.
func TestNginxSites(t *testing.T) {
	docs := []state.Doc{
		{Kind: state.KindImage, Name: "debian-13", Spec: json.RawMessage(`{"title":"Debian 13","type":"vm",` +
			`"url":"https://example.invalid/debian.img","sha256":"` + strings.Repeat("0", 64) + `"}`)},
		blockDoc(t, "nginx.yaml"),
		blockDoc(t, "nginx-site.yaml"),
		{Kind: state.KindTarget, Name: "web1", Spec: json.RawMessage(`{"type":"vm","image":"debian-13",` +
			`"guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[` +
			`{"use":"nginx"},` +
			`{"use":"nginx-site","id":"shop","params":{"name":"shop","server_name":"shop.lan"}},` +
			`{"use":"nginx-site","id":"blog","params":{"name":"blog","server_name":"blog.lan","root":"/srv/blog"}}],` +
			`"overrides":{"service:nginx":{"restart_on":["file:/etc/nginx/sites-available/default","symlink:/etc/nginx/sites-enabled/default",` +
			`"file:/etc/nginx/sites-available/shop","symlink:/etc/nginx/sites-enabled/shop",` +
			`"file:/etc/nginx/sites-available/blog","symlink:/etc/nginx/sites-enabled/blog"]}}}`)},
	}
	set, err := state.Validate(docs)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := set.Render("web1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	links := map[string]wire.Resource{}
	for _, res := range r.Resources {
		if res.Type == "symlink" {
			links[res.ID] = res
		}
	}
	for site, block := range map[string]string{"default": "nginx", "shop": "shop", "blog": "blog"} {
		l, ok := links["symlink:/etc/nginx/sites-enabled/"+site]
		if !ok || l.Block != block || l.Attrs["target"] != "/etc/nginx/sites-available/"+site || l.Attrs["state"] != "present" {
			t.Errorf("site %s: %+v", site, l)
		}
	}
	if len(links) != 3 {
		t.Errorf("links: %v", links)
	}
}
