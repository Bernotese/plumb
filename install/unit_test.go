package install

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
)

// Review S1 of #144: systemd must give plumb serve more time to stop than
// the engine's clean-up grace plus the HTTP shutdown (5 s), else a stop
// during a build ends in SIGKILL.
func TestUnitStopTimeoutAboveShutdownGrace(t *testing.T) {
	src, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(src), "readonly UNIT='")
	unit, _, ok2 := strings.Cut(rest, "'\n")
	if !ok || !ok2 {
		t.Fatal("no UNIT text in install.sh")
	}
	m := regexp.MustCompile(`(?m)^TimeoutStopSec=([0-9]+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("the unit has no TimeoutStopSec in seconds:\n%s", unit)
	}
	sec, _ := strconv.Atoi(m[1])
	if need := plan.DefaultShutdownGrace + 5*time.Second; time.Duration(sec)*time.Second <= need+30*time.Second {
		t.Fatalf("TimeoutStopSec=%d s, want well above %v", sec, need)
	}
	if !strings.Contains(unit, "[Service]") || strings.Index(unit, "TimeoutStopSec") < strings.Index(unit, "[Service]") {
		t.Fatal("TimeoutStopSec is not in [Service]")
	}
}
