package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// appBlock is a Block with a secret parameter. secretIn names the file
// that gets the secret; the other file is plain.
func appBlock(secretIn string) state.Doc {
	file := func(path string) map[string]any {
		content := "PLAIN=1\n"
		if path == secretIn {
			content = "DB_PASSWORD={{ .db_password }}\n"
		}
		return map[string]any{"file": map[string]any{"path": path, "content": content, "mode": "0600"}}
	}
	spec, _ := json.Marshal(map[string]any{
		"params": map[string]any{"type": "object", "required": []any{"db_password"}, "properties": map[string]any{
			"db_password": map[string]any{"type": "string", "writeOnly": true}}},
		"resources": []any{file("/etc/app.env"), file("/etc/other.env")},
	})
	return state.Doc{Kind: state.KindBlock, Name: "app", Spec: spec}
}

func appTarget(name, secretName string) state.Doc {
	spec, _ := json.Marshal(map[string]any{"type": "vm", "image": "ubuntu",
		"guest":  map[string]any{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0"},
		"blocks": []any{map[string]any{"use": "app", "params": map[string]any{"db_password": map[string]any{"secret": secretName}}}}})
	return state.Doc{Kind: state.KindTarget, Name: name, Spec: spec}
}

func (e *env) planID(body []byte) string {
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	return itoa(p.ID)
}

func (e *env) waitGuest(target, id, want string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := e.agents[target].Guest(id)["content"].(string); got == want {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s %s: content %v, want %q", target, id, e.agents[target].Guest(id)["content"], want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The T-029 slice test with a canary: create a target with a secret,
// apply, change the secret, plan, apply, drift on the sensitive
// attribute, revert. The canary values are nowhere but on the guest.
func TestSecretSlice(t *testing.T) {
	var logs bytes.Buffer
	envLog = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	defer func() { envLog = quiet }()
	e := newEnv(t, true)
	adm := e.admin()
	canary1, canary2 := "canary-one-7f3a9c", "canary-two-51be0d"

	// A missing secret is 422 with the pointer of the parameter.
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
	var prob struct {
		Errors  []fieldError
		Secrets []string
	}
	json.Unmarshal(body, &prob)
	if code != 422 || problemType(body) != "validation" || len(prob.Secrets) != 1 || prob.Secrets[0] != "app-db" ||
		len(prob.Errors) != 1 || prob.Errors[0].Pointer != "/put/2/spec/blocks/0/params/db_password" {
		t.Fatalf("missing secret: %d %s", code, body)
	}

	// Create: the new target plans the sensitive attribute without a value.
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": canary1})
	e.agent("web1")
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
	if code != 201 || !strings.Contains(string(body), `"secrets":[{"name":"app-db","version":1}]`) ||
		!strings.Contains(string(body), `{"attr":"content","sensitive":true,"risk":"disruptive"}`) {
		t.Fatalf("create plan: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+canary1+"\n")

	// A new value changes no guest; a Plan for used_by sends it.
	code, body = adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": canary2})
	if code != 200 || !strings.Contains(string(body), `"used_by":["web1"]`) {
		t.Fatalf("put: %d %s", code, body)
	}
	time.Sleep(50 * time.Millisecond)
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+canary1+"\n")
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "rotate", "targets": []string{"web1"}})
	if code != 201 || !strings.Contains(string(body), `{"attr":"content","sensitive":true`) || !strings.Contains(string(body), `"human_only":[]`) {
		t.Fatalf("rotate plan: %d %s", code, body)
	}
	// Only a new value over an existing binding: an apply token approves.
	applyTok := e.token(TierApply, wire.RiskDisruptive)
	if code, body := applyTok.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("token approves a rotation: %d %s", code, body)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+canary2+"\n")

	// Drift on the sensitive attribute, then revert.
	e.agents["web1"].Change(e.ctx, "file:/etc/app.env", map[string]any{"path": "/etc/app.env", "content": "edited " + canary1, "mode": "0600"})
	var issueID string
	for i := 0; i < 500 && issueID == ""; i++ {
		var list struct{ Items []struct{ ID int64 } }
		_, b := adm.do("GET", "/api/v1/issues?status=open", nil)
		json.Unmarshal(b, &list)
		if len(list.Items) > 0 {
			issueID = itoa(list.Items[0].ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if issueID == "" {
		t.Fatal("no drift issue")
	}
	if _, b := adm.do("GET", "/api/v1/issues/"+issueID, nil); !strings.Contains(string(b), `"sensitive":true`) {
		t.Errorf("issue: %s", b)
	}
	code, body = adm.do("POST", "/api/v1/issues/"+issueID+"/resolve", map[string]string{"action": "revert"})
	if code != 201 {
		t.Fatalf("revert: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve revert: %d %s", code, body)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+canary2+"\n")

	// The diff of the revision shows the reference only.
	_, body = adm.do("GET", "/api/v1/revisions/1/diff", nil)
	if !strings.Contains(string(body), `"db_password":{"secret":"app-db"}`) {
		t.Errorf("diff: %s", body)
	}
	// In use: 409 with the target.
	if code, body := adm.do("DELETE", "/api/v1/secrets/app-db", nil); code != 409 || !strings.Contains(string(body), `"targets":["web1"]`) {
		t.Errorf("delete in use: %d %s", code, body)
	}
	// Read every list once more, so the scan covers them.
	for _, p := range []string{"/api/v1/plans", "/api/v1/runs", "/api/v1/issues", "/api/v1/targets/web1", "/api/v1/secrets",
		"/api/v1/revisions", "/api/v1/revisions/2/diff"} {
		adm.do("GET", p, nil)
	}
	for _, r := range []string{"1", "2", "3"} {
		adm.do("GET", "/api/v1/runs/"+r, nil)
		adm.do("GET", "/api/v1/plans/"+r, nil)
	}
	e.noValue(&logs, canary1, canary2)
}

// noValue scans the database files, all events, all API answers and the
// log for the values and for marker characters in answers.
// rawScanMin is the shortest needle that noValue looks for in the raw
// database files (T-213). The files hold random bytes (sealed values,
// nonces, keyed hashes): a k-byte needle occurs by chance in an n-byte
// file with a chance of about n/256^k, that is 25 % for 3 bytes in a
// 4 MiB WAL, but 2e-13 for 8 bytes. A shorter value is looked for in the
// decoded text values of every table instead (textValuesHold), and, like
// every value, in the events, the API answers and the log.
const rawScanMin = 8

func (e *env) noValue(logs *bytes.Buffer, values ...string) {
	e.t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.dir, "plumb.db*"))
	if len(files) == 0 {
		e.t.Fatal("no database file")
	}
	var short []string
	for _, v := range values {
		if len(v) < rawScanMin {
			short = append(short, v)
		}
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, v := range values {
			if len(v) >= rawScanMin && bytes.Contains(b, []byte(v)) {
				e.t.Errorf("%s holds a secret value", filepath.Base(f))
			}
		}
	}
	if len(short) > 0 {
		if where := e.textValuesHold(short); where != "" {
			e.t.Errorf("the database text value %s holds a short secret value", where)
		}
	}
	evs, err := e.st.EventsAfter(e.ctx, 0, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, ev := range evs {
		for _, v := range values {
			if bytes.Contains(ev.Data, []byte(v)) {
				e.t.Errorf("event %s holds a secret value", ev.Type)
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range e.seen {
		for _, v := range values {
			if bytes.Contains(b, []byte(v)) {
				e.t.Errorf("an answer holds a secret value: %.120s", b)
			}
		}
		if bytes.ContainsRune(b, state.MarkStart) || bytes.ContainsRune(b, state.MarkEnd) {
			e.t.Errorf("an answer holds a secret marker: %.120s", b)
		}
	}
	if logs != nil {
		for _, v := range values {
			if strings.Contains(logs.String(), v) {
				e.t.Errorf("the log holds a secret value")
			}
		}
	}
}

// A buggy agent that reports clear values: the controller still stores none.
func TestSecretLeakyAgent(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "leak-canary-1"})
	a := e.agent("web1")
	a.LeakSensitive = true
	_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=leak-canary-1\n")
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "leak-canary-2"})
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "rotate", "targets": []string{"web1"}})
	if code != 201 {
		t.Fatalf("rotate: %d %s", code, body)
	}
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=leak-canary-2\n")
	a.Change(e.ctx, "file:/etc/app.env", map[string]any{"path": "/etc/app.env", "content": "x", "mode": "0600"})
	time.Sleep(200 * time.Millisecond)
	adm.do("GET", "/api/v1/issues", nil)
	adm.do("GET", "/api/v1/runs/2", nil)
	e.noValue(nil, "leak-canary-1", "leak-canary-2")
}

// D-099: a token cannot send a secret to a new place.
func TestSecretHumanOnly(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "human-only-canary"})
	planTok := e.token(TierPlan, "")
	applyTok := e.token(TierApply, wire.RiskDisruptive)
	refused := func(name string, code int, body []byte) {
		t.Helper()
		if code != 201 || !strings.Contains(string(body), `"human_only":["secret-binding"]`) {
			t.Fatalf("%s: plan %d %s", name, code, body)
		}
		if c, b := applyTok.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); c != 403 || problemType(b) != "human-required" {
			t.Fatalf("%s: token approve %d %s", name, c, b)
		}
	}
	// An existing target gets a secret.
	code, body := planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "bind", "put": []state.Doc{appBlock("/etc/app.env"), appTarget("web1", "x")}})
	refused("existing target", code, body)
	if c, b := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); c != 202 {
		t.Fatalf("human approve: %d %s", c, b)
	}
	e.eng.Wait()
	bound, _ := e.st.Head(e.ctx)
	// A new target with a secret.
	code, body = planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "new", "put": []state.Doc{appTarget("web3", "x")}})
	refused("new target", code, body)
	adm.do("DELETE", "/api/v1/plans/"+e.planID(body), nil)
	// The Block moves the secret into another resource.
	code, body = planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "move", "put": []state.Doc{appBlock("/etc/other.env")}})
	refused("other resource", code, body)
	adm.do("DELETE", "/api/v1/plans/"+e.planID(body), nil)
	// Rollback to a revision with the binding, from a head without it.
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "unbind", "put": []state.Doc{targetDoc("web1", "hello")}})
	if code != 201 {
		t.Fatalf("unbind: %d %s", code, body)
	}
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait()
	code, body = planTok.do("POST", "/api/v1/revisions/"+itoa(bound.ID)+"/rollback", map[string]string{"reason": "back"})
	refused("rollback", code, body)
	e.noValue(nil, "human-only-canary")
}

// D-089: the approver approves one value; a Run sends exactly that one.
func TestSecretVersions(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "v1"})
	e.agent("web1")
	_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
	adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=v1\n")

	// PUT between plan and approve: plan-stale.
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "v2"})
	_, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "rotate", "targets": []string{"web1"}})
	id := e.planID(body)
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "v3"})
	if code, body := adm.do("POST", "/api/v1/plans/"+id+"/approve", nil); code != 409 || problemType(body) != "plan-stale" {
		t.Fatalf("approve after a new value: %d %s", code, body)
	}

	// Delete between approve and apply: a new target waits for its agent;
	// the secret goes away meanwhile (delete and Plan are not atomic).
	adm.do("PUT", "/api/v1/secrets/gone", map[string]string{"value": "gone-canary"})
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "web2", "put": []state.Doc{appTarget("web2", "gone")}})
	if code != 201 {
		t.Fatalf("web2: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve web2: %d %s", code, body)
	}
	e.sec.SetUsage(nil)
	if err := e.sec.Delete(e.ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	a := e.agent("web2")
	e.eng.Wait()
	run, err := e.st.LastRunOfTarget(e.ctx, "web2")
	if err != nil || run.Status != "failed" {
		t.Fatalf("run: %+v %v", run, err)
	}
	steps, _ := e.st.Steps(e.ctx, run.ID)
	failed := 0
	for _, s := range steps {
		if s.Resource != wire.GuestConfigID {
			if s.Status != "failed" || !strings.Contains(s.Output, "secret") {
				t.Errorf("step %s: %s %q", s.Resource, s.Status, s.Output)
			}
			failed++
		}
	}
	if failed != 2 || a.Guest("file:/etc/app.env") != nil {
		t.Fatalf("failed steps %d, guest %v", failed, a.Guest("file:/etc/app.env"))
	}
	for _, c := range a.Commands() {
		if c == wire.CommandApply || c == wire.CommandWatch {
			t.Errorf("the agent got a %s without its secret", c)
		}
	}

	// A new value between approve and apply: the Run sends no other value
	// than the planned one, so it fails.
	adm.do("PUT", "/api/v1/secrets/rot", map[string]string{"value": "rot-v1"})
	_, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "web3", "put": []state.Doc{appTarget("web3", "rot")}})
	if code, body := adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil); code != 202 {
		t.Fatalf("approve web3: %d %s", code, body)
	}
	if code, _ := adm.do("PUT", "/api/v1/secrets/rot", map[string]string{"value": "rot-v2"}); code != 200 {
		t.Fatal("put rot")
	}
	a3 := e.agent("web3")
	e.eng.Wait()
	if run, _ := e.st.LastRunOfTarget(e.ctx, "web3"); run.Status != "failed" {
		t.Fatalf("run with a changed secret: %+v", run)
	}
	if got := a3.Guest("file:/etc/app.env"); got != nil {
		t.Fatalf("the guest got a value that nobody approved: %v", got)
	}
}

// Review must 2: an agent error that holds only the secret, on another
// resource of the target, is masked before it is stored. Also a value
// shorter than 4 bytes. The first canary test missed this: its agent never
// printed a secret in an Output or an Error.
func TestSecretMaskedInAgentErrors(t *testing.T) {
	// The second value is one 3-byte character.
	for _, value := range []string{"pw-canary-9x1", "ⓩ"} {
		t.Run(value, func(t *testing.T) {
			e := newEnv(t, true)
			adm := e.admin()
			adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": value})
			a := e.agent("web1")
			a.FailApply("file:/etc/other.env", "write failed: "+value)
			a.FailApply("file:/etc/app.env", value)
			_, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "app",
				"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}})
			adm.do("POST", "/api/v1/plans/"+e.planID(body)+"/approve", nil)
			e.eng.Wait()
			run, _ := e.st.LastRunOfTarget(e.ctx, "web1")
			steps, _ := e.st.Steps(e.ctx, run.ID)
			masked := 0
			for _, s := range steps {
				if strings.Contains(s.Output, "[redacted]") {
					masked++
				}
			}
			if masked != 2 {
				t.Errorf("masked steps %d: %+v", masked, steps)
			}
			adm.do("GET", "/api/v1/runs/"+itoa(run.ID), nil)
			e.noValue(nil, value)
		})
	}
}

// appServiceBlock is appBlock plus a service that restarts on the file
// with the secret.
func appServiceBlock() state.Doc {
	d := appBlock("/etc/app.env")
	var spec map[string]any
	json.Unmarshal(d.Spec, &spec)
	spec["resources"] = append(spec["resources"].([]any),
		map[string]any{"service": map[string]any{"name": "app", "restart_on": []any{"file:/etc/app.env"}}})
	d.Spec, _ = json.Marshal(spec)
	return d
}

// planBodiesHold reports whether any command body after the first skip
// ones holds the value.
func bodiesHold(a interface{ Bodies() [][]byte }, skip int, value string) bool {
	for _, b := range a.Bodies()[skip:] {
		if bytes.Contains(b, []byte(value)) {
			return true
		}
	}
	return false
}

type planShape struct {
	ID        int64
	HumanOnly []string `json:"human_only"`
	Targets   []struct {
		Target    string
		Resources []struct {
			Resource   string
			Unverified bool
			Changes    []wire.Change
		}
	}
}

func (p planShape) resource(id string) (unverified, restart, found bool) {
	for _, t := range p.Targets {
		for _, r := range t.Resources {
			if r.Resource == id {
				for _, c := range r.Changes {
					restart = restart || c.Attr == wire.AttrRestart
				}
				return r.Unverified, restart, true
			}
		}
	}
	return false, false, false
}

// D-156: before the approval, no command carries the value of a new
// binding to the guest.
func TestSecretNewBindingNotObserved(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	a := e.agents["web1"]
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "new-binding-canary"})
	adm.do("PUT", "/api/v1/secrets/y", map[string]string{"value": "other-name-canary"})
	planTok := e.token(TierPlan, "")
	propose := func(c *client, docs ...state.Doc) planShape {
		t.Helper()
		code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "bind", "put": docs})
		if code != 201 {
			t.Fatalf("plan: %d %s", code, body)
		}
		var p planShape
		json.Unmarshal(body, &p)
		return p
	}

	// 1. An existing online target gets a secret: the resource is left out
	// of the planning observe and planned as unverified.
	skip := len(a.Bodies())
	p := propose(planTok, appBlock("/etc/app.env"), appTarget("web1", "x"))
	if bodiesHold(a, skip, "new-binding-canary") {
		t.Fatal("a command before the approval carried the value of a new binding")
	}
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || !u || len(p.HumanOnly) != 1 || p.HumanOnly[0] != "secret-binding" {
		t.Fatalf("plan: %+v", p)
	}
	if u, _, ok := p.resource("file:/etc/other.env"); !ok || u {
		t.Fatalf("the plain file is observed: %+v", p)
	}
	if c, b := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); c != 202 {
		t.Fatalf("approve: %d %s", c, b)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=new-binding-canary\n")

	// 2. Another secret name on the same resource is a new binding too.
	skip = len(a.Bodies())
	p = propose(planTok, appTarget("web1", "y"))
	if bodiesHold(a, skip, "other-name-canary") {
		t.Fatal("a command before the approval carried the value of another secret name")
	}
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || !u || len(p.HumanOnly) != 1 {
		t.Fatalf("other name: %+v", p)
	}
	adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil)

	// 4. A rotation over the existing binding is observed with the value
	// (the known limit of D-156) and is not human-only.
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "rotated-canary"})
	skip = len(a.Bodies())
	code, body := planTok.do("POST", "/api/v1/plans", map[string]any{"reason": "rotate", "targets": []string{"web1"}})
	p = planShape{}
	json.Unmarshal(body, &p)
	if code != 201 || len(p.HumanOnly) != 0 || !bodiesHold(a, skip, "rotated-canary") {
		t.Fatalf("rotation: %d %s", code, body)
	}
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || u {
		t.Fatalf("rotation is not unverified: %+v", p)
	}
}

// D-156 point 4: a service that restarts on a left-out resource gets a
// planned restart, so the apply does not refuse it.
func TestSecretNewBindingRestart(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "restart-canary"})
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "bind", "put": []state.Doc{appServiceBlock(), appTarget("web1", "x")}})
	var p planShape
	json.Unmarshal(body, &p)
	if _, restart, ok := p.resource("service:app"); code != 201 || !ok || !restart {
		t.Fatalf("no planned restart: %d %s", code, body)
	}
	if c, b := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); c != 202 {
		t.Fatalf("approve: %d %s", c, b)
	}
	e.eng.Wait()
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=restart-canary\n")
	run, _ := e.st.LastRunOfTarget(e.ctx, "web1")
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		t.Fatalf("run %s: %+v", run.Status, steps)
	}
}

// D-156, D-160: the service is in place and does not differ. Only the
// controller can plan its restart, because the agent did not get the
// resource that causes it. The Plan shows the restart alone, and the
// apply does not refuse it.
func TestSecretNewBindingRestartOfUnchangedService(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	adm.do("PUT", "/api/v1/secrets/x", map[string]string{"value": "first-canary"})
	adm.do("PUT", "/api/v1/secrets/y", map[string]string{"value": "second-canary"})
	apply := func(reason string, docs ...state.Doc) planShape {
		t.Helper()
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": reason, "put": docs})
		var p planShape
		json.Unmarshal(body, &p)
		if code != 201 {
			t.Fatalf("%s: %d %s", reason, code, body)
		}
		if c, b := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); c != 202 {
			t.Fatalf("%s approve: %d %s", reason, c, b)
		}
		e.eng.Wait()
		return p
	}
	apply("bind x", appServiceBlock(), appTarget("web1", "x"))
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=first-canary\n")

	p := apply("bind y", appTarget("web1", "y"))
	if u, _, ok := p.resource("file:/etc/app.env"); !ok || !u {
		t.Fatalf("the file with the new binding is not unverified: %+v", p)
	}
	for _, tp := range p.Targets {
		for _, r := range tp.Resources {
			if r.Resource != "service:app" {
				continue
			}
			if len(r.Changes) != 1 || r.Changes[0].Attr != wire.AttrRestart || r.Changes[0].Risk != wire.RiskDisruptive || r.Unverified {
				t.Fatalf("service:app must show one disruptive restart: %+v", r)
			}
		}
	}
	if _, restart, ok := p.resource("service:app"); !ok || !restart {
		t.Fatalf("no planned restart for the unchanged service: %+v", p)
	}
	e.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD=second-canary\n")
	run, _ := e.st.LastRunOfTarget(e.ctx, "web1")
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		t.Fatalf("run %s: %+v", run.Status, steps)
	}
}

// textValuesHold reports the first table and column whose TEXT value
// holds one of needles, through a read-only connection to the env's
// database ("" when none does). INTEGER and REAL values are scanned in
// their decimal text (#320 n2: a short numeric secret). BLOB values
// (sealed data, nonces) are skipped: their random bytes are what the raw
// scan cannot tell apart.
func (e *env) textValuesHold(needles []string) string {
	e.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, "plumb.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		e.t.Fatal(err)
	}
	defer db.Close()
	var tables []string
	rows, err := db.QueryContext(e.ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		e.t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			e.t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		e.t.Fatal(err)
	}
	if len(tables) < 10 {
		e.t.Fatalf("only %d tables read: the text scan would prove nothing", len(tables))
	}
	for _, tbl := range tables {
		rows, err := db.QueryContext(e.ctx, `SELECT * FROM "`+tbl+`"`)
		if err != nil {
			e.t.Fatal(err)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				e.t.Fatal(err)
			}
			for i, v := range vals {
				var text string
				switch v := v.(type) {
				case string:
					text = v
				case int64:
					text = strconv.FormatInt(v, 10)
				case float64:
					text = strconv.FormatFloat(v, 'g', -1, 64)
				default:
					continue
				}
				for _, n := range needles {
					if strings.Contains(text, n) {
						rows.Close()
						return tbl + "." + cols[i]
					}
				}
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			e.t.Fatal(err)
		}
	}
	return ""
}
