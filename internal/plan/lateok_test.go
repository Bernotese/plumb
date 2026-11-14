package plan

import (
	"testing"
	"time"
)

// The memory of a late unbind result belongs only to a delete Run of the
// same agent that parks within two unbind waits (T-210; #313 n1). An older
// one, or one of another agent, does not end a park; either way it is
// forgotten after one read.
func TestTakeLateOKAgeBound(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	e := &Engine{cfg: Config{UnbindTimeout: time.Minute, Now: func() time.Time { return now }}}
	at := func(age time.Duration) {
		e.lateOK = map[string]lateUnbindOK{"wiki": {agentID: "a1", at: now.Add(-age)}}
	}
	for _, c := range []struct {
		age   time.Duration
		agent string
		want  bool
	}{
		{0, "a1", true},
		{2 * time.Minute, "a1", true}, // the bound itself
		{2*time.Minute + time.Nanosecond, "a1", false},
		{time.Hour, "a1", false},
		{0, "a2", false},
	} {
		at(c.age)
		if got := e.takeLateOK("wiki", c.agent); got != c.want {
			t.Errorf("age %v agent %s: %v, want %v", c.age, c.agent, got, c.want)
		}
		if _, still := e.lateOK["wiki"]; still {
			t.Errorf("age %v agent %s: the memory stays after the read", c.age, c.agent)
		}
	}
}
