package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
)

// Limits bound the work of rendering one Target. Block templates are user
// input, so a template must not run for ever or fill the memory (D-052).
type Limits struct {
	// Timeout is the time limit for all templates of one Target.
	Timeout time.Duration
	// MaxOutput is the limit, in bytes, for the summed output of all
	// templates of one Target. Every string that a function returns
	// counts against it too.
	MaxOutput int
	// TotalTimeout and TotalOutput bound one Validate (and Load) call over
	// all Targets. Each Target render gets the smaller of its own budget
	// and the rest of the total.
	TotalTimeout time.Duration
	TotalOutput  int
}

// DefaultLimits are the limits of Validate, Load and the controller.
var DefaultLimits = Limits{
	Timeout: 2 * time.Second, MaxOutput: 1 << 20,
	TotalTimeout: 10 * time.Second, TotalOutput: 32 << 20,
}

// withDefaults returns the limits with every zero field set from
// DefaultLimits. A zero never means "no limit".
func (l Limits) withDefaults() Limits {
	if l.Timeout <= 0 {
		l.Timeout = DefaultLimits.Timeout
	}
	if l.MaxOutput <= 0 {
		l.MaxOutput = DefaultLimits.MaxOutput
	}
	if l.TotalTimeout <= 0 {
		l.TotalTimeout = DefaultLimits.TotalTimeout
	}
	if l.TotalOutput <= 0 {
		l.TotalOutput = DefaultLimits.TotalOutput
	}
	return l
}

// ErrRenderLimit is wrapped by every error from the limit of one Target render.
var ErrRenderLimit = errors.New("render limit")

// ErrTotalBudget is wrapped by the error from the total budget of one
// Validate call. It is not an ErrRenderLimit: the caller of Load can tell
// "the whole state is too slow to render" from "this Target is broken".
var ErrTotalBudget = errors.New("total render budget exceeded")

// limitError is a budget error. It keeps its short message when it comes
// back wrapped in a template error.
type limitError struct {
	kind error // ErrRenderLimit or ErrTotalBudget
	msg  string
}

func (e *limitError) Error() string {
	if strings.HasPrefix(e.msg, "(") {
		return e.kind.Error() + " " + e.msg
	}
	return e.kind.Error() + ": " + e.msg
}
func (e *limitError) Unwrap() error { return e.kind }

// budget is the allowance of one Target render. total is the shared
// allowance of the whole Validate call, or nil.
type budget struct {
	deadline time.Time
	timeout  time.Duration
	left     int // output bytes
	max      int
	total    *budget
}

func newBudget(lim Limits, total *budget) *budget {
	return &budget{
		deadline: time.Now().Add(lim.Timeout), timeout: lim.Timeout,
		left: lim.MaxOutput, max: lim.MaxOutput, total: total,
	}
}

// newTotal returns the budget of one Validate call. It starts now.
func newTotal(lim Limits) *budget {
	return &budget{
		deadline: time.Now().Add(lim.TotalTimeout), timeout: lim.TotalTimeout,
		left: lim.TotalOutput, max: lim.TotalOutput,
	}
}

func (b *budget) totalError(what string) error {
	return &limitError{ErrTotalBudget, fmt.Sprintf("%s (%v, %d bytes over all Targets)", what, b.total.timeout, b.total.max)}
}

// exhausted reports the total budget error when nothing is left of it.
func (b *budget) exhausted() error {
	if time.Now().After(b.deadline) || b.left <= 0 {
		return &limitError{ErrTotalBudget, fmt.Sprintf("(%v, %d bytes over all Targets)", b.timeout, b.max)}
	}
	return nil
}

func (b *budget) checkTime() error {
	now := time.Now()
	if b.total != nil && now.After(b.total.deadline) {
		return b.totalError("time is up")
	}
	if now.After(b.deadline) {
		return &limitError{ErrRenderLimit, "rendering took longer than the time limit"}
	}
	return nil
}

// fits checks the size of a value that a function is about to build.
// It does not use up the budget; only written output does.
func (b *budget) fits(n int) error {
	if n > b.max {
		return &limitError{ErrRenderLimit, fmt.Sprintf("a value is larger than %d bytes", b.max)}
	}
	if b.total != nil && n > b.total.left {
		return b.totalError("a value does not fit the rest of the output")
	}
	return b.checkTime()
}

// Write consumes output budget. It is the io.Writer of every template.
type budgetWriter struct {
	b   *budget
	buf strings.Builder
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	b := w.b
	if err := b.checkTime(); err != nil {
		return 0, err
	}
	if len(p) > b.left {
		return 0, &limitError{ErrRenderLimit, fmt.Sprintf("the output is larger than %d bytes", b.max)}
	}
	if b.total != nil {
		if len(p) > b.total.left {
			return 0, b.totalError("the output is too large")
		}
		b.total.left -= len(p)
	}
	b.left -= len(p)
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
				case number:
					parts[i] = string(e)
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

// number is how a template sees a JSON number: its canonical text. The
// type has no methods, so ".n.Float64" and similar calls fail, and no
// value goes through float64 (D-050).
type number string

// templateData converts the parameters for a template: every json.Number
// becomes a number. Strings, bools, lists and maps have no methods.
func templateData(v any) any {
	switch v := v.(type) {
	case json.Number:
		return number(v)
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			m[k] = templateData(e)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, e := range v {
			s[i] = templateData(e)
		}
		return s
	}
	return v
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
		var le *limitError
		if errors.As(err, &le) {
			return "", le
		}
		return "", fmt.Errorf("template: %s", strings.TrimPrefix(err.Error(), "template: "))
	}
	return w.buf.String(), nil
}
