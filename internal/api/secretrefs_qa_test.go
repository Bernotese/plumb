package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

func qaSecretPlan(t *testing.T, e *env, c *client, name string) string {
	t.Helper()
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "QA binding", "put": []state.Doc{
		doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget(name, "app-db")}})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	return "/api/v1/plans/" + e.planID(body)
}

func TestQASecretValuesAndErrors(t *testing.T) {
	for _, mode := range []string{"markers", "newlines", "64KiB", "1MiB", "refusal", "timeout", "short-error1", "short-error2", "short-error3", "short-output1", "short-output2", "short-output3"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			envLog = slog.New(slog.NewTextHandler(&logs, nil))
			defer func() { envLog = quiet }()
			// Only the timeout case waits for the apply timeout; 1 s keeps it
			// short (T-211) and the other modes keep the default.
			e := newEnv(t, true, func(c *plan.Config) {
				if mode == "timeout" {
					c.ApplyTimeout = time.Second
				}
			})
			adm := e.admin()
			value := "qa-error-secret-82c3"
			short := strings.HasPrefix(mode, "short-")
			if short {
				value = "~!@"[:int(mode[len(mode)-1]-'0')]
			}
			switch mode {
			case "markers":
				value = "qa-" + state.Marker("not-a-reference")
			case "newlines":
				value = "qa-first\nsecond\r\nthird"
			case "1MiB":
				value = strings.Repeat("q", 1<<20)
			case "64KiB":
				value = strings.Repeat("q", 1<<16)
			}
			code, body := adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": value})
			if mode == "1MiB" {
				if code != 413 || problemType(body) != "too-large" {
					t.Fatalf("oversize PUT: %d", code)
				}
				e.noValue(&logs, value)
				return
			}
			if code != 200 {
				t.Fatalf("PUT: %d", code)
			}
			a := e.agent("web1")
			if mode == "refusal" || mode == "timeout" || short {
				a.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rec := httptest.NewRecorder()
					e.link.Handler().ServeHTTP(rec, r)
					var cmd wire.Command
					json.Unmarshal(rec.Body.Bytes(), &cmd)
					if r.URL.Path == wire.PathPoll && cmd.Type == wire.CommandApply {
						if mode == "refusal" || short {
							rep := wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Items: []wire.Item{{Resource: "file:/etc/app.env", Status: wire.StatusRefused, Output: "refused password " + value}}}
							if !strings.HasPrefix(mode, "short-output") {
								rep.Error = "refused password " + value
								if short {
									rep.Items = nil
								}
							}
							if code := a.Report(r.Context(), rep); code != http.StatusNoContent {
								t.Errorf("report: %d", code)
							}
						}
						w.WriteHeader(http.StatusNoContent)
						return
					}
					w.WriteHeader(rec.Code)
					w.Write(rec.Body.Bytes())
				}))
			}
			p := qaSecretPlan(t, e, adm, "web1")
			if code, b := adm.do("POST", p+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, b)
			}
			e.eng.Wait()
			run, err := e.st.LastRunOfTarget(e.ctx, "web1")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "refusal" || mode == "timeout" || short {
				if run.Status != "failed" || a.Guest("file:/etc/app.env") != nil {
					t.Fatal("failed command changed guest or run did not fail")
				}
			} else if got := a.Guest("file:/etc/app.env")["content"]; got != "DB_PASSWORD="+value+"\n" {
				t.Fatal("secret did not arrive unchanged")
			}
			adm.do("GET", "/api/v1/runs/"+itoa(run.ID), nil)
			if short {
				steps, _ := e.st.Steps(e.ctx, run.ID)
				masked := false
				for _, step := range steps {
					if strings.Contains(step.Output, value) {
						t.Fatal("short secret stored")
					}
					masked = masked || strings.Contains(step.Output, "[redacted]")
				}
				if !masked {
					t.Fatal("no masked report persisted")
				}
				return // A one-byte canary would also match unrelated encrypted database bytes.
			}
			escaped, _ := json.Marshal(value)
			e.noValue(&logs, value, string(escaped[1:len(escaped)-1]))
		})
	}
}

func TestQASecretDeletedAndShared(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "qa-shared-82c3"})
	planTok, applyTok := e.token(TierPlan, ""), e.token(TierApply, wire.RiskDisruptive)
	p := qaSecretPlan(t, e, planTok, "web1")
	if code, b := adm.do("DELETE", "/api/v1/secrets/app-db", nil); code != 409 || problemType(b) != "secret-in-use" {
		t.Fatalf("pending binding deletion: %d %s", code, b)
	}
	e.sec.SetUsage(nil) // Simulate deletion racing proposal/approval, without SecretChanged.
	if err := e.sec.Delete(e.ctx, "app-db"); err != nil {
		t.Fatal(err)
	}
	if code, b := adm.do("POST", p+"/approve", nil); code != 409 || problemType(b) != "plan-stale" {
		t.Fatalf("deleted at approval: %d %s", code, b)
	}
	if code, b := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "deleted", "put": []state.Doc{
		doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), appTarget("web1", "app-db")}}); code != 422 || problemType(b) != "validation" {
		t.Fatalf("deleted at proposal: %d %s", code, b)
	}
	adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "qa-shared-82c3"})
	e.agent("web1")
	p = qaSecretPlan(t, e, adm, "web1")
	if code, b := adm.do("POST", p+"/approve", nil); code != 202 {
		t.Fatalf("approve first target: %d %s", code, b)
	}
	e.eng.Wait()
	a2 := e.agent("web2")
	p = qaSecretPlan(t, e, planTok, "web2")
	for _, tok := range []*client{planTok, applyTok} {
		if code, _ := tok.do("POST", p+"/approve", nil); code != 403 {
			t.Fatalf("token approved new shared binding: %d", code)
		}
	}
	if a2.Guest("file:/etc/app.env") != nil || len(a2.Commands()) != 0 {
		t.Fatal("unapproved target received a secret command")
	}
}

func TestQASecretMarkerErrorResponse(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	d := appTarget("web1", "app-db")
	var spec map[string]any
	json.Unmarshal(d.Spec, &spec)
	params := spec["blocks"].([]any)[0].(map[string]any)["params"].(map[string]any)
	params[state.Marker("forged")] = "plain"
	d.Spec, _ = json.Marshal(spec)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "marker key", "put": []state.Doc{
		doc(state.KindImage, "ubuntu", imageSpec), appBlock("/etc/app.env"), d}})
	if code != 422 || problemType(body) != "validation" {
		t.Fatalf("marker key: %d %s", code, body)
	}
	if bytes.ContainsRune(body, state.MarkStart) || bytes.ContainsRune(body, state.MarkEnd) {
		t.Fatal("validation response contains marker characters")
	}
}
