package state

import (
	"encoding/json"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// PlanRisk returns the risk that a Plan shows for a resource of this type
// whose changes have risk r. A type whose every change is destructive (a
// command, D-260) gets destructive, also for an unverified create and
// whatever the agent reported, so only a human session approves (D-031).
func PlanRisk(typ string, r wire.Risk) wire.Risk {
	if t, ok := resource.Lookup(typ); ok && t.Destructive {
		return wire.RiskDestructive
	}
	return r
}

// commandDefaultTimeout is the schema default of command.timeout, seconds.
const commandDefaultTimeout = 600

// CommandTime is the longest time that the command resources of a list
// may run, one after the other: the sum of their timeouts. The Run adds it
// to the apply timeout of the Target (T-094 review M2).
func CommandTime(rs []wire.Resource) time.Duration {
	var total time.Duration
	for _, r := range rs {
		if r.Type != "command" {
			continue
		}
		sec := int64(commandDefaultTimeout)
		switch v := r.Attrs["timeout"].(type) {
		case json.Number:
			if n, err := v.Int64(); err == nil {
				sec = n
			}
		case float64:
			sec = int64(v)
		case int:
			sec = int64(v)
		case int64:
			sec = v
		}
		total += time.Duration(sec) * time.Second
	}
	return total
}
