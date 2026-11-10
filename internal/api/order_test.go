package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Blocks of the order tests (T-044): a small nginx, and a site that comes
// after its package and restarts its service (D-167, D-172).
const (
	orderNginx = `{"resources":[{"package":{"name":"nginx"}},{"file":{"path":"/etc/nginx/default","content":"d"}},` +
		`{"service":{"name":"nginx","restart_on":["file:/etc/nginx/default"]}}]}`
	orderSite = `{"params":{"type":"object","properties":{"name":{"type":"string","default":"site"},"text":{"type":"string","default":"s"}}},` +
		`"after":["package:nginx"],"restarts":["service:nginx"],"resources":[` +
		`{"file":{"path":"/etc/nginx/{{ .name }}","content":"{{ .text }}"}},` +
		`{"symlink":{"path":"/etc/nginx/on-{{ .name }}","target":"/etc/nginx/{{ .name }}"}}]}`
)

func orderTarget(name, blocks string) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[`+blocks+`]}`)
}

type orderPlan struct {
	ID      int64
	Targets []struct {
		Target    string
		Resources []struct {
			Resource   string
			Action     string
			Unverified bool
			Changes    []wire.Change
			After      []string
			RestartBy  []string `json:"restart_by"`
		}
	}
}

// fields returns after and restart_by of one resource of the Plan.
func (p orderPlan) fields(t *testing.T, id string) (after, restartBy []string) {
	t.Helper()
	for _, tp := range p.Targets {
		for _, r := range tp.Resources {
			if r.Resource == id {
				return r.After, r.RestartBy
			}
		}
	}
	t.Fatalf("the Plan has no resource %s: %+v", id, p)
	return nil, nil
}

func (e *env) orderPropose(c *client, k *contract, reason string, docs ...state.Doc) orderPlan {
	e.t.Helper()
	rec := c.raw("POST", "/api/v1/plans", map[string]any{"reason": reason, "put": docs})
	if rec.Code != 201 {
		e.t.Fatalf("%s: %d %s", reason, rec.Code, rec.Body.Bytes())
	}
	k.check(e.t, recorded{"POST", "/plans", rec.Code, "application/json", rec.Body.Bytes()})
	var p orderPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	// The stored Plan gives the same fields.
	get := c.raw("GET", "/api/v1/plans/"+itoa(p.ID), nil)
	k.check(e.t, recorded{"GET", "/plans/{id}", get.Code, "application/json", get.Body.Bytes()})
	var stored orderPlan
	json.Unmarshal(get.Body.Bytes(), &stored)
	if !reflect.DeepEqual(p.Targets, stored.Targets) {
		e.t.Fatalf("%s: GET gives other targets than POST:\n%+v\n%+v", reason, stored.Targets, p.Targets)
	}
	return p
}

func (e *env) orderApply(c *client, p orderPlan, target string) {
	e.t.Helper()
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	run, _ := e.st.LastRunOfTarget(e.ctx, target)
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		e.t.Fatalf("run %s: %+v", run.Status, steps)
	}
}

// D-171: the Plan shows after on the resource that waits, and the
// resources that cause a restart. An enrolled Target gets nginx and two
// sites; then one site changes.
func TestPlanShowsAfterAndRestartBy(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	const uses = `{"use":"motd"},{"use":"site","id":"site-a","params":{"name":"a"}},{"use":"nginx"},{"use":"site","id":"site-b","params":{"name":"b"}}`
	p := e.orderPropose(adm, k, "nginx and two sites",
		doc(state.KindBlock, "nginx", orderNginx), doc(state.KindBlock, "site", orderSite), orderTarget("web1", uses))

	for id, want := range map[string][]string{
		"file:/etc/nginx/a":       {"package:nginx"},
		"file:/etc/nginx/b":       {"package:nginx"},
		"symlink:/etc/nginx/on-a": nil, // only the first resource of a use carries it
		"package:nginx":           nil,
		"service:nginx":           nil,
	} {
		if after, _ := p.fields(t, id); !reflect.DeepEqual(after, want) {
			t.Errorf("%s: after = %v, want %v", id, after, want)
		}
	}
	// In restart_on order: the default site, then the uses in Target order.
	wantBy := []string{"file:/etc/nginx/default", "file:/etc/nginx/a", "symlink:/etc/nginx/on-a", "file:/etc/nginx/b", "symlink:/etc/nginx/on-b"}
	if _, by := p.fields(t, "service:nginx"); !reflect.DeepEqual(by, wantBy) {
		t.Errorf("service:nginx: restart_by = %v, want %v", by, wantBy)
	}
	if _, by := p.fields(t, "file:/etc/nginx/a"); by != nil {
		t.Errorf("a resource that is no service has restart_by %v", by)
	}
	e.orderApply(adm, p, "web1")

	// One site changes: the service has only the restart, and the Plan
	// names the one file that causes it.
	p = e.orderPropose(adm, k, "new text for site a", orderTarget("web1",
		strings.Replace(uses, `"params":{"name":"a"}`, `"params":{"name":"a","text":"new"}`, 1)))
	if len(p.Targets) != 1 || len(p.Targets[0].Resources) != 2 {
		t.Fatalf("want the file and the service in the Plan: %+v", p)
	}
	if after, by := p.fields(t, "service:nginx"); after != nil || !reflect.DeepEqual(by, []string{"file:/etc/nginx/a"}) {
		t.Errorf("service:nginx: after %v, restart_by %v; want only file:/etc/nginx/a", after, by)
	}
	if after, by := p.fields(t, "file:/etc/nginx/a"); !reflect.DeepEqual(after, []string{"package:nginx"}) || by != nil {
		t.Errorf("file:/etc/nginx/a: after %v, restart_by %v", after, by)
	}
	e.orderApply(adm, p, "web1")
}

// A new Target: every resource is unverified, the site files carry after,
// and no restart is planned, so nothing has restart_by (D-066, D-171).
func TestPlanShowsAfterOnANewTarget(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	p := e.orderPropose(adm, k, "new target", doc(state.KindBlock, "nginx", orderNginx), doc(state.KindBlock, "site", orderSite),
		orderTarget("web2", `{"use":"site","id":"site-a","params":{"name":"a"}},{"use":"nginx"}`))
	if after, _ := p.fields(t, "file:/etc/nginx/a"); !reflect.DeepEqual(after, []string{"package:nginx"}) {
		t.Errorf("after = %v", after)
	}
	for _, tp := range p.Targets {
		for _, r := range tp.Resources {
			if r.RestartBy != nil {
				t.Errorf("%s has restart_by %v on a new target", r.Resource, r.RestartBy)
			}
			if r.Resource != "guest:config" && !r.Unverified {
				t.Errorf("%s is not unverified", r.Resource)
			}
		}
	}
}

// Review S2: restarts must keep restart_on in the form that the planned
// restart of D-160 reads. nginx is in place and does not differ; a Block
// with a new secret binding restarts it. The agent does not get that
// resource before the approval (D-156), so only the controller can plan
// the restart. Without it the apply is refused as not planned.
func TestRestartsWithANewSecretBinding(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "order-canary"})
	e.orderApply(adm, e.orderPropose(adm, k, "nginx", doc(state.KindBlock, "nginx", orderNginx),
		orderTarget("web1", `{"use":"motd"},{"use":"nginx"}`)), "web1")

	env := doc(state.KindBlock, "site-env", `{"params":{"type":"object","required":["password"],"properties":{"password":{"type":"string","writeOnly":true}}},`+
		`"after":["package:nginx"],"restarts":["service:nginx"],"resources":[{"file":{"path":"/etc/nginx/site.env","content":"PASSWORD={{ .password }}\n","mode":"0600"}}]}`)
	p := e.orderPropose(adm, k, "bind", env,
		orderTarget("web1", `{"use":"motd"},{"use":"nginx"},{"use":"site-env","params":{"password":{"secret":"x"}}}`))
	var service, file bool
	for _, tp := range p.Targets {
		for _, r := range tp.Resources {
			switch r.Resource {
			case "file:/etc/nginx/site.env":
				file = true
				if !r.Unverified || !reflect.DeepEqual(r.After, []string{"package:nginx"}) {
					t.Errorf("the file with the new binding: %+v", r)
				}
			case "service:nginx":
				service = true
				if len(r.Changes) != 1 || r.Changes[0].Attr != wire.AttrRestart || r.Changes[0].Risk != wire.RiskDisruptive ||
					r.Unverified || r.Action != "restart" || !reflect.DeepEqual(r.RestartBy, []string{"file:/etc/nginx/site.env"}) {
					t.Errorf("service:nginx must show one planned restart by the file: %+v", r)
				}
			}
		}
	}
	if !file || !service {
		t.Fatalf("the Plan needs the file and the planned restart of service:nginx: %+v", p)
	}
	e.orderApply(adm, p, "web1")
	e.waitGuest("web1", "file:/etc/nginx/site.env", "PASSWORD=order-canary\n")
}

// D-168, review S3: a revision from before the save-time cycle check is in
// the store. The controller serves it and warns once; a proposal that
// leaves the cycle in is refused and names the Target; a proposal that
// repairs it passes.
func TestStoredCycleIsServedAndMustBeRepaired(t *testing.T) {
	var logs bytes.Buffer
	envLog = slog.New(slog.NewTextHandler(&logs, nil))
	defer func() { envLog = quiet }()
	e := newEnv(t, true)
	envLog = quiet
	adm := e.admin()

	target := doc(state.KindTarget, "old1", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"svc"}]}`)
	var rows []store.DocRow
	for _, d := range []state.Doc{
		doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec), target,
		// The service is before the file that restarts it (D-055).
		doc(state.KindBlock, "svc", `{"resources":[{"service":{"name":"x","restart_on":["file:/late"]}},{"file":{"path":"/late","content":"l"}}]}`),
	} {
		body, err := state.Canonical(d)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, store.DocRow{Kind: d.Kind, Name: d.Name, Hash: state.Hash(body), Body: body})
	}
	if _, err := e.st.CommitRevision(e.ctx, store.NewRevision{Author: "system", Reason: "from before D-168", Docs: rows}); err != nil {
		t.Fatal(err)
	}

	if code, body := adm.do("GET", "/api/v1/targets", nil); code != 200 || !bytes.Contains(body, []byte(`"old1"`)) {
		t.Fatalf("the controller must serve the stored state: %d %s", code, body)
	}
	const cycle = "apply order has a cycle: service:x"
	for i := 0; i < 2; i++ {
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "other work",
			"put": []state.Doc{doc(state.KindBlock, "extra", `{"resources":[{"file":{"path":"/e","content":"e"}}]}`)}})
		if code != 422 || !bytes.Contains(body, []byte(cycle)) || !bytes.Contains(body, []byte(`Target \"old1\"`)) {
			t.Fatalf("a proposal that leaves the cycle in: %d %s", code, body)
		}
	}
	if n := strings.Count(logs.String(), "target=old1"); n != 1 || !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), cycle) {
		t.Errorf("want one warning for old1 with the cycle, got %d:\n%s", n, logs.String())
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "repair",
		"put": []state.Doc{doc(state.KindBlock, "svc", `{"resources":[{"file":{"path":"/late","content":"l"}},{"service":{"name":"x","restart_on":["file:/late"]}}]}`)}})
	if code != 201 {
		t.Fatalf("the proposal that repairs the cycle: %d %s", code, body)
	}
}
