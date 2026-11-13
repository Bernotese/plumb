//go:build guest

package agent

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// The guest harness has an overlay root. Refuse it before any disk write.
func TestGuestGrowRefusesContainerRoot(t *testing.T) {
	out, err := exec.Command("findmnt", "-n", "-o", "FSTYPE", "/").Output()
	if err != nil || strings.TrimSpace(string(out)) != "overlay" {
		t.Skip("requires the container guest harness with an overlay root")
	}
	a := &Agent{env: resource.NewEnv("/", resource.ExecRunner{})}
	item := a.grow(context.Background(), wire.GrowBody{MinBytes: 1})
	if item.Status != wire.StatusFailed || item.Output != "grow: unsupported in a container" {
		t.Fatalf("%+v", item)
	}
}
