package resource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// command runs a program once (schema/command.json, D-260, D-270). It is
// the escape hatch of a Block: no shell, no drift, and every run is
// destructive, so only a human session approves it (D-031).
//
// Observe reports state "absent" while the creates path is missing and
// "present" when it exists (a node of any kind; a symlink is not followed).
// So the only change is a create. A changed argv on a guest where creates
// exists changes nothing.
//
// Output: a command can print anything that root reads in the guest, also
// secrets that Plumb never knew. So no stdout or stderr of the program
// leaves the guest; a failure says only the exit status or the timeout.
type command struct {
	base
	a commandAttrs
}

type commandAttrs struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Creates string   `json:"creates"`
	Timeout int      `json:"timeout"`
}

// commandTimeout is the schema default of timeout, in seconds.
const commandTimeout = 600

func newCommand(r wire.Resource, env Env) (Resource, error) {
	c := &command{base: base{res: r, env: env}, a: commandAttrs{Timeout: commandTimeout}}
	if err := decode(r, "command", &c.a, func() string { return c.a.Name }); err != nil {
		return nil, err
	}
	// The controller checked the schema; the agent checks what it relies on.
	switch {
	case len(c.a.Argv) == 0 || !absPath(c.a.Argv[0]):
		return nil, fmt.Errorf("resource %s: argv[0] must be an absolute path", r.ID)
	case !absPath(c.a.Creates):
		return nil, fmt.Errorf("resource %s: creates must be an absolute path without a .. segment", r.ID)
	case c.a.Timeout < 1 || c.a.Timeout > 3600:
		return nil, fmt.Errorf("resource %s: timeout must be 1 … 3600 seconds", r.ID)
	}
	return c, nil
}

// absPath reports whether p is absolute, has no ".." segment and no NUL:
// the "absolute path" rule of the schemas.
func absPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}

func (c *command) Observe(_ context.Context) (State, error) {
	_, err := os.Lstat(c.path(c.a.Creates))
	switch {
	case err == nil:
		return State{wire.AttrState: wire.StatePresent}, nil
	case errors.Is(err, os.ErrNotExist):
		return absent(), nil
	default:
		return nil, fmt.Errorf("creates %s: %w", c.a.Creates, err)
	}
}

// Diff has one change, the create, always destructive (D-260).
func (c *command) Diff(observed State) []wire.Change {
	if observed[wire.AttrState] == wire.StatePresent {
		return nil
	}
	return c.finish([]wire.Change{change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskDestructive)})
}

func (c *command) Apply(ctx context.Context, changes []wire.Change) error {
	if !has(changes, wire.AttrState) {
		return nil
	}
	// The guard again: something may have made the path since the observe.
	if st, err := c.Observe(ctx); err != nil {
		return err
	} else if st[wire.AttrState] == wire.StatePresent {
		return nil
	}
	limit := time.Duration(c.a.Timeout) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	_, err := c.run(runCtx, c.a.Argv[0], c.a.Argv[1:]...)
	if err != nil {
		var ee *ExitError
		switch {
		case runCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil:
			return fmt.Errorf("%s: killed after the timeout of %d s (output not shown)", c.a.Argv[0], c.a.Timeout)
		case errors.As(err, &ee):
			return fmt.Errorf("%s: exit status %d (output not shown)", c.a.Argv[0], ee.Code)
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			// A start error (no such file, no exec right) holds no output.
			return err
		}
	}
	st, err := c.Observe(ctx)
	if err != nil {
		return err
	}
	if st[wire.AttrState] != wire.StatePresent {
		return fmt.Errorf("%s ended with exit status 0 but did not create %s; it runs again at the next apply", c.a.Argv[0], c.a.Creates)
	}
	return nil
}

// Watch is empty: a command has no drift (D-260).
func (c *command) Watch() []string { return nil }
