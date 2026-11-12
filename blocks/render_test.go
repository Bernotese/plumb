package blocks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
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

func nginxTargetDocs(t *testing.T, uses []any, includeNginx bool, extra ...state.Doc) []state.Doc {
	t.Helper()
	image := state.Doc{Kind: state.KindImage, Name: "debian-13", Spec: json.RawMessage(`{"title":"Debian 13","type":"vm",` +
		`"url":"https://example.invalid/debian.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	docs := []state.Doc{image, blockDoc(t, "nginx-site.yaml")}
	if includeNginx {
		docs = append(docs, blockDoc(t, "nginx.yaml"))
	}
	docs = append(docs, extra...)
	spec, err := json.Marshal(map[string]any{
		"type": "vm", "image": "debian-13",
		"guest":  map[string]any{"cores": 1, "memory": 1024, "disk": 10, "bridge": "vmbr0"},
		"blocks": uses,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(docs, state.Doc{Kind: state.KindTarget, Name: "web1", Spec: spec})
}

func nginxSiteUse(id string) map[string]any {
	return map[string]any{
		"use": "nginx-site", "id": id,
		"params": map[string]any{"name": id, "server_name": id + ".lan"},
	}
}

func TestNginxSiteFirstRunInBothTargetOrders(t *testing.T) {
	for _, tc := range []struct {
		name string
		uses []any
	}{
		{"nginx first", []any{map[string]any{"use": "nginx"}, nginxSiteUse("shop"), nginxSiteUse("blog")}},
		{"sites first", []any{nginxSiteUse("shop"), nginxSiteUse("blog"), map[string]any{"use": "nginx"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, err := state.Validate(nginxTargetDocs(t, tc.uses, true))
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			rendered, err := set.Render("web1")
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			resources := map[string]stateResource{}
			for _, resource := range rendered.Resources {
				resources[resource.ID] = stateResource{after: resource.After, attrs: resource.Attrs}
			}
			for _, site := range []string{"shop", "blog"} {
				id := "file:/etc/nginx/sites-available/" + site
				if got := resources[id].after; len(got) != 1 || got[0] != "package:nginx" {
					t.Errorf("%s after = %v", id, got)
				}
			}
			got, ok := resources["service:nginx"].attrs["restart_on"].([]any)
			if !ok || len(got) != 6 {
				t.Fatalf("service:nginx restart_on = %#v; want 6 ids", resources["service:nginx"].attrs["restart_on"])
			}
			want := []string{
				"file:/etc/nginx/sites-available/default", "symlink:/etc/nginx/sites-enabled/default",
				"file:/etc/nginx/sites-available/shop", "symlink:/etc/nginx/sites-enabled/shop",
				"file:/etc/nginx/sites-available/blog", "symlink:/etc/nginx/sites-enabled/blog",
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("restart_on[%d] = %v, want %s", i, got[i], want[i])
				}
			}
		})
	}
}

type stateResource struct {
	after []string
	attrs map[string]any
}

func TestNginxSiteRequiresNginxPackage(t *testing.T) {
	stubService := state.Doc{Kind: state.KindBlock, Name: "nginx-service", Spec: json.RawMessage(
		`{"title":"Nginx service","description":"test fixture","params":{"type":"object","properties":{}},` +
			`"resources":[{"service":{"name":"nginx","state":"running","enabled":true}}]}`)}
	_, err := state.Validate(nginxTargetDocs(t, []any{nginxSiteUse("shop"), map[string]any{"use": "nginx-service"}}, false, stubService))
	if err == nil || !strings.Contains(err.Error(), "package:nginx") {
		t.Fatalf("Target without nginx Block error = %v; want package:nginx", err)
	}
}

// T-088 (D-255): the default site must serve the page that the Debian and
// Ubuntu package puts in /var/www/html, else it answers 403. The Block owns
// only the site config; it writes no page into the web root.
func TestNginxDefaultSiteServesDistroPage(t *testing.T) {
	set, err := state.Validate(nginxTargetDocs(t, []any{map[string]any{"use": "nginx"}}, true))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	rendered, err := set.Render("web1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var content string
	for _, r := range rendered.Resources {
		if path, _ := r.Attrs["path"].(string); strings.HasPrefix(path, "/var/www") {
			t.Errorf("%s writes into the web root", r.ID)
		}
		if r.ID == "file:/etc/nginx/sites-available/default" {
			content, _ = r.Attrs["content"].(string)
		}
	}
	if !strings.Contains(content, "root /var/www/html;") ||
		!strings.Contains(content, "index index.html index.htm index.nginx-debian.html;") {
		t.Errorf("default site content = %q", content)
	}
}
