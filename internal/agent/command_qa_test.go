package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

type commandQARunner struct {
	root  string
	fail  bool
	calls int
}

func (r *commandQARunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	r.calls++
	if r.fail {
		return []byte("private-stdout"), &resource.ExitError{Name: name, Code: 7, Stderr: "private-stderr"}
	}
	return []byte("private-stdout"), os.WriteFile(filepath.Join(r.root, "srv/done"), nil, 0o600)
}

// Check the reports at the controller, after the real agent has encoded them.
func TestCommandReportsAndGuard(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			runner := &commandQARunner{fail: fail}
			startAgentWith(t, ctl, ca, "tok-1", func(o *Options) {
				runner.root = o.Root
				o.Runner = runner
			})
			r := wire.Resource{ID: "command:init", Type: "command", Block: "app", Attrs: map[string]any{
				"name": "init", "argv": []any{"/opt/init"}, "creates": "/srv/done",
			}}
			want := wire.StatusApplied
			if fail {
				want = wire.StatusFailed
			}
			ctl.send(t, "apply", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, Planned: unverified(r), MaxRisk: wire.RiskDestructive})
			result := ctl.result(t, "apply")
			if len(result.Items) != 1 || result.Items[0].Status != want {
				t.Fatalf("result = %+v", result)
			}
			if !fail {
				ctl.send(t, "again", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, Planned: unverified(r), MaxRisk: wire.RiskDestructive})
				if result := ctl.result(t, "again"); len(result.Items) != 1 || result.Items[0].Status != wire.StatusUnchanged {
					t.Fatalf("guard result = %+v", result)
				}
			}
			if runner.calls != 1 {
				t.Fatalf("command calls = %d", runner.calls)
			}
			for _, typ := range []wire.ReportType{wire.ReportResult, wire.ReportProgress, wire.ReportDrift} {
				b, err := json.Marshal(ctl.reportsOf(typ))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), "private-stdout") || strings.Contains(string(b), "private-stderr") {
					t.Fatalf("command output left the guest in %s", typ)
				}
			}
		})
	}
}
