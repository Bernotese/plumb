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
	srv, bearer, password, runner, root := fixture(t)
	call := sliceCall(t, srv.URL, bearer)
	human := sessionCall(t, srv.URL, password)
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
	// An Accept adopts guest data: only a person approves it (D-323).
	approve := func(p map[string]any) map[string]any {
		t.Helper()
		approver := call
		if p["origin"] == "accept" {
			if fmt.Sprint(p["human_only"]) != "[accept-guest-data]" {
				t.Fatalf("accept plan human_only: %v", p["human_only"])
			}
			if refused := call("POST", "/plans/"+id(p["id"])+"/approve", nil, 403); refused["type"] == nil {
				t.Fatalf("token approve of accept: %+v", refused)
			}
			approver = human
		} else if len(p["human_only"].([]any)) != 0 {
			t.Fatal("secret-free slice unexpectedly needs a human")
		}
		run := approver("POST", "/plans/"+id(p["id"])+"/approve", nil, 202)
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
		// os.WriteFile truncates, then writes: a drift check between the
		// two (debounce 20 ms, a busy CI runner) opens the Issue with the
		// empty file, and Accept would take that. Wait until the Issue
		// shows the written content.
		wait("nginx drift issue opens", func() bool {
			for _, item := range call("GET", "/issues?status=open", nil, 200)["items"].([]any) {
				i := item.(map[string]any)
				if i["target"] != "web1" || i["resource"] != "file:/"+site {
					continue
				}
				for _, c := range i["changes"].([]any) {
					if ch := c.(map[string]any); ch["attr"] == "content" && ch["from"] == value {
						issueID = id(i["id"])
						return true
					}
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
	return sliceCallAs(t, url, func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+bearer) })
}

// sessionCall logs the admin in, changes the bootstrap password and calls
// as that person. The session cookie is Secure, so it is set by hand.
func sessionCall(t *testing.T, url, password string) func(string, string, any, int) map[string]any {
	t.Helper()
	var cookie *http.Cookie
	call := sliceCallAs(t, url, func(req *http.Request) {
		req.Header.Set("Origin", "https://"+req.Host)
		if cookie != nil {
			req.AddCookie(cookie)
		}
	})
	raw, err := json.Marshal(map[string]string{"username": "admin", "password": password})
	must(t, err)
	req, err := http.NewRequest("POST", url+"/api/v1/session", bytes.NewReader(raw))
	must(t, err)
	req.Header.Set("Origin", "https://"+req.Host)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) != 1 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	cookie = resp.Cookies()[0]
	call("PUT", "/session/password", map[string]string{"current_password": password, "new_password": "a-good-long-password"}, 204)
	return call
}

func sliceCallAs(t *testing.T, url string, auth func(*http.Request)) func(string, string, any, int) map[string]any {
	return func(method, path string, body any, status int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		must(t, err)
		req, err := http.NewRequest(method, url+"/api/v1"+path, bytes.NewReader(raw))
		must(t, err)
		auth(req)
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
		if len(b) > 0 {
			must(t, json.Unmarshal(b, &out))
		}
		return out
	}
}
