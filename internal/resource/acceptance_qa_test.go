package resource

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestSensitiveResourcesConvergeWithoutReportingValues(t *testing.T) {
	for _, rendered := range []wire.Resource{
		res("file", "/etc/private", map[string]any{"content": secret}, "content"),
		res("directory", "/srv/private", map[string]any{"mode": "0750"}, "mode"),
		res("package", "nginx", map[string]any{"strictness": "exact", "version": "1.26.0-1"}, "version"),
		res("service", "nginx", nil, "state", "enabled"),
		res("user", "deploy", map[string]any{"shell": "/bin/sh", "ssh_keys": []any{keyA}}, "shell", "home", "ssh_keys"),
	} {
		t.Run(rendered.Type, func(t *testing.T) {
			g := newFakeGuest(t)
			rs := build(t, rendered, g.env())
			observed, err := rs.Observe(context.Background())
			if err != nil || observed[wire.AttrState] != wire.StateAbsent {
				t.Fatalf("missing resource must be absent: %+v, %v", observed, err)
			}
			if rendered.Type == "service" {
				g.units["nginx.service"] = &unit{}
			}
			changes := converge(t, rs)
			for _, attr := range rendered.Sensitive {
				found := false
				for _, change := range changes {
					if change.Attr != attr {
						continue
					}
					found = true
					if !change.Sensitive || change.From != nil || change.To != nil || !change.Risk.Valid() {
						t.Fatalf("sensitive attribute leaked values or lacks risk: %+v", change)
					}
				}
				if !found {
					t.Errorf("missing sensitive change for %s", attr)
				}
			}
			assertClean(t, "changes", changes, secret, keyA)
		})
	}
}

func TestSensitiveObservedContentStaysOutOfOutput(t *testing.T) {
	g := newFakeGuest(t)
	const old = "db_password=previous-private-credential"
	mustWrite(t, filepath.Join(g.root, "etc/app.conf"), old)
	r := res("file", "/etc/app.conf", map[string]any{"content": secret}, "content")
	rs := build(t, r, g.env())
	changes := plan(t, rs)
	assertClean(t, "drift changes", changes, old, secret)
	report := wire.Report{Type: wire.ReportResult, Items: []wire.Item{{
		Resource: r.ID,
		Status:   wire.StatusFailed,
		Changes:  changes,
		Output:   SafeOutput(r, "failed while replacing "+old+" with "+secret),
	}}}
	assertClean(t, "drift report including Output", report, old, secret)
}
