package slice_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestSliceAcceptance(t *testing.T) {
	srv, bearer, runner, root := fixture(t)
	call := sliceCall(t, srv.URL, bearer)
	wait := func(label string, ready func() bool) {
		t.Helper()
		start := time.Now()
		for !ready() {
			if time.Since(start) >= 10*time.Second {
				t.Fatalf("%s exceeded 10 s", label)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("%s: %s", label, time.Since(start))
	}
	id := func(v any) string { return fmt.Sprintf("%.0f", v) }
	approve := func(p map[string]any) map[string]any {
		t.Helper()
		if len(p["human_only"].([]any)) != 0 {
			t.Fatal("secret-free slice unexpectedly needs a human")
		}
		run := call("POST", "/plans/"+id(p["id"])+"/approve", nil, 202)
		wait("run succeeds", func() bool {
			run = call("GET", "/runs/"+id(run["id"]), nil, 200)
			if run["status"] == "failed" {
				t.Fatalf("run failed: %+v", run)
			}
			return run["status"] == "succeeded"
		})
		return run
	}
	docs := []any{}
	for _, name := range []string{"deploy-user", "motd", "nginx"} {
		b, err := os.ReadFile(filepath.Join("../../blocks", name+".yaml"))
		must(t, err)
		var doc map[string]any
		must(t, yaml.Unmarshal(b, &doc))
		docs = append(docs, doc)
	}
	docs = append(docs,
		map[string]any{"kind": "Image", "name": "ubuntu", "spec": map[string]any{"type": "vm", "url": "https://images.invalid/u.img", "sha256": strings.Repeat("0", 64)}},
		map[string]any{"kind": "Target", "name": "web1", "spec": map[string]any{"type": "vm", "image": "ubuntu", "guest": map[string]any{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0"}, "blocks": []any{
			map[string]any{"use": "deploy-user", "params": map[string]string{"ssh_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest slice"}},
			map[string]any{"use": "motd"}, map[string]any{"use": "nginx"},
		}}},
	)
	approve(call("POST", "/plans", map[string]any{"reason": "slice acceptance", "put": docs}, 201))
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, path))
		must(t, err)
		return string(b)
	}
	if read("etc/motd") != "Managed by Plumb.\n" || !strings.Contains(read("home/deploy/.ssh/authorized_keys"), "ssh-ed25519") {
		t.Fatal("starter blocks not applied")
	}
	const site = "etc/nginx/sites-available/default"
	original := read(site)
	drift := func(value string) string {
		t.Helper()
		write(t, root, site, value)
		var issueID string
		wait("nginx drift issue opens", func() bool {
			for _, item := range call("GET", "/issues?status=open", nil, 200)["items"].([]any) {
				i := item.(map[string]any)
				if i["target"] == "web1" && i["resource"] == "file:/"+site {
					issueID = id(i["id"])
					return true
				}
			}
			return false
		})
		return issueID
	}
	issueID := drift("manual first drift\n")
	p := call("POST", "/issues/"+issueID+"/resolve", map[string]string{"action": "revert"}, 201)
	restartPlanned := false
	for _, target := range p["targets"].([]any) {
		for _, res := range target.(map[string]any)["resources"].([]any) {
			r := res.(map[string]any)
			for _, change := range r["changes"].([]any) {
				if r["resource"] == "service:nginx" && change.(map[string]any)["attr"] == "restart" {
					restartPlanned = true
				}
			}
		}
	}
	if !restartPlanned {
		t.Fatal("revert plan omits nginx restart")
	}
	before := runner.restarts.Load()
	reverted := approve(p)
	if read(site) != original || runner.restarts.Load() != before+1 {
		t.Fatal("revert did not restore site and restart nginx")
	}
	wait("revert closes issue", func() bool {
		i := call("GET", "/issues/"+issueID, nil, 200)
		return i["status"] == "resolved" && i["resolution"].(map[string]any)["action"] == "revert"
	})
	issueID = drift("manual accepted drift\n")
	accepted := approve(call("POST", "/issues/"+issueID+"/resolve", map[string]string{"action": "accept"}, 201))
	if accepted["revision"].(float64) <= reverted["revision"].(float64) || read(site) != "manual accepted drift\n" {
		t.Fatal("accept did not create a revision preserving drift")
	}
	wait("accept closes issue", func() bool {
		i := call("GET", "/issues/"+issueID, nil, 200)
		return i["status"] == "resolved" && i["resolution"].(map[string]any)["action"] == "accept"
	})
	target := call("GET", "/targets/web1", nil, 200)
	raw, err := json.Marshal(target)
	must(t, err)
	if !bytes.Contains(raw, []byte(`manual accepted drift\n`)) {
		t.Fatal("accepted content missing from target revision")
	}
}

func sliceCall(t *testing.T, url, bearer string) func(string, string, any, int) map[string]any {
	return func(method, path string, body any, status int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		must(t, err)
		req, err := http.NewRequest(method, url+"/api/v1"+path, bytes.NewReader(raw))
		must(t, err)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
		must(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		must(t, err)
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
		var out map[string]any
		must(t, json.Unmarshal(b, &out))
		return out
	}
}
