package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

func slice2Call(t *testing.T, c *client, method, path string, body any, want int) []byte {
	t.Helper()
	code, out := c.do(method, "/api/v1"+path, body)
	if code != want {
		t.Fatalf("%s %s: status %d, want %d (body omitted for leak safety)", method, path, code, want)
	}
	return out
}

func slice2Approve(t *testing.T, c *client, body []byte) {
	t.Helper()
	slice2Call(t, c, "POST", "/plans/"+planIDOf(body)+"/approve", nil, 202)
	c.e.eng.Wait()
}

func TestSlice2Acceptance(t *testing.T) {
	for _, leaky := range []bool{false, true} {
		t.Run(fmt.Sprintf("LeakSensitive=%t", leaky), func(t *testing.T) {
			var logs bytes.Buffer
			envLog = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			defer func() { envLog = quiet }()
			src := newEnv(t, true)
			adm := src.admin()
			ct := src.createToken(adm, map[string]any{"name": "acceptance", "tier": "plan"})
			planner := &client{e: src, bearer: ct.Bearer}
			values := []string{"s2-" + hex.EncodeToString(randBytes(t)), "s2-" + hex.EncodeToString(randBytes(t))}
			slice2Call(t, adm, "PUT", "/secrets/app-db", map[string]string{"value": values[0]}, 200)
			a := src.agent("web1")
			a.LeakSensitive = leaky
			p := slice2Call(t, planner, "POST", "/plans", map[string]any{"reason": "slice 2", "put": []state.Doc{
				doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}}, 201)
			if len(a.Bodies()) != 0 {
				t.Fatal("new target received commands before approval")
			}
			slice2Approve(t, adm, p)
			src.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+values[0]+"\n")
			src.waitWatch("web1")
			drift := func(file, content string) string {
				t.Helper()
				if err := a.Change(src.ctx, "file:"+file, map[string]any{"path": file, "content": content, "mode": "0600"}); err != nil {
					t.Fatal(err)
				}
				b := slice2Call(t, adm, "GET", "/issues?status=open", nil, 200)
				var list struct{ Items []struct{ ID int64 } }
				if err := json.Unmarshal(b, &list); err != nil || len(list.Items) != 1 {
					t.Fatal("expected one open drift issue")
				}
				return "/issues/" + itoa(list.Items[0].ID) + "/resolve"
			}
			path := drift("/etc/app.env", "manual "+values[0])
			ignored := slice2Call(t, adm, "POST", path, map[string]string{"action": "ignore_once"}, 200)
			if !bytes.Contains(ignored, []byte(`"status":"resolved"`)) {
				t.Fatal("ignore_once did not resolve")
			}
			path = drift("/etc/other.env", "different")
			p = slice2Call(t, planner, "POST", path, map[string]string{"action": "ignore_rule"}, 201)
			slice2Approve(t, adm, p)
			slice2Call(t, adm, "PUT", "/secrets/app-db", map[string]string{"value": values[1]}, 200)
			if a.Guest("file:/etc/app.env")["content"] != "manual "+values[0] {
				t.Fatal("rotation changed guest before approval")
			}
			p = slice2Call(t, planner, "POST", "/plans", map[string]any{"reason": "rotate", "targets": []string{"web1"}}, 201)
			slice2Approve(t, adm, p)
			src.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+values[1]+"\n")
			plain := slice2Call(t, adm, "GET", "/export", nil, 200)
			sealed := slice2Call(t, adm, "POST", "/export", map[string]string{"passphrase": exportPass}, 200)
			if bytes.Contains(plain, []byte("SecretBundle")) || !bytes.Contains(sealed, []byte("SecretBundle")) {
				t.Fatal("wrong export bundle selection")
			}
			dst := newEnv(t, true)
			dadm := dst.admin()
			dt := dst.createToken(dadm, map[string]any{"name": "reader", "tier": "read"})
			da := dst.agent("web1")
			da.LeakSensitive = leaky
			p = slice2Call(t, dadm, "POST", "/import", map[string]any{"reason": "restore", "content": string(sealed), "passphrase": exportPass}, 201)
			if len(da.Bodies()) != 0 {
				t.Fatal("import delivered secret before approval")
			}
			slice2Approve(t, dadm, p)
			dst.waitGuest("web1", "file:/etc/app.env", "DB_PASSWORD="+values[1]+"\n")
			restored := slice2Call(t, dadm, "GET", "/export", nil, 200)
			if strip(string(restored)) != strip(string(plain)) {
				t.Fatal("import changed documents")
			}
			// Roll back the rule revision on the source. Secret values have no history.
			p = slice2Call(t, planner, "POST", "/revisions/1/rollback", map[string]string{"reason": "restore without ignore rule"}, 201)
			slice2Approve(t, adm, p)
			rules := slice2Call(t, adm, "GET", "/rules", nil, 200)
			if !bytes.Contains(rules, []byte(`"items":[]`)) {
				t.Fatal("rollback retained rule")
			}
			for i, e := range []*env{src, dst} {
				c, reader := adm, planner
				if i == 1 {
					c, reader = dadm, &client{e: dst, bearer: dt.Bearer}
				}
				// Read every collection and all details produced by this flow.
				for _, path := range []string{"/session", "/tokens", "/secrets", "/targets", "/targets/web1", "/blocks", "/images", "/rules", "/revisions"} {
					slice2Call(t, c, "GET", path, nil, 200)
				}
				for _, kind := range []string{"plans", "runs", "issues"} {
					b := slice2Call(t, c, "GET", "/"+kind, nil, 200)
					var list struct{ Items []struct{ ID int64 } }
					if err := json.Unmarshal(b, &list); err != nil {
						t.Fatal(err)
					}
					for _, item := range list.Items {
						slice2Call(t, c, "GET", "/"+kind+"/"+itoa(item.ID), nil, 200)
					}
				}
				revs := slice2Call(t, c, "GET", "/revisions", nil, 200)
				var list struct{ Items []struct{ ID int64 } }
				if err := json.Unmarshal(revs, &list); err != nil {
					t.Fatal(err)
				}
				for _, r := range list.Items {
					slice2Call(t, c, "GET", "/revisions/"+itoa(r.ID)+"/diff", nil, 200)
				}
				// Replay from sequence zero through the final persisted event, with no type filter.
				last, err := e.st.LastEventSeq(e.ctx)
				if err != nil || last == 0 {
					t.Fatal("missing events", err)
				}
				s := e.stream(reader, "?since=0", "")
				var stream bytes.Buffer
				reached := false
				for s.sc.Scan() {
					line := s.sc.Text()
					stream.WriteString(line + "\n")
					if line == "id: "+itoa(last) {
						reached = true
					}
					if reached && line == "" {
						break
					}
				}
				s.resp.Body.Close()
				if !reached {
					t.Fatal("incomplete SSE replay", s.sc.Err())
				}
				files, err := filepath.Glob(filepath.Join(e.dir, "plumb.db*"))
				if err != nil {
					t.Fatal(err)
				}
				wal := false
				for _, f := range files {
					b, err := os.ReadFile(f)
					if err != nil {
						t.Fatal(err)
					}
					slice2Scan(t, filepath.Base(f), b, values)
					wal = wal || strings.HasSuffix(f, "-wal")
				}
				if !wal {
					t.Fatal("WAL not scanned")
				}
				evs, err := e.st.EventsAfter(e.ctx, 0, 10000)
				if err != nil || len(evs) == 10000 {
					t.Fatal("incomplete event scan", err)
				}
				events, err := json.Marshal(evs)
				if err != nil {
					t.Fatal(err)
				}
				slice2Scan(t, "events", events, values)
				slice2Scan(t, "SSE", stream.Bytes(), values)
				e.mu.Lock()
				for _, b := range e.seen {
					slice2Scan(t, "API response/export", b, values)
				}
				e.mu.Unlock()
			}
			slice2Scan(t, "slog", logs.Bytes(), values)
			for _, v := range values {
				if !bodiesHold(a, 0, v) {
					t.Fatal("agent positive control missing")
				}
			}
			if !bodiesHold(da, 0, values[1]) {
				t.Fatal("import agent positive control missing")
			}
		})
	}
}

func randBytes(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Capture expected scanner errors without failing the surrounding test.
type slice2ScanProbe struct{ hits int }

func (*slice2ScanProbe) Helper()                 {}
func (p *slice2ScanProbe) Errorf(string, ...any) { p.hits++ }

func TestSlice2ScannerPositiveControl(t *testing.T) {
	value := "qa-scanner-࠿࠾-canary-0123456789"
	plants := map[string]string{"text": value, "hex": hex.EncodeToString([]byte(value)), "HEX": strings.ToUpper(hex.EncodeToString([]byte(value)))}
	for name, enc := range map[string]*base64.Encoding{"std": base64.StdEncoding, "raw-std": base64.RawStdEncoding, "url": base64.URLEncoding, "raw-url": base64.RawURLEncoding} {
		for offset := 0; offset < 3; offset++ {
			plants[fmt.Sprintf("%s/offset-%d", name, offset)] = enc.EncodeToString([]byte(strings.Repeat("z", offset) + value + "suffix"))
		}
	}
	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			probe := &slice2ScanProbe{}
			slice2Scan(probe, "planted buffer", []byte("before "+plant+" after"), []string{value})
			if probe.hits == 0 {
				t.Fatal("scanner missed planted canary")
			}
		})
	}
	probe := &slice2ScanProbe{}
	slice2Scan(probe, "clean buffer", []byte("no canary here"), []string{value})
	if probe.hits != 0 {
		t.Fatal("scanner flagged a clean buffer")
	}
}

func slice2Scan(t interface {
	Helper()
	Errorf(string, ...any)
}, surface string, b []byte, values []string) {
	t.Helper()
	for _, v := range values {
		patterns := []string{v, hex.EncodeToString([]byte(v)), strings.ToUpper(hex.EncodeToString([]byte(v)))}
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			for offset := 0; offset < 3; offset++ {
				encoded := enc.EncodeToString([]byte(strings.Repeat("x", offset) + v))
				// Keep only characters whose six bits come entirely from the canary.
				start, end := (offset*8+5)/6, (offset+len(v))*8/6
				if end > start {
					patterns = append(patterns, encoded[start:end])
				}
			}
		}
		for _, encoded := range patterns {
			if bytes.Contains(b, []byte(encoded)) {
				t.Errorf("canary found in %s (value omitted)", surface)
			}
		}
	}
}

func TestSlice2HumanOnlyMatrix(t *testing.T) {
	for _, kind := range []string{"new-target", "other-resource", "rule-add", "rule-change", "rule-delete"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t, true)
			adm := e.admin()
			tok := e.createToken(adm, map[string]any{"name": "apply", "tier": "apply", "max_risk": "disruptive"})
			apply := &client{e: e, bearer: tok.Bearer}
			value := "s2-" + hex.EncodeToString(randBytes(t))
			slice2Call(t, adm, "PUT", "/secrets/app-db", map[string]string{"value": value}, 200)
			a := e.agent("web1")
			rule := func(attr string) state.Doc {
				return doc(state.KindRule, "test-rule", `{"action":"ignore","target":"*","resource":"file:/etc/other.env","attrs":["`+attr+`"]}`)
			}
			baseline := []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}
			if kind == "rule-change" || kind == "rule-delete" {
				baseline = append(baseline, rule("content"))
			}
			p := slice2Call(t, adm, "POST", "/plans", map[string]any{"reason": "baseline", "put": baseline}, 201)
			slice2Approve(t, adm, p)
			e.waitWatch("web1")
			revision := func() string {
				b := slice2Call(t, adm, "GET", "/revisions", nil, 200)
				var list struct{ Items []struct{ ID int64 } }
				if err := json.Unmarshal(b, &list); err != nil || len(list.Items) == 0 {
					t.Fatal("missing revision")
				}
				return itoa(list.Items[0].ID)
			}
			base := revision()
			proposal := map[string]any{"reason": "candidate"}
			reason := "rule-change"
			switch kind {
			case "new-target":
				e.agent("web2")
				proposal["put"] = []state.Doc{appTarget("web2", "app-db")}
				reason = "secret-binding"
			case "other-resource":
				proposal["put"] = []state.Doc{appBlock("/etc/other.env")}
				reason = "secret-binding"
			case "rule-add":
				proposal["put"] = []state.Doc{rule("content")}
			case "rule-change":
				proposal["put"] = []state.Doc{rule("mode")}
			case "rule-delete":
				proposal["delete"] = []map[string]string{{"kind": "Rule", "name": "test-rule"}}
			}
			// Commit the candidate once to obtain a genuine rollback revision and export.
			p = slice2Call(t, adm, "POST", "/plans", proposal, 201)
			slice2Approve(t, adm, p)
			desired := revision()
			file := slice2Call(t, adm, "GET", "/export", nil, 200)
			p = slice2Call(t, adm, "POST", "/revisions/"+base+"/rollback", map[string]string{"reason": "reset"}, 201)
			slice2Approve(t, adm, p)
			for _, origin := range []string{"proposal", "rollback", "import"} {
				t.Run(origin, func(t *testing.T) {
					skip := len(a.Bodies())
					targetSkip := 0
					if kind == "new-target" {
						targetSkip = len(e.agents["web2"].Bodies())
					}
					switch origin {
					case "proposal":
						p = slice2Call(t, adm, "POST", "/plans", proposal, 201)
					case "rollback":
						p = slice2Call(t, adm, "POST", "/revisions/"+desired+"/rollback", map[string]string{"reason": "candidate"}, 201)
					case "import":
						p = slice2Call(t, adm, "POST", "/import", map[string]any{"reason": "candidate", "content": string(file)}, 201)
					}
					if !bytes.Contains(p, []byte(`"`+reason+`"`)) {
						t.Fatal("missing human-only reason")
					}
					b := slice2Call(t, apply, "POST", "/plans/"+planIDOf(p)+"/approve", nil, 403)
					if problemType(b) != "human-required" {
						t.Fatal("approval did not require human")
					}
					// Existing bindings may be observed; a prospective destination must never get the value.
					if kind == "new-target" && bodiesHold(e.agents["web2"], targetSkip, value) {
						t.Fatal("new target received unapproved secret")
					}
					if kind == "other-resource" {
						for i, raw := range a.Bodies()[skip:] {
							if a.Commands()[skip+i] == "observe" {
								slice2Scan(t, "pre-approval observe body", raw, []string{value})
							}
							var body struct {
								Resources []struct {
									ID    string
									Attrs map[string]any
								}
							}
							if err := json.Unmarshal(raw, &body); err != nil {
								t.Fatal(err)
							}
							for _, r := range body.Resources {
								if r.ID == "file:/etc/other.env" {
									attrs, _ := json.Marshal(r.Attrs)
									slice2Scan(t, "unapproved destination", attrs, []string{value})
								}
							}
						}
					}
					slice2Call(t, adm, "DELETE", "/plans/"+planIDOf(p), nil, 204)
				})
			}
			e.noValue(nil, value)
		})
	}
}

func TestSlice2TokenAndImportBoundaries(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	pt := e.createToken(adm, map[string]any{"name": "planner", "tier": "plan"})
	at := e.createToken(adm, map[string]any{"name": "applier", "tier": "apply", "max_risk": "disruptive"})
	planner, apply := &client{e: e, bearer: pt.Bearer}, &client{e: e, bearer: at.Bearer}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/secrets/app-db", map[string]string{"value": "denied"}},
		{"GET", "/tokens", nil}, {"POST", "/tokens", map[string]string{"name": "denied", "tier": "read"}},
		{"DELETE", "/tokens/" + at.Token.ID, nil},
		{"POST", "/export", map[string]string{"passphrase": exportPass}},
		{"POST", "/import", map[string]string{"reason": "denied", "content": "kind: Export\nformat: 1\n"}},
	} {
		slice2Call(t, planner, r.method, r.path, r.body, 403)
	}
	value := "s2-" + hex.EncodeToString(randBytes(t))
	slice2Call(t, adm, "PUT", "/secrets/app-db", map[string]string{"value": value}, 200)
	e.agent("web1")
	p := slice2Call(t, adm, "POST", "/plans", map[string]any{"reason": "seed", "put": []state.Doc{doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}}, 201)
	slice2Approve(t, adm, p)
	e.waitWatch("web1")
	if err := e.agents["web1"].Change(e.ctx, "file:/etc/app.env", map[string]any{"path": "/etc/app.env", "content": "manual", "mode": "0600"}); err != nil {
		t.Fatal(err)
	}
	list := slice2Call(t, adm, "GET", "/issues?status=open", nil, 200)
	var issues struct{ Items []struct{ ID int64 } }
	if err := json.Unmarshal(list, &issues); err != nil || len(issues.Items) != 1 {
		t.Fatal("missing sensitive issue")
	}
	for _, c := range []*client{planner, apply} {
		b := slice2Call(t, c, "POST", "/issues/"+itoa(issues.Items[0].ID)+"/resolve", map[string]string{"action": "ignore_once"}, 403)
		if problemType(b) != "human-required" {
			t.Fatal("sensitive ignore did not require human")
		}
	}
	file := slice2Call(t, adm, "POST", "/export", map[string]string{"passphrase": exportPass}, 200)
	slice2Call(t, adm, "PUT", "/secrets/app-db", map[string]string{"value": value + "-replacement"}, 200)
	before := qaImportDB(t, e)
	b := slice2Call(t, adm, "POST", "/import", map[string]any{"reason": "conflict", "content": string(file), "passphrase": exportPass}, 409)
	if problemType(b) != "secret-conflict" || before != qaImportDB(t, e) {
		t.Fatal("conflicting import changed database or wrong error")
	}
	e.agent("web2")
	p = slice2Call(t, adm, "POST", "/plans", map[string]any{"reason": "second target", "put": []state.Doc{appTarget("web2", "app-db")}}, 201)
	slice2Approve(t, adm, p)
	p = slice2Call(t, planner, "POST", "/revisions/1/rollback", map[string]string{"reason": "remove second target"}, 201)
	if !bytes.Contains(p, []byte(`"risk":"destructive"`)) {
		t.Fatal("rollback was not destructive")
	}
	b = slice2Call(t, apply, "POST", "/plans/"+planIDOf(p)+"/approve", nil, 403)
	if problemType(b) != "human-required" && problemType(b) != "risk-too-high" {
		t.Fatal("wrong destructive approval refusal")
	}
	e.noValue(nil, value, value+"-replacement")
}
