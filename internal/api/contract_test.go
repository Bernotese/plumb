package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// contract holds api/openapi.yaml as JSON and compiles its schemas.
type contract struct {
	doc  map[string]any
	c    *jsonschema.Compiler
	mu   sync.Mutex
	seen map[string]*jsonschema.Schema
}

const specURL = "mem:///api/openapi.json"

func loadContract(t *testing.T) *contract {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var y any
	if err := yaml.Unmarshal(raw, &y); err != nil {
		t.Fatal(err)
	}
	js, err := json.Marshal(y)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(specURL, doc); err != nil {
		t.Fatal(err)
	}
	// The document schemas the spec refers to (D-043).
	for _, kind := range []string{"image", "block", "target"} {
		b, err := os.ReadFile(filepath.Join("../state/schema", kind+".json"))
		if err != nil {
			t.Fatal(err)
		}
		d, _ := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err := c.AddResource("mem:///internal/state/schema/"+kind+".json", d); err != nil {
			t.Fatal(err)
		}
	}
	var m map[string]any
	json.Unmarshal(js, &m)
	return &contract{doc: m, c: c, seen: map[string]*jsonschema.Schema{}}
}

func ptrEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func (k *contract) get(path ...string) (any, bool) {
	var cur any = k.doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func (k *contract) schema(t *testing.T, pointer string) *jsonschema.Schema {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	if s, ok := k.seen[pointer]; ok {
		return s
	}
	s, err := k.c.Compile(specURL + "#" + pointer)
	if err != nil {
		t.Fatalf("compile %s: %v", pointer, err)
	}
	k.seen[pointer] = s
	return s
}

// check validates one response against the operation in the spec.
func (k *contract) check(t *testing.T, r recorded) {
	t.Helper()
	name := r.method + " " + r.template + " → " + strconv.Itoa(r.status)
	op := []string{"paths", r.template, strings.ToLower(r.method)}
	if _, ok := k.get(op...); !ok {
		t.Errorf("%s: operation is not in api/openapi.yaml", name)
		return
	}
	respPath := append(op, "responses", strconv.Itoa(r.status))
	resp, ok := k.get(respPath...)
	if !ok {
		t.Errorf("%s: status is not documented", name)
		return
	}
	pointer := "/" + strings.Join(mapEscape(respPath), "/")
	if ref, ok := resp.(map[string]any)["$ref"].(string); ok {
		pointer = strings.TrimPrefix(ref, "#")
		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		resp, _ = k.get(parts...)
	}
	content, _ := resp.(map[string]any)["content"].(map[string]any)
	if len(content) == 0 {
		if len(bytes.TrimSpace(r.body)) != 0 {
			t.Errorf("%s: the spec has no body, the server sent %s", name, r.body)
		}
		return
	}
	if _, ok := content[r.ctype]; !ok {
		t.Errorf("%s: content type %q is not documented", name, r.ctype)
		return
	}
	if r.ctype == "text/event-stream" {
		return
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(r.body))
	if err != nil {
		t.Errorf("%s: body is not JSON: %s", name, r.body)
		return
	}
	s := k.schema(t, pointer+"/content/"+ptrEscape(r.ctype)+"/schema")
	if err := s.Validate(inst); err != nil {
		t.Errorf("%s: response does not match the spec:\n%v\nbody: %.400s", name, err, r.body)
	}
}

func mapEscape(p []string) []string {
	out := make([]string, len(p))
	for i, s := range p {
		out[i] = ptrEscape(s)
	}
	return out
}

type recorded struct {
	method, template string
	status           int
	ctype            string
	body             []byte
}

// TestContract runs the slice through the API and validates every
// response, and every event, with the schemas of api/openapi.yaml.
func TestContract(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	var calls []recorded
	call := func(c *client, method, path, template string, body any) []byte {
		t.Helper()
		rec := c.raw(method, path, body)
		ct, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
		calls = append(calls, recorded{method, template, rec.Code, ct, rec.Body.Bytes()})
		return rec.Body.Bytes()
	}
	id := func(body []byte) string {
		var v struct{ ID int64 }
		json.Unmarshal(body, &v)
		return strconv.FormatInt(v.ID, 10)
	}

	// Session.
	call(e.anon(), "GET", "/api/v1/session", "/session", nil)                                                          // 401
	call(e.anon(), "POST", "/api/v1/session", "/session", map[string]string{"username": "admin", "password": "wrong"}) // 401
	call(e.anon(), "POST", "/api/v1/session", "/session", []byte(`{`))                                                 // 400
	first := e.login("admin", e.password)
	call(first, "GET", "/api/v1/session", "/session", nil) // 200, must change
	call(first, "GET", "/api/v1/targets", "/targets", nil) // 403 password-change-required
	call(first, "PUT", "/api/v1/session/password", "/session/password",
		map[string]string{"current_password": e.password, "new_password": "short"}) // 422
	adm := e.admin()
	call(adm, "GET", "/api/v1/session", "/session", nil)
	tok := e.token(TierApply, wire.RiskReversible)
	call(tok, "GET", "/api/v1/session", "/session", nil)
	readTok := e.token(TierRead, "")

	// Plans: invalid, valid, approve, list.
	call(adm, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "bad", "put": []state.Doc{doc(state.KindBlock, "motd", `{"resources":[]}`)}}) // 422
	call(readTok, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "x"})                                                                     // 403
	e.agent("web1")
	created := call(adm, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "seed",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), doc(state.KindBlock, "motd", motdSpec), targetDoc("web1", "hello")}})
	p1 := id(created)
	stale := call(adm, "POST", "/api/v1/plans", "/plans", map[string]any{"reason": "other",
		"put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec)}})
	call(tok, "POST", "/api/v1/plans/"+p1+"/approve", "/plans/{id}/approve", nil) // 403 risk-too-high (disruptive)
	run := call(adm, "POST", "/api/v1/plans/"+p1+"/approve", "/plans/{id}/approve", map[string]string{"comment": "go"})
	e.eng.Wait()
	call(adm, "POST", "/api/v1/plans/"+id(stale)+"/approve", "/plans/{id}/approve", nil) // 409 stale
	call(adm, "GET", "/api/v1/plans/"+p1, "/plans/{id}", nil)
	call(adm, "GET", "/api/v1/plans/"+id(stale), "/plans/{id}", nil)
	call(adm, "GET", "/api/v1/plans/999", "/plans/{id}", nil) // 404
	call(adm, "GET", "/api/v1/plans?limit=1", "/plans", nil)
	call(adm, "GET", "/api/v1/plans?status=approved", "/plans", nil)
	call(adm, "GET", "/api/v1/runs/"+id(run), "/runs/{id}", nil)
	call(adm, "GET", "/api/v1/runs", "/runs", nil)

	// Documents and targets.
	for !e.link.Online("web1") {
		time.Sleep(10 * time.Millisecond)
	}
	call(adm, "GET", "/api/v1/images", "/images", nil)
	call(adm, "GET", "/api/v1/images/ubuntu", "/images/{name}", nil)
	call(adm, "GET", "/api/v1/images/nope", "/images/{name}", nil) // 404
	call(adm, "GET", "/api/v1/blocks", "/blocks", nil)
	call(adm, "GET", "/api/v1/blocks/motd", "/blocks/{name}", nil)
	call(adm, "GET", "/api/v1/targets", "/targets", nil)
	call(adm, "GET", "/api/v1/targets/web1", "/targets/{name}", nil)
	call(adm, "GET", "/api/v1/targets/nope", "/targets/{name}", nil) // 404

	// Drift → issue → comment → resolve → approve.
	e.agents["web1"].Change(context.Background(), "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "x", "mode": "0644"})
	var issueID string
	for i := 0; i < 500 && issueID == ""; i++ {
		var list struct{ Items []struct{ ID int64 } }
		json.Unmarshal(call(adm, "GET", "/api/v1/issues?status=open", "/issues", nil), &list)
		if len(list.Items) > 0 {
			issueID = strconv.FormatInt(list.Items[0].ID, 10)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if issueID == "" {
		t.Fatal("no issue")
	}
	call(adm, "POST", "/api/v1/issues/"+issueID+"/comments", "/issues/{id}/comments", map[string]string{"body": "seen it"})
	call(readTok, "POST", "/api/v1/issues/"+issueID+"/comments", "/issues/{id}/comments", map[string]string{"body": "x"}) // 403
	call(adm, "GET", "/api/v1/issues/"+issueID, "/issues/{id}", nil)
	resolve := call(adm, "POST", "/api/v1/issues/"+issueID+"/resolve", "/issues/{id}/resolve", map[string]string{"action": issue.ActionRevert})
	call(adm, "POST", "/api/v1/issues/"+issueID+"/resolve", "/issues/{id}/resolve", map[string]string{"action": issue.ActionRevert}) // 409
	call(adm, "POST", "/api/v1/plans/"+id(resolve)+"/approve", "/plans/{id}/approve", nil)
	e.eng.Wait()
	call(adm, "GET", "/api/v1/issues/"+issueID, "/issues/{id}", nil) // resolved
	call(adm, "GET", "/api/v1/issues?target=web1", "/issues", nil)
	call(adm, "GET", "/api/v1/issues/999", "/issues/{id}", nil) // 404

	// Re-enroll and discard.
	re := call(adm, "POST", "/api/v1/targets/web1/reenroll", "/targets/{name}/reenroll", map[string]string{"reason": "test"})
	call(adm, "POST", "/api/v1/targets/nope/reenroll", "/targets/{name}/reenroll", nil) // 404
	call(adm, "DELETE", "/api/v1/plans/"+id(re), "/plans/{id}", nil)
	call(adm, "DELETE", "/api/v1/plans/"+id(re), "/plans/{id}", nil) // 409

	// Secrets (T-028).
	call(adm, "PUT", "/api/v1/secrets/app-db", "/secrets/{name}", map[string]string{"value": "contract-value"})
	call(adm, "PUT", "/api/v1/secrets/app-db", "/secrets/{name}", map[string]string{})                 // 422
	call(adm, "PUT", "/api/v1/secrets/app-db", "/secrets/{name}", []byte(`{"value": nope}`))           // 400
	call(readTok, "PUT", "/api/v1/secrets/app-db", "/secrets/{name}", map[string]string{"value": "x"}) // 403
	call(adm, "PUT", "/api/v1/secrets/other", "/secrets/{name}", map[string]string{"value": "x"})
	call(readTok, "GET", "/api/v1/secrets", "/secrets", nil)
	call(adm, "GET", "/api/v1/secrets?limit=1", "/secrets", nil)
	e.sec.SetUsage(func(context.Context) (map[string]secret.Usage, error) {
		return map[string]secret.Usage{"app-db": {Targets: []string{"web1"}, Plans: []int64{1}}}, nil
	})
	call(adm, "DELETE", "/api/v1/secrets/app-db", "/secrets/{name}", nil) // 409
	e.sec.SetUsage(nil)
	call(adm, "DELETE", "/api/v1/secrets/app-db", "/secrets/{name}", nil)
	call(adm, "DELETE", "/api/v1/secrets/app-db", "/secrets/{name}", nil) // 404
	call(tok, "DELETE", "/api/v1/secrets/other", "/secrets/{name}", nil)  // 403
	call(e.anon(), "GET", "/api/v1/secrets", "/secrets", nil)             // 401

	// Logout.
	call(adm, "DELETE", "/api/v1/session", "/session", nil)

	for _, r := range calls {
		k.check(t, r)
	}
	if len(calls) < 40 {
		t.Errorf("only %d calls", len(calls))
	}

	// Every event that the slice emitted matches the Event schema; the type
	// enum is closed (T-017 contract check).
	evSchema := k.schema(t, "/components/schemas/Event")
	sub := e.events.Subscribe(e.ctx, 0)
	timeout := time.After(time.Second)
	types := map[string]bool{}
	for {
		select {
		case ev := <-sub.C:
			types[ev.Type] = true
			raw, _ := json.Marshal(eventView{Seq: ev.Seq, Type: ev.Type, Time: ev.Time.UTC(), Subject: ev.Subject, Actor: ev.Actor, Data: ev.Data})
			inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err := evSchema.Validate(inst); err != nil {
				t.Errorf("event does not match the spec: %s\n%v", raw, err)
			}
		case <-timeout:
			if len(types) < 6 {
				t.Errorf("few event types: %v", types)
			}
			return
		}
	}
}

// The contract check itself catches a wrong body and an undocumented
// status (so a passing TestContract means something).
func TestContractCatches(t *testing.T) {
	k := loadContract(t)
	for name, r := range map[string]recorded{
		"wrong field type":      {"GET", "/plans/{id}", 200, "application/json", []byte(`{"id":"twelve"}`)},
		"missing required":      {"GET", "/session", 200, "application/json", []byte(`{"principal":"user:admin"}`)},
		"undocumented status":   {"GET", "/images", 418, "application/json", []byte(`{}`)},
		"unknown event subject": {"GET", "/runs/{id}", 200, "application/json", []byte(`{"id":1,"plan_id":1,"revision":1,"status":"done","started_at":"2026-11-10T00:00:00Z","finished_at":null,"targets":[]}`)},
		"bad change shape":      {"GET", "/issues/{id}", 200, "application/json", []byte(`{"id":1,"target":"web1","resource":"file:/x","status":"open","risk":"read","opened_at":"2026-11-10T00:00:00Z","updated_at":"2026-11-10T00:00:00Z","changes":[{"attr":"a","sensitive":true,"from":"leak","risk":"read"}],"actions":[],"plan_id":null,"resolution":null,"comments":[]}`)},
	} {
		ft := &testing.T{}
		func() {
			defer func() { recover() }()
			k.check(ft, r)
		}()
		if !ft.Failed() {
			t.Errorf("%s: the contract check did not fail", name)
		}
	}
	bad, _ := jsonschema.UnmarshalJSON(strings.NewReader(`{"seq":1,"type":"step.applied","time":"2026-11-10T00:00:00Z","subject":"run:1","actor":"system","data":{}}`))
	if k.schema(t, "/components/schemas/Event").Validate(bad) == nil {
		t.Error("an event type outside the enum passed")
	}
}
