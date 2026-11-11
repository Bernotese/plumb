//go:build pve

package pve_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

const site = "/etc/nginx/sites-available/default"
const fingerprintCommand = "sudo ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub; cat /proc/sys/kernel/random/boot_id"

func fingerprint(t *testing.T, g guest) string {
	t.Helper()
	out := guestRead(t, g, fingerprintCommand)
	if !strings.Contains(out, "SHA256:") || len(strings.Split(out, "\n")) != 2 {
		t.Fatal("invalid guest fingerprint or boot ID")
	}
	return out
}

func guestRead(t *testing.T, g guest, command string) string {
	t.Helper()
	out, ok := guestSSH(t, g, command)
	if !ok {
		t.Fatal("guest SSH failed (output withheld)")
	}
	return out
}

func (a *apiClient) issue(target, resource string, limit time.Duration) string {
	a.t.Helper()
	var found string
	wait(a.t, "drift issue opens", limit, func() bool {
		path := "/issues?status=open&target=" + target + "&limit=200"
		for {
			page := a.call("GET", path, nil, 200)
			for _, item := range page["items"].([]any) {
				i := item.(map[string]any)
				if i["target"] == target && i["resource"] == resource {
					found = id(i["id"])
					return true
				}
			}
			next, _ := page["next"].(string)
			if next == "" {
				return false
			}
			path = "/issues?status=open&target=" + target + "&limit=200&cursor=" + url.QueryEscape(next)
		}
	})
	return found
}

func (a *apiClient) closed(issue, action string) {
	a.t.Helper()
	wait(a.t, "issue resolved", 10*time.Second, func() bool {
		i := a.call("GET", "/issues/"+issue, nil, 200)
		r, _ := i["resolution"].(map[string]any)
		return i["status"] == "resolved" && r["action"] == action
	})
}

func hasRestart(p object, field string) bool {
	for _, target := range p["targets"].([]any) {
		for _, item := range target.(map[string]any)[field].([]any) {
			r := item.(map[string]any)
			if r["resource"] != "service:nginx" {
				continue
			}
			for _, item := range r["changes"].([]any) {
				if item.(map[string]any)["attr"] == "restart" {
					return true
				}
			}
		}
	}
	return false
}

func driftPath(a *apiClient, g guest, target, original string) {
	a.t.Helper()
	change := func(marker string) string {
		// Comments keep nginx configuration valid for a real service restart.
		guestRead(a.t, g, "printf '\\n# "+marker+"\\n' | sudo tee -a "+site+" >/dev/null")
		return a.issue(target, "file:"+site, 10*time.Second)
	}
	issue := change("T015-revert")
	p := a.call("POST", "/issues/"+issue+"/resolve", object{"action": "revert"}, 201)
	if !hasRestart(p, "resources") {
		a.t.Fatal("revert plan omits nginx restart")
	}
	before := guestRead(a.t, g, "systemctl show nginx -p InvocationID --value")
	reverted := a.approve(p, false)
	if !hasRestart(reverted, "steps") || guestRead(a.t, g, "sudo cat "+site) != original ||
		guestRead(a.t, g, "systemctl show nginx -p InvocationID --value") == before {
		a.t.Fatal("revert did not restore content and restart nginx")
	}
	a.closed(issue, "revert")
	issue = change("T015-accept")
	accepted := a.approve(a.call("POST", "/issues/"+issue+"/resolve", object{"action": "accept"}, 201), false)
	if accepted["revision"].(float64) <= reverted["revision"].(float64) ||
		!strings.Contains(guestRead(a.t, g, "sudo cat "+site), "# T015-accept") {
		a.t.Fatal("accept lost drift or revision")
	}
	a.closed(issue, "accept")
	d := a.call("GET", "/targets/"+target, nil, 200)["document"].(map[string]any)
	o := d["spec"].(map[string]any)["overrides"].(map[string]any)
	f, _ := o["file:"+site].(map[string]any)
	content, _ := f["content"].(string)
	if !strings.Contains(content, "# T015-accept") {
		a.t.Fatal("accepted content missing from target revision")
	}
}
