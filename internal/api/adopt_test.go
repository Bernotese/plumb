package api

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

const adoptCA = "-----BEGIN CERTIFICATE-----\nMIIBfake\n-----END CERTIFICATE-----\n"

func withInstall(c *plan.Config) {
	c.Repair.Tokens = c.Agents.(*link.Link)
	c.Repair.CA = []byte(adoptCA)
	c.Repair.AgentURL = "https://plumb.lan:8444"
}

// person adds a guest that a person made to the fake pool.
func (e *env) person(name string) (int, string) {
	e.t.Helper()
	u := driver.Unmanaged{Node: "pve", Name: name, Kind: wire.KindVM, Warnings: []string{driver.WarnNotSealed},
		Present: driver.Present{CIPassword: true, Env: false}, Guest: map[string]any{"cores": 1, "memory": 512, "disk": 4, "bridge": "vmbr0"}}
	id := e.drv.AddUnmanaged(u, driver.GuestSpec{Kind: wire.KindVM, Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0"})
	v, _ := e.drv.InspectGuest(e.ctx, id)
	n, _ := strconv.Atoi(string(id))
	return n, v.Digest
}

func TestAdoptAPI(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	rc := &recorder{t: t}
	planTok := e.token(TierPlan, "")
	vmid, digest := e.person("wiki")

	code, body := rc.do(e.token(TierRead, ""), "GET", "/api/v1/guests/unmanaged", "/guests/unmanaged", nil)
	var list struct {
		Items []struct {
			VMID      int
			Name      string
			Adoptable bool
			Digest    string
			Present   struct{ CIPassword bool } `json:"present"`
		}
	}
	json.Unmarshal(body, &list)
	if code != 200 || len(list.Items) != 1 || list.Items[0].VMID != vmid || !list.Items[0].Adoptable || !list.Items[0].Present.CIPassword {
		t.Fatalf("unmanaged: %d %s", code, body)
	}

	// Refusals: wrong name, wrong digest, bad input.
	for _, c := range []struct {
		name string
		body map[string]any
		code int
		typ  string
	}{
		{"wiki2", map[string]any{"node": "pve", "vmid": vmid, "digest": digest}, 409, "guest-changed"},
		{"wiki", map[string]any{"node": "pve", "vmid": vmid, "digest": strings.Repeat("0", 40)}, 409, "guest-changed"},
		{"wiki", map[string]any{"node": "pve", "vmid": vmid, "digest": "nope"}, 400, "bad-request"},
	} {
		code, body := rc.do(planTok, "POST", "/api/v1/targets/"+c.name+"/adopt", "/targets/{name}/adopt", c.body)
		if code != c.code || problemType(body) != c.typ {
			t.Errorf("%s %v: %d %s", c.name, c.body, code, body)
		}
	}

	// The adopt Plan: a plan token makes it, it is human-only.
	code, body = rc.do(planTok, "POST", "/api/v1/targets/wiki/adopt", "/targets/{name}/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": digest})
	var p struct {
		ID        int64
		Origin    string
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &p)
	if code != 201 || p.Origin != "adopt" || !slices.Contains(p.HumanOnly, "accept-guest-data") {
		t.Fatalf("adopt: %d %s", code, body)
	}
	e.tokenRefused(p.ID)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	code, body = rc.do(adm, "GET", "/api/v1/targets/wiki", "/targets/{name}", nil)
	if code != 200 || !strings.Contains(string(body), `"adopted":true`) {
		t.Fatalf("target: %d %s", code, body)
	}
	// The Target holds the guest now: a second adopt is refused (the guest
	// is bound; D-395 checks the guest before the name).
	if code, body := rc.do(planTok, "POST", "/api/v1/targets/wiki/adopt", "/targets/{name}/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": digest}); code != 409 || problemType(body) != "guest-bound" {
		t.Fatalf("second adopt: %d %s", code, body)
	}
	// D-392: no re-enrollment of an adopted Target.
	if code, body := rc.do(planTok, "POST", "/api/v1/targets/wiki/reenroll", "/targets/{name}/reenroll", nil); code != 409 || problemType(body) != "adopted-target" {
		t.Fatalf("reenroll: %d %s", code, body)
	}

	// The install command: admin session only, once, no-store; the token
	// only inside the here document (review R6 of #290).
	if code, _ := planTok.do("POST", "/api/v1/targets/wiki/install-command", nil); code != 403 {
		t.Fatalf("install command by a token: %d", code)
	}
	rec := adm.raw("POST", "/api/v1/targets/wiki/install-command", nil)
	if rec.Code != 201 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("install command: %d %s", rec.Code, rec.Body)
	}
	var ic struct{ Command string }
	json.Unmarshal(rec.Body.Bytes(), &ic)
	lines := strings.Split(ic.Command, "\n")
	tokenAt := slices.Index(lines, "cat > /etc/plumb-agent/enroll.token <<'PLUMB_TOKEN'")
	if tokenAt < 0 || lines[tokenAt+2] != "PLUMB_TOKEN" || len(lines[tokenAt+1]) < 32 {
		t.Fatalf("command:\n%s", ic.Command)
	}
	token := lines[tokenAt+1]
	for i, l := range lines {
		if i != tokenAt+1 && strings.Contains(l, token) {
			t.Fatalf("the token is on line %d: %q", i, l)
		}
	}
	if !strings.Contains(ic.Command, adoptCA) || !strings.Contains(ic.Command, "'https://plumb.lan:8444/agent/v1/install.sh'") {
		t.Fatalf("command:\n%s", ic.Command)
	}
	// A2 of #299: curl trusts only the Plumb CA (--capath to a dir without
	// hashed names replaces the system store) and only https.
	curl := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "curl ") {
			curl = l
		}
	}
	for _, flag := range []string{"--proto =https", `--capath "$d"`, `--cacert "$d/ca.pem"`} {
		if !strings.Contains(curl, flag) {
			t.Fatalf("curl without %q: %s", flag, curl)
		}
	}
}

// D-395 through the API: a refused adopt Run leaves a not-adopted Target;
// the adopt route under the guest's new name replaces it in one Plan.
func TestAdoptAPINotAdopted(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	rc := &recorder{t: t}
	vmid, digest := e.person("wiki")
	_, body := rc.do(adm, "POST", "/api/v1/targets/wiki/adopt", "/targets/{name}/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": digest})
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	e.drv.RenameGuest(driver.GuestID(strconv.Itoa(vmid)), "wiki-new")
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	code, body := rc.do(adm, "GET", "/api/v1/targets/wiki", "/targets/{name}", nil)
	if code != 200 || !strings.Contains(string(body), `"not_adopted":true`) {
		t.Fatalf("stale target: %d %s", code, body)
	}
	nu, _ := e.drv.InspectGuest(e.ctx, driver.GuestID(strconv.Itoa(vmid)))
	code, body = rc.do(adm, "POST", "/api/v1/targets/wiki-new/adopt", "/targets/{name}/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": nu.Digest})
	if code != 201 || !strings.Contains(string(body), `"wiki"`) {
		t.Fatalf("adopt again: %d %s", code, body)
	}
	json.Unmarshal(body, &p)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	if code, _ := adm.do("GET", "/api/v1/targets/wiki", nil); code != 404 {
		t.Fatalf("the stale Target stays: %d", code)
	}
	code, body = rc.do(adm, "GET", "/api/v1/targets/wiki-new", "/targets/{name}", nil)
	if code != 200 || strings.Contains(string(body), `"not_adopted":true`) || !strings.Contains(string(body), `"adopted":true`) {
		t.Fatalf("new target: %d %s", code, body)
	}
}
