package issue

import (
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// AcceptMode returns what Accept of an Issue writes (D-362, Issue.accept_mode):
// state.AcceptNone when accept is not allowed, state.AcceptGuest for the
// Proxmox settings (guest:*, D-068), else the result of
// state.Set.AcceptParams: Block parameters, or raw (an override) with the
// reason. set is the head revision; nil gives raw.
func AcceptMode(set *state.Set, target, resource string, changes []wire.Change, allowed bool) state.AcceptPlan {
	if !allowed {
		return state.AcceptPlan{Mode: state.AcceptNone}
	}
	switch typ, _, _ := wire.SplitID(resource); typ {
	case state.GuestType:
		return state.AcceptPlan{Mode: state.AcceptGuest}
	case wire.AgentItemType:
		return state.AcceptPlan{Mode: state.AcceptNone}
	}
	if set == nil {
		return state.AcceptPlan{Mode: state.AcceptRaw, Reason: "the desired state is not loaded"}
	}
	return set.AcceptParams(target, resource, changes)
}

// errParamUse is returned when spec.blocks of the stored Target does not
// match the use that the mapping names (the head changed in between).
var errParamUse = errors.New("issue: the Block use of the Target changed; open the Issue again")

// setParams writes the parameter values into spec.blocks[i].params of a
// decoded Target spec. Each use must still be the one of the mapping.
func setParams(spec map[string]any, params []state.ParamValue) error {
	blocks, _ := spec["blocks"].([]any)
	for _, p := range params {
		if p.Index < 0 || p.Index >= len(blocks) {
			return errParamUse
		}
		use, ok := blocks[p.Index].(map[string]any)
		if !ok {
			return errParamUse
		}
		id, _ := use["id"].(string)
		if id == "" {
			id, _ = use["use"].(string)
		}
		if id != p.Use {
			return fmt.Errorf("%w (use %q)", errParamUse, p.Use)
		}
		values, _ := use["params"].(map[string]any)
		if values == nil {
			values = map[string]any{}
		}
		values[p.Param] = p.Value
		use["params"] = values
	}
	return nil
}
