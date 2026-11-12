//go:build !plumbtest

package agent

import "time"

// Timing of the update guard (D-271). The guest test builds the agent with
// the tag plumbtest and shorter values (update_timing_plumbtest.go).
const (
	guardTick   = 20 * time.Second
	guardWindow = 5 * time.Minute
)
