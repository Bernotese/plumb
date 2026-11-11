//go:build guest

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/blocks"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
	"go.yaml.in/yaml/v3"
)

func TestGuestNginxSitesFirstRun(t *testing.T) {
	comm, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() != 0 || err != nil || string(comm) != "systemd\n" {
		t.Fatal("requires root in the systemd guest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	run := func(t *testing.T, name string, args ...string) []byte {
		t.Helper()
		out, err := (resource.ExecRunner{}).Run(ctx, name, args...)
		if err != nil {
			t.Fatalf("%s %v: %s: %v", name, args, out, err)
		}
		return out
	}
	// curl is a probe prerequisite, not part of either apply.
	run(t, "apt-get", "-q", "-y", "install", "--no-install-recommends", "curl")
	for _, sitesFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("sitesFirst=%t", sitesFirst), func(t *testing.T) {
			run(t, "apt-get", "-q", "-y", "purge", "nginx", "nginx-common")
			// dpkg keeps directories with files from the previous use; remove only
			// this guest test's nginx configuration before checking the clean start.
			if err := os.RemoveAll("/etc/nginx"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat("/etc/nginx"); !os.IsNotExist(err) {
				t.Fatalf("nginx config still present: %v", err)
			}
			webroot := "/srv/plumb-nginx-guest"
			t.Cleanup(func() { os.RemoveAll(webroot) })
			uses := []any{map[string]any{"use": "nginx"}}
			for _, name := range []string{"a", "b"} {
				root := filepath.Join(webroot, name)
				write(t, filepath.Join(root, "index.html"), name+" guest page\n")
				uses = append(uses, map[string]any{"use": "nginx-site", "id": "site-" + name, "params": map[string]any{"name": name, "server_name": name + ".lan", "root": root}})
			}
			if sitesFirst {
				uses = []any{uses[1], uses[2], uses[0]}
			}
			docs := []state.Doc{}
			add := func(value any) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var doc state.Doc
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				docs = append(docs, doc)
			}
			for _, name := range []string{"nginx", "nginx-site"} {
				raw, err := blocks.FS.ReadFile(name + ".yaml")
				if err != nil {
					t.Fatal(err)
				}
				var value map[string]any
				if err := yaml.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				add(value)
			}
			add(map[string]any{"kind": "Image", "name": "guest", "spec": map[string]any{"type": "vm", "url": "https://images.invalid/guest.img", "sha256": fmt.Sprintf("%064d", 0)}})
			add(map[string]any{"kind": "Target", "name": "guest", "spec": map[string]any{"type": "vm", "image": "guest", "guest": map[string]any{"cores": 1, "memory": 512, "disk": 8, "bridge": "vmbr0"}, "blocks": uses}})
			set, err := state.Validate(docs)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := set.Render("guest")
			if err != nil {
				t.Fatal(err)
			}
			list := rendered.Resources
			if len(list) != 8 {
				t.Fatalf("expected eight resources, got %d", len(list))
			}
			ca := newCA(t)
			ctl := newController(t, ca, "guest-token")
			a, err := New(Options{Root: "/", Config: Config{URL: ctl.srv.URL, CA: ca.pem, TokenFile: filepath.Join(t.TempDir(), "token"), StateDir: filepath.Join(t.TempDir(), "state")}})
			if err != nil {
				t.Fatal(err)
			}
			items, err := a.apply(ctx, "nginx-first-run", wire.ApplyBody{Resources: list, Planned: unverified(list...), MaxRisk: wire.RiskDisruptive})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(list) {
				t.Fatalf("missing apply results: %+v", items)
			}
			for _, it := range items {
				if it.Status != wire.StatusApplied && it.Status != wire.StatusUnchanged {
					t.Errorf("unsuccessful step: %+v", it)
				}
			}
			if t.Failed() {
				return
			}
			for _, name := range []string{"a", "b"} {
				out := run(t, "curl", "--fail", "--silent", "--show-error", "--noproxy", "*", "--max-time", "5", "-H", "Host: "+name+".lan", "http://127.0.0.1/")
				if string(out) != name+" guest page\n" {
					t.Errorf("%s.lan returned %q", name, out)
				}
			}
		})
	}
}
