package agent

import (
	"strings"
	"testing"
	"time"
)

// T-195 (mutant A1, D-390): a refused one-shot token is dropped at once.
// The agent asks the controller with it once, not in every round, and
// goes on with its old credential.
func TestOneShotQARefusedTriedOnce(t *testing.T) {
	e := newOneShotEnv(t)
	stop := e.run()
	first := e.agentAfter(time.Now().Add(-time.Minute))
	stop()

	e.writeOneShot("an-unknown-token-0123456789abcdef", 0o600)
	start := time.Now()
	stop = e.run()
	a := e.agentAfter(start)
	time.Sleep(1500 * time.Millisecond) // many rounds of the agent loop
	stop()
	if a.ID != first.ID {
		t.Fatalf("agent %s, want the old %s", a.ID, first.ID)
	}
	if n := strings.Count(e.log.String(), "one-shot enrollment token refused"); n != 1 {
		t.Fatalf("the refused token was tried %d times, want 1:\n%s", n, e.log.String())
	}
	if strings.Contains(e.log.String(), "an-unknown-token-0123456789abcdef") {
		t.Fatal("the agent logged the token")
	}
}
