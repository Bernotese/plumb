package resource

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Bernotese/plumb/internal/wire"
)

type groupResource struct {
	base
	a struct {
		Name   string `json:"name"`
		GID    int    `json:"gid"`
		System bool   `json:"system"`
		State  string `json:"state"`
	}
}

func newGroup(r wire.Resource, env Env) (Resource, error) {
	g := &groupResource{base: base{res: r, env: env}}
	g.a.State = wire.StatePresent
	if err := decode(r, "group", &g.a, func() string { return g.a.Name }); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *groupResource) Observe(context.Context) (State, error) {
	groups, err := readGroups(g.env.Root)
	if err != nil {
		return nil, err
	}
	for _, cur := range groups {
		if cur.name == g.a.Name {
			users, err := readPasswd(g.env.Root)
			if err != nil {
				return nil, err
			}
			primary := false
			for _, u := range users {
				if u.gid == cur.gid {
					primary = true
				}
			}
			return State{wire.AttrState: wire.StatePresent, "gid": cur.gid, "primary": primary}, nil
		}
	}
	return absent(), nil
}

func (g *groupResource) Diff(obs State) []wire.Change {
	if g.a.State == wire.StateAbsent {
		if obs[wire.AttrState] == wire.StatePresent {
			return g.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	if obs[wire.AttrState] != wire.StatePresent {
		return g.finish([]wire.Change{change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible)})
	}
	if g.a.GID != 0 && obs["gid"] != g.a.GID {
		risk := wire.RiskDisruptive
		if obs["primary"] == true {
			risk = wire.RiskDestructive
		}
		return g.finish([]wire.Change{change("gid", obs["gid"], g.a.GID, risk)})
	}
	return nil
}

func (g *groupResource) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	g.env.AccountLock.Lock()
	defer g.env.AccountLock.Unlock()
	obs, err := g.Observe(ctx)
	if err != nil {
		return err
	}
	exists := obs[wire.AttrState] == wire.StatePresent
	if !exists && g.a.State == wire.StateAbsent {
		return nil
	}
	if exists {
		gid := obs["gid"].(int)
		if g.a.State == wire.StateAbsent {
			if gid < 1000 || obs["primary"] == true {
				return fmt.Errorf("group %s is a system or primary group; removal refused", g.a.Name)
			}
			_, err = g.run(ctx, "groupdel", "--", g.a.Name)
			return err
		}
		if g.a.GID == 0 || gid == g.a.GID {
			return nil
		}
		if gid < 1000 || g.a.GID < 1000 {
			return fmt.Errorf("group %s: gid change from or to a system gid refused", g.a.Name)
		}
		_, err = g.run(ctx, "groupmod", "--gid", strconv.Itoa(g.a.GID), "--", g.a.Name)
		return err
	}
	args := []string{}
	if g.a.System {
		args = append(args, "--system")
	}
	if g.a.GID != 0 {
		args = append(args, "--gid", strconv.Itoa(g.a.GID))
	}
	_, err = g.run(ctx, "groupadd", append(args, "--", g.a.Name)...)
	return err
}

func (g *groupResource) Watch() []string { return []string{g.path("/etc/group")} }
