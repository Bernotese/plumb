package state

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

const cmdBlock = `{"resources":[{"command":{"name":"init","argv":["/usr/local/bin/init"],"creates":"/var/lib/init.done"}}]}`

// T-094: an Image whose build Blocks hold a command is refused at save
// (D-260); a Target may use the same Block.
func TestImageRefusesCommand(t *testing.T) {
	useApp := func(s map[string]any) {
		s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "app"}}
	}
	_, err := Validate([]Doc{builtImage(t, useApp), doc(KindBlock, "app", cmdBlock)})
	errAt(t, err, KindImage, "/spec/build/blocks", "an Image build cannot hold a command resource")

	// A Target may hold it.
	tgt := Doc{Kind: KindTarget, Name: "web1", Spec: json.RawMessage(`{"type":"vm","image":"web","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"app"}]}`)}
	if _, err := Validate([]Doc{builtImage(t, nil), motdBlock("hi"), doc(KindBlock, "app", cmdBlock), tgt}); err != nil {
		t.Fatalf("a Target with a command: %v", err)
	}
}

// Reviewer note of T-094: a Block that gets a command AFTER an Image uses
// it. The whole set is checked on every proposal, so the change fails.
func TestBlockGetsCommandAfterImageUsesIt(t *testing.T) {
	useApp := func(s map[string]any) {
		s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "app"}}
	}
	plain := doc(KindBlock, "app", `{"resources":[{"file":{"path":"/etc/app","content":"x"}}]}`)
	if _, err := Validate([]Doc{builtImage(t, useApp), plain}); err != nil {
		t.Fatalf("before: %v", err)
	}
	_, err := Validate([]Doc{builtImage(t, useApp), doc(KindBlock, "app", cmdBlock)})
	errAt(t, err, KindImage, "/spec/build/blocks", "cannot hold a command")
	// The build start (plan build, RenderImage) renders with the same
	// renderUnit, so it refuses the same way.
}

func TestPlanRisk(t *testing.T) {
	if got := PlanRisk("command", wire.RiskReversible); got != wire.RiskDestructive {
		t.Errorf("command: %s", got)
	}
	if got := PlanRisk("file", wire.RiskReversible); got != wire.RiskReversible {
		t.Errorf("file: %s", got)
	}
}

// RenderImage is also the build-start boundary. Bypass save validation to
// prove rendering checks the current Blocks independently.
func TestCommandRefusedWhenRenderingImage(t *testing.T) {
	image := builtImage(t, func(s map[string]any) {
		s["build"].(map[string]any)["blocks"] = []any{map[string]any{"use": "app"}}
	})
	set, err := Validate([]Doc{image, doc(KindBlock, "app", `{"resources":[{"file":{"path":"/etc/app","content":"x"}}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.RenderImage("web"); err != nil {
		t.Fatal(err)
	}
	commands, err := Validate([]Doc{doc(KindBlock, "app", cmdBlock)})
	if err != nil {
		t.Fatal(err)
	}
	set.blocks["app"] = commands.blocks["app"]
	rendered, err := set.RenderImage("web")
	errAt(t, err, KindImage, "/spec/build/blocks", "cannot hold a command")
	if rendered != nil {
		t.Fatal("unsafe build recipe returned")
	}
}
