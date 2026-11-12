package state

import (
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
