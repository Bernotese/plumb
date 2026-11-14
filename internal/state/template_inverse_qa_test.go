package state

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// QA of Accept to a Block parameter (T-186 A3, D-362, review of #281).

const (
	// secretFile: the content is a writeOnly (secret) parameter.
	accSecret = `{"params":{"type":"object","properties":{"pw":{"type":"string","writeOnly":true}}},` +
		`"resources":[{"file":{"path":"/etc/pw","content":"{{ .pw }}"}}]}`
	// named: the parameter also names the resource (its id).
	accNamed = `{"params":{"type":"object","properties":{"name":{"type":"string","default":"a"}}},` +
		`"resources":[{"file":{"path":"/etc/{{ .name }}.conf","content":"{{ .name }}"}}]}`
)

func TestAcceptParamsQA(t *testing.T) {
	extra := []Doc{doc(KindBlock, "secret", accSecret), doc(KindBlock, "named", accNamed)}
	for _, c := range []struct {
		name, body, id string
		changes        []wire.Change
		mode, reason   string
		value          any
	}{
		// A hostile agent reports the secret file as a plain change: the
		// parameter is a secret, so no Accept may write the guest's text
		// into it (the guard of b.secrets, not only c.Sensitive).
		{"secret parameter, report not sensitive", `"blocks":[{"use":"secret","params":{"pw":{"secret":"db"}}}]`,
			"file:/etc/pw", []wire.Change{ch("content", "hunter2")}, AcceptRaw, reasonSecret, nil},
		// The parameter also builds the resource id: a new value would make
		// another resource, so it is raw.
		{"parameter in the resource id", `"blocks":[{"use":"named"}]`,
			"file:/etc/a.conf", []wire.Change{ch("content", "b")}, AcceptRaw, reasonSideEffect, nil},
		// Template syntax in the guest value stays text: the value is
		// accepted as it is, and nothing evaluates it.
		{"template text in the value", `"blocks":[{"use":"greet"}]`,
			"file:/etc/motd", []wire.Change{ch("content", "Welcome to {{ .pw }}!\n")}, AcceptParam, "", "{{ .pw }}"},
		{"integer with a plus", `"blocks":[{"use":"greet"}]`,
			"file:/etc/count", []wire.Change{ch("content", "+7")}, AcceptRaw, reasonType, nil},
		{"integer minus zero", `"blocks":[{"use":"greet"}]`,
			"file:/etc/count", []wire.Change{ch("content", "-0")}, AcceptRaw, reasonType, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := accSet(t, c.body, extra...)
			got := s.AcceptParams("t1", c.id, c.changes)
			if got.Mode != c.mode {
				t.Fatalf("mode %q (%s), want %q", got.Mode, got.Reason, c.mode)
			}
			if c.mode == AcceptRaw {
				if !strings.Contains(got.Reason, c.reason) || len(got.Params) != 0 {
					t.Fatalf("raw: %+v, want reason %q", got, c.reason)
				}
				return
			}
			if len(got.Params) != 1 || got.Params[0].Value != c.value {
				t.Fatalf("params %+v, want %#v", got.Params, c.value)
			}
			// The proof render with the literal value gives the observed text.
			next, ok := s.renderWith("t1", got.Params[0].Index, map[string]any{got.Params[0].Param: got.Params[0].Value})
			if !ok {
				t.Fatal("render with the value failed")
			}
			for _, r := range next.Resources {
				if r.ID == c.id && r.Attrs["content"] != c.changes[0].From {
					t.Fatalf("render %q, want %q", r.Attrs["content"], c.changes[0].From)
				}
			}
		})
	}
}
