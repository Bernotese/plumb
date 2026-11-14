package link

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// parked enrolls web1, queues an observe before the park, and parks it.
func parkedFixture(t *testing.T) (*fixture, string) {
	f := newFixture(t)
	cred := f.agent("web1")
	if code, _ := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "0.2", Hostname: "web1"}}); code != 204 {
		t.Fatalf("hello before park: %d", code)
	}
	if _, err := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{}); err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(cred, ".")
	if err := f.st.Park(context.Background(), store.Parked{Target: "web1", AgentID: id, GuestRef: "150", Node: "pve", Since: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return f, cred
}

func (f *fixture) deliveries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

// D-396 condition (1) of pl-lead: a parked agent can take unbind and send
// its result, and nothing else. One case per link call.
func TestParkedAgentOnlyTakesUnbind(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		f, _ := parkedFixture(t)
		for _, typ := range []wire.CommandType{wire.CommandObserve, wire.CommandApply, wire.CommandWatch, wire.CommandGrow, wire.CommandUpdate} {
			var body any = struct{}{}
			switch typ {
			case wire.CommandApply:
				body = wire.ApplyBody{MaxRisk: wire.RiskReversible}
			case wire.CommandGrow:
				body = wire.GrowBody{PlanID: 1, MinBytes: 1 << 30}
			case wire.CommandUpdate:
				body = wire.UpdateBody{}
			case wire.CommandObserve:
				body = wire.ObserveBody{}
			case wire.CommandWatch:
				body = wire.WatchBody{}
			}
			if _, err := f.link.Send("web1", typ, body); !errors.Is(err, ErrParked) {
				t.Errorf("send %s to a parked agent: %v", typ, err)
			}
		}
		if _, err := f.link.Send("web1", wire.CommandUnbind, struct{}{}); err != nil {
			t.Fatalf("send unbind: %v", err)
		}
	})
	t.Run("poll gets only unbind", func(t *testing.T) {
		f, cred := parkedFixture(t)
		// The observe queued before the park is never handed out.
		if code, body := f.do("GET", wire.PathPoll, cred, nil); code != 204 {
			t.Fatalf("poll: %d %s", code, body)
		}
		id, _ := f.link.Send("web1", wire.CommandUnbind, struct{}{})
		code, body := f.do("GET", wire.PathPoll, cred, nil)
		var cmd wire.Command
		json.Unmarshal(body, &cmd)
		if code != 200 || cmd.Type != wire.CommandUnbind || cmd.ID != id {
			t.Fatalf("poll: %d %s", code, body)
		}
	})
	t.Run("reports are dropped", func(t *testing.T) {
		f, cred := parkedFixture(t)
		before := f.deliveries()
		for name, rep := range map[string]wire.Report{
			"hello":  {Type: wire.ReportHello, Hello: &wire.Hello{Version: "0.3", Hostname: "evil"}},
			"drift":  {Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/x", Status: wire.StatusDiffers}}},
			"result": {Type: wire.ReportResult, CommandID: "nope", Items: []wire.Item{{Resource: "file:/etc/x", Status: wire.StatusApplied}}},
		} {
			if code, body := f.do("POST", wire.PathReport, cred, rep); code != 204 {
				t.Errorf("%s: %d %s", name, code, body)
			}
		}
		if n := f.deliveries(); n != before {
			t.Fatalf("a parked agent's report was delivered (%d → %d)", before, n)
		}
	})
	t.Run("result of another command is dropped", func(t *testing.T) {
		f := newFixture(t)
		cred := f.agent("web1")
		obs, _ := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{})
		if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 200 {
			t.Fatal("poll observe")
		}
		id, _, _ := strings.Cut(cred, ".")
		f.st.Park(context.Background(), store.Parked{Target: "web1", AgentID: id, GuestRef: "150", Since: time.Now()})
		before := f.deliveries()
		rep := wire.Report{Type: wire.ReportResult, CommandID: obs, Items: []wire.Item{{Resource: "file:/etc/x", Status: wire.StatusUnchanged}}}
		if code, body := f.do("POST", wire.PathReport, cred, rep); code != 204 {
			t.Fatalf("%d %s", code, body)
		}
		if f.deliveries() != before {
			t.Fatal("the observe result of a parked agent was delivered")
		}
	})
	t.Run("unbind result is delivered", func(t *testing.T) {
		f, cred := parkedFixture(t)
		id, _ := f.link.Send("web1", wire.CommandUnbind, struct{}{})
		f.do("GET", wire.PathPoll, cred, nil)
		before := f.deliveries()
		if code, body := f.do("POST", wire.PathReport, cred, wire.Report{Type: wire.ReportResult, CommandID: id}); code != 204 {
			t.Fatalf("%d %s", code, body)
		}
		if f.deliveries() != before+1 {
			t.Fatal("the unbind result was not delivered")
		}
	})
	t.Run("no new enrollment or repair token", func(t *testing.T) {
		f, _ := parkedFixture(t)
		if _, _, err := f.link.NewEnrollToken(context.Background(), "web1"); !errors.Is(err, ErrParked) {
			t.Errorf("enroll token: %v", err)
		}
		if _, err := f.link.NewRepairToken(context.Background(), "web1"); !errors.Is(err, ErrParked) {
			t.Errorf("repair token: %v", err)
		}
		// Another target is not affected.
		if _, _, err := f.link.NewEnrollToken(context.Background(), "web2"); err != nil {
			t.Errorf("web2: %v", err)
		}
	})
	t.Run("a token made before the park cannot enroll", func(t *testing.T) {
		f := newFixture(t)
		cred := f.agent("web1")
		tok := f.token("web1") // e.g. an install command shown before the delete
		id, _, _ := strings.Cut(cred, ".")
		if err := f.st.Park(context.Background(), store.Parked{Target: "web1", AgentID: id, GuestRef: "150", Since: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if code, _, body := f.enroll(tok); code != 401 {
			t.Fatalf("enroll with an old token: %d %s", code, body)
		}
	})
	t.Run("unpark ends it", func(t *testing.T) {
		f, cred := parkedFixture(t)
		f.st.Unpark(context.Background(), "web1")
		if _, err := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{}); err != nil {
			t.Fatal(err)
		}
		if code, _ := f.do("GET", wire.PathPoll, cred, nil); code != 200 {
			t.Fatal("an unparked agent gets no command")
		}
	})
}
