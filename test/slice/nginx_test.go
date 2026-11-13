package slice_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/blocks"
	"go.yaml.in/yaml/v3"
)

func TestSliceNginxSitesFirstRun(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, sitesFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/sitesFirst=%t", existing, sitesFirst), func(t *testing.T) {
				srv, bearer, _, runner, root := fixtureNginx(t, false)
				call := sliceCall(t, srv.URL, bearer)
				approve := func(p map[string]any) {
					t.Helper()
					run := call("POST", fmt.Sprintf("/plans/%.0f/approve", p["id"]), nil, 202)
					deadline := time.Now().Add(10 * time.Second)
					for {
						run = call("GET", fmt.Sprintf("/runs/%.0f", run["id"]), nil, 200)
						if run["status"] == "succeeded" {
							return
						}
						if run["status"] == "failed" || time.Now().After(deadline) {
							t.Fatalf("first run: %+v", run)
						}
						time.Sleep(20 * time.Millisecond)
					}
				}
				docs := []any{}
				for _, name := range []string{"motd", "nginx", "nginx-site"} {
					raw, err := blocks.FS.ReadFile(name + ".yaml")
					must(t, err)
					var doc map[string]any
					must(t, yaml.Unmarshal(raw, &doc))
					docs = append(docs, doc)
				}
				docs = append(docs, map[string]any{"kind": "Image", "name": "ubuntu", "spec": map[string]any{"type": "vm", "url": "https://images.invalid/u.img", "sha256": strings.Repeat("0", 64)}})
				uses := []any{map[string]any{"use": "nginx"}}
				for _, name := range []string{"a", "b"} {
					uses = append(uses, map[string]any{"use": "nginx-site", "id": "site-" + name, "params": map[string]any{"name": name, "server_name": name + ".lan"}})
				}
				if sitesFirst {
					uses = []any{uses[1], uses[2], uses[0]}
				}
				spec := map[string]any{"type": "vm", "image": "ubuntu", "guest": map[string]any{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0"}, "blocks": uses}
				target := map[string]any{"kind": "Target", "name": "web1", "spec": spec}
				if existing {
					spec["blocks"] = []any{map[string]any{"use": "motd"}}
				}
				docs = append(docs, target)
				if existing {
					approve(call("POST", "/plans", map[string]any{"reason": "enroll without nginx", "put": docs}, 201))
					spec["blocks"] = append([]any{map[string]any{"use": "motd"}}, uses...)
					docs = []any{target}
				}
				if _, err := os.Stat(filepath.Join(root, "etc/nginx")); !os.IsNotExist(err) {
					t.Fatalf("nginx present before install: %v", err)
				}
				p := call("POST", "/plans", map[string]any{"reason": "first nginx run", "put": docs}, 201)
				count := 0
				for _, target := range p["targets"].([]any) {
					for _, value := range target.(map[string]any)["resources"].([]any) {
						r := value.(map[string]any)
						if r["resource"] == "package:nginx" || strings.HasPrefix(r["resource"].(string), "file:/etc/nginx/sites-available/") {
							count++
							if existing {
								if r["unverified"] == true || len(r["changes"].([]any)) == 0 {
									t.Fatalf("expected verified planned changes: %+v", r)
								}
							} else if r["unverified"] != true {
								t.Fatalf("new Target resource is verified: %+v", r)
							}
						}
					}
				}
				if count != 4 {
					t.Fatalf("expected package and three site files in plan, got %d", count)
				}
				approve(p)
				for _, name := range []string{"a", "b"} {
					raw, err := os.ReadFile(filepath.Join(root, "etc/nginx/sites-available", name))
					must(t, err)
					if !strings.Contains(string(raw), "server_name "+name+".lan;") {
						t.Fatalf("wrong site %s: %s", name, raw)
					}
					link, err := os.Readlink(filepath.Join(root, "etc/nginx/sites-enabled", name))
					must(t, err)
					if link != "/etc/nginx/sites-available/"+name {
						t.Fatalf("wrong link: %s", link)
					}
				}
				if !runner.installed.Load() || runner.restarts.Load() != 1 {
					t.Fatal("expected one package install and one nginx restart")
				}
			})
		}
	}
}
