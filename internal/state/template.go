package state

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
)

// Limits bound the work of rendering one Target. Block templates are user
// input, so a template must not run for ever or fill the memory.
type Limits struct {
	// Timeout is the time limit for all templates of one Target.
	Timeout time.Duration
	// MaxOutput is the limit, in bytes, for the summed output of all
	// templates of one Target. Every string that a function returns
	// counts against it too.
	MaxOutput int
}

// DefaultLimits are the limits of Validate and of the controller.
var DefaultLimits = Limits{Timeout: 2 * time.Second, MaxOutput: 1 << 20}

// ErrRenderLimit is wrapped by every error from a render limit.
var ErrRenderLimit = errors.New("render limit")

// budget is the shared allowance of one Target render.
type budget struct {
	deadline time.Time
	left     int // output bytes
	max      int
}

func newBudget(lim Limits) *budget {
	return &budget{deadline: time.Now().Add(lim.Timeout), left: lim.MaxOutput, max: lim.MaxOutput}
}

func (b *budget) checkTime() error {
	if time.Now().After(b.deadline) {
		return fmt.Errorf("%w: rendering took longer than the time limit", ErrRenderLimit)
	}
	return nil
}

// fits checks the size of a value that a function is about to build.
// It does not use up the budget; only written output does.
func (b *budget) fits(n int) error {
	if n > b.max {
		return fmt.Errorf("%w: a value is larger than %d bytes", ErrRenderLimit, b.max)
	}
	return b.checkTime()
}

// Write consumes output budget. It is the io.Writer of every template.
type budgetWriter struct {
	b   *budget
	buf strings.Builder
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if err := w.b.checkTime(); err != nil {
		return 0, err
	}
	if len(p) > w.b.left {
		return 0, fmt.Errorf("%w: the output is larger than %d bytes", ErrRenderLimit, w.b.max)
	}
	w.b.left -= len(p)
	return w.buf.Write(p)
}

// allowedBuiltins are the text/template built-in functions that a Block
// may use. They cannot build large values. call, print, printf, println,
// html, js and urlquery are not allowed.
var allowedBuiltins = map[string]bool{
	"and": true, "or": true, "not": true, "len": true, "index": true, "slice": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
}

// tickFunc is the name of the internal function that the sandbox inserts
// into every range body. Blocks cannot call it (it is not allowed).
const tickFunc = "_plumb_tick"

// funcs returns the allowlisted functions. All of them are pure: no file,
// environment, network or clock access. Each one checks the size of what
// it builds against the budget. b is nil when templates are only parsed.
func funcs(b *budget) template.FuncMap {
	if b == nil {
		b = &budget{deadline: time.Now().Add(time.Hour), max: 0}
	}
	str := func(f func(string) string) func(string) (string, error) {
		return func(s string) (string, error) {
			out := f(s)
			return out, b.fits(len(out))
		}
	}
	return template.FuncMap{
		"lower":      str(strings.ToLower),
		"upper":      str(strings.ToUpper),
		"trim":       str(strings.TrimSpace),
		"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
		"trimSuffix": func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
		"contains":   func(sub, s string) bool { return strings.Contains(s, sub) },
		"hasPrefix":  func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
		"hasSuffix":  func(suffix, s string) bool { return strings.HasSuffix(s, suffix) },
		// replace OLD NEW S replaces every OLD in S by NEW.
		"replace": func(old, new, s string) (string, error) {
			n := strings.Count(s, old)
			if err := b.fits(len(s) + n*(len(new)-len(old))); err != nil {
				return "", err
			}
			return strings.ReplaceAll(s, old, new), nil
		},
		// join SEP LIST joins the elements of LIST (strings or numbers) with SEP.
		"join": func(sep string, list []any) (string, error) {
			parts := make([]string, len(list))
			size := 0
			for i, e := range list {
				switch e := e.(type) {
				case string:
					parts[i] = e
				case fmt.Stringer: // json.Number
					parts[i] = e.String()
				case bool:
					parts[i] = fmt.Sprint(e)
				default:
					return "", fmt.Errorf("join: element %d is not a string or a number", i)
				}
				size += len(parts[i]) + len(sep)
			}
			if err := b.fits(size); err != nil {
				return "", err
			}
			return strings.Join(parts, sep), nil
		},
		// default DEF V returns V, or DEF when V is empty.
		"default": func(def, v any) any {
			switch v := v.(type) {
			case nil:
				return def
			case string:
				if v == "" {
					return def
				}
			}
			return v
		},
		tickFunc: func() (string, error) { return "", b.checkTime() },
	}
}

// allowedFuncs is the full allowlist: the custom functions and the
// allowed built-ins.
var allowedFuncs = func() map[string]bool {
	m := map[string]bool{}
	for name := range funcs(nil) {
		if name != tickFunc {
			m[name] = true
		}
	}
	for name := range allowedBuiltins {
		m[name] = true
	}
	return m
}()

// parseTemplate parses one value template and checks it against the
// sandbox rules. With a budget, every range body gets a time check, so a
// huge loop stops at the time limit even when it writes nothing.
func parseTemplate(text string, b *budget) (*template.Template, error) {
	t, err := template.New("value").Option("missingkey=error").Funcs(funcs(b)).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("template: %s", strings.TrimPrefix(err.Error(), "template: "))
	}
	if len(t.Templates()) > 1 {
		return nil, errors.New("template: define and block are not allowed")
	}
	if t.Tree == nil || t.Tree.Root == nil {
		return t, nil
	}
	var tick *parse.ActionNode
	if b != nil {
		tt, err := template.New("tick").Funcs(funcs(b)).Parse("{{" + tickFunc + "}}")
		if err != nil {
			return nil, err
		}
		tick = tt.Tree.Root.Nodes[0].(*parse.ActionNode)
	}
	if err := checkNode(t.Tree.Root, tick); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	return t, nil
}

// checkNode walks a parse tree, rejects what the sandbox does not allow
// and inserts the tick into range bodies.
func checkNode(n parse.Node, tick *parse.ActionNode) error {
	switch n := n.(type) {
	case nil:
		return nil
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := checkNode(c, tick); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return checkNode(n.Pipe, tick)
	case *parse.PipeNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Cmds {
			if err := checkNode(c, tick); err != nil {
				return err
			}
		}
	case *parse.CommandNode:
		for _, a := range n.Args {
			if err := checkNode(a, tick); err != nil {
				return err
			}
		}
	case *parse.IdentifierNode:
		if !allowedFuncs[n.Ident] {
			return fmt.Errorf("function %q is not allowed", n.Ident)
		}
	case *parse.ChainNode:
		return checkNode(n.Node, tick)
	case *parse.IfNode:
		return checkBranch(&n.BranchNode, tick)
	case *parse.WithNode:
		return checkBranch(&n.BranchNode, tick)
	case *parse.RangeNode:
		if err := checkBranch(&n.BranchNode, tick); err != nil {
			return err
		}
		if tick != nil && n.List != nil {
			n.List.Nodes = append([]parse.Node{tick}, n.List.Nodes...)
		}
	case *parse.TemplateNode:
		return fmt.Errorf("template calls are not allowed")
	case *parse.TextNode, *parse.FieldNode, *parse.VariableNode, *parse.DotNode,
		*parse.StringNode, *parse.NumberNode, *parse.BoolNode, *parse.NilNode,
		*parse.BreakNode, *parse.ContinueNode, *parse.CommentNode:
	default:
		return fmt.Errorf("template element %T is not allowed", n)
	}
	return nil
}

func checkBranch(n *parse.BranchNode, tick *parse.ActionNode) error {
	for _, c := range []parse.Node{n.Pipe, n.List, n.ElseList} {
		if c == nil {
			continue
		}
		// typed nils must not reach checkNode as non-nil interfaces
		if l, ok := c.(*parse.ListNode); ok && l == nil {
			continue
		}
		if p, ok := c.(*parse.PipeNode); ok && p == nil {
			continue
		}
		if err := checkNode(c, tick); err != nil {
			return err
		}
	}
	return nil
}

// execTemplate renders one value template with the budget.
func execTemplate(text string, data map[string]any, b *budget) (string, error) {
	if !strings.Contains(text, "{{") {
		w := &budgetWriter{b: b}
		if _, err := w.Write([]byte(text)); err != nil {
			return "", err
		}
		return text, nil
	}
	t, err := parseTemplate(text, b)
	if err != nil {
		return "", err
	}
	w := &budgetWriter{b: b}
	if err := t.Execute(w, data); err != nil {
		if errors.Is(err, ErrRenderLimit) {
			return "", unwrapLimit(err)
		}
		return "", fmt.Errorf("template: %s", strings.TrimPrefix(err.Error(), "template: "))
	}
	return w.buf.String(), nil
}

// unwrapLimit returns the limit error inside a template error, so the
// message is short and errors.Is still works.
func unwrapLimit(err error) error {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.HasPrefix(e.Error(), ErrRenderLimit.Error()) {
			return e
		}
	}
	return err
}
