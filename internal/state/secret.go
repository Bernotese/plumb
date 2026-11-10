package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// Secret references (D-088). A Block parameter with type string and
// writeOnly true is a secret parameter. In a Target its value is
// {"secret": "<name>"}. The template sees an opaque value: plain output
// writes a marker, and every function, builtin and action that would read
// it fails by type. The render then checks every marker and rewrites it
// to the stable form MarkStart <name> MarkEnd. package state never sees a
// secret value; package plan replaces the markers (WithSecrets).

// Marker characters (Unicode private use area). No document may hold them.
const (
	MarkStart = '\uE000'
	MarkEnd   = '\uE001'
)

const markChars = "\uE000\uE001"

var secretName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

// refName returns the name of a reference {"secret": "<name>"}. ok is
// false for any other value. A map with a "secret" key and other keys, or
// a non-string name, is a broken reference: ok is true and name is "".
func refName(v any) (name string, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return "", false
	}
	raw, has := m["secret"]
	if !has {
		return "", false
	}
	s, isStr := raw.(string)
	if !isStr || len(m) != 1 || !secretName.MatchString(s) {
		return "", true
	}
	return s, true
}

// Marker returns the stable marker of a secret, as a render stores it.
func Marker(name string) string { return string(MarkStart) + name + string(MarkEnd) }

// secretValue is the template value of a secret parameter. Its only
// method is Format (fmt calls it for {{ .p }}); a template cannot call it,
// because it needs an fmt.State. The marker field is unexported, so field
// access fails. The slice field makes the type non-comparable, so eq and
// ne fail. Functions that take a string fail by type; len, index, slice
// and range fail on a struct (D-088, T-029 table test).
type secretValue struct {
	marker string
	_      []byte
}

func (v secretValue) Format(f fmt.State, _ rune) { io.WriteString(f, v.marker) }

// nonce is the random part of the markers of one render.
func newNonce() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func renderMarker(nonce, name string) string {
	return string(MarkStart) + nonce + name + string(MarkEnd)
}

// finalizeMarkers rewrites the render markers in every string of v to the
// stable form and refuses every other marker character. path names v for
// the error.
func finalizeMarkers(v any, nonce, path string) (any, error) {
	switch v := v.(type) {
	case string:
		if !strings.ContainsAny(v, markChars) {
			return v, nil
		}
		var b strings.Builder
		rest := v
		for {
			i := strings.IndexAny(rest, markChars)
			if i < 0 {
				b.WriteString(rest)
				return b.String(), nil
			}
			b.WriteString(rest[:i])
			rest = rest[i:]
			if !strings.HasPrefix(rest, string(MarkStart)+nonce) {
				return nil, fmt.Errorf("%s: the output holds a character U+E000 or U+E001 that is not a secret marker of this render; a template cannot make a secret marker", path)
			}
			body := rest[len(string(MarkStart))+len(nonce):]
			end := strings.IndexRune(body, MarkEnd)
			if end < 0 || !secretName.MatchString(body[:end]) {
				return nil, fmt.Errorf("%s: a secret marker is broken; a template cannot cut or join a secret", path)
			}
			b.WriteString(Marker(body[:end]))
			rest = body[end+len(string(MarkEnd)):]
		}
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			r, err := finalizeMarkers(e, nonce, ptr(path, k))
			if err != nil {
				return nil, err
			}
			m[k] = r
		}
		return m, nil
	case []any:
		s := make([]any, len(v))
		for i, e := range v {
			r, err := finalizeMarkers(e, nonce, ptr(path, i))
			if err != nil {
				return nil, err
			}
			s[i] = r
		}
		return s, nil
	}
	return v, nil
}

// stableNames adds the names of the stable markers inside v to names.
func stableNames(v any, names map[string]bool) {
	switch v := v.(type) {
	case string:
		rest := v
		for {
			i := strings.IndexRune(rest, MarkStart)
			if i < 0 {
				return
			}
			rest = rest[i+len(string(MarkStart)):]
			end := strings.IndexRune(rest, MarkEnd)
			if end < 0 {
				return
			}
			names[rest[:end]] = true
			rest = rest[end+len(string(MarkEnd)):]
		}
	case map[string]any:
		for _, e := range v {
			stableNames(e, names)
		}
	case []any:
		for _, e := range v {
			stableNames(e, names)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResourceSecrets returns the names of the secrets in one rendered
// resource, sorted.
func ResourceSecrets(r wire.Resource) []string {
	names := map[string]bool{}
	stableNames(r.Attrs, names)
	return sortedKeys(names)
}

// SecretNames returns the names of all secrets in a render, sorted.
func SecretNames(r *Rendered) []string {
	names := map[string]bool{}
	for _, res := range r.All() {
		stableNames(res.Attrs, names)
	}
	return sortedKeys(names)
}

// WithSecrets returns a deep copy of the resources with every marker
// replaced by the value of its secret. Only package plan calls it, and
// only for the body of an observe, apply or watch command (D-089). A
// missing value is an error: a marker or an empty value never reaches a
// guest.
func WithSecrets(rs []wire.Resource, values map[string]string) ([]wire.Resource, error) {
	out := make([]wire.Resource, len(rs))
	for i, r := range rs {
		attrs, err := replaceMarkers(r.Attrs, values)
		if err != nil {
			return nil, fmt.Errorf("state: %s: %w", r.ID, err)
		}
		r.Attrs = attrs.(map[string]any)
		r.Sensitive = append([]string(nil), r.Sensitive...)
		out[i] = r
	}
	return out, nil
}

func replaceMarkers(v any, values map[string]string) (any, error) {
	switch v := v.(type) {
	case string:
		if !strings.ContainsRune(v, MarkStart) {
			return v, nil
		}
		var b strings.Builder
		rest := v
		for {
			i := strings.IndexRune(rest, MarkStart)
			if i < 0 {
				b.WriteString(rest)
				return b.String(), nil
			}
			b.WriteString(rest[:i])
			rest = rest[i+len(string(MarkStart)):]
			end := strings.IndexRune(rest, MarkEnd)
			if end < 0 {
				return nil, fmt.Errorf("a secret marker is broken")
			}
			val, ok := values[rest[:end]]
			if !ok || val == "" {
				return nil, fmt.Errorf("secret %q has no value", rest[:end])
			}
			b.WriteString(val)
			rest = rest[end+len(string(MarkEnd)):]
		}
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			r, err := replaceMarkers(e, values)
			if err != nil {
				return nil, err
			}
			m[k] = r
		}
		return m, nil
	case []any:
		s := make([]any, len(v))
		for i, e := range v {
			r, err := replaceMarkers(e, values)
			if err != nil {
				return nil, err
			}
			s[i] = r
		}
		return s, nil
	}
	return v, nil
}

// SecretRef is a reference of a Target to a secret, with the JSON pointer
// of the parameter that holds it.
type SecretRef struct {
	Name string
	Path string // for example /spec/blocks/0/params/db_password
}

// SecretRefs returns the secret references of a Target, in Block order.
func (s *Set) SecretRefs(target string) []SecretRef {
	t, ok := s.targets[target]
	if !ok {
		return nil
	}
	var out []SecretRef
	for i, use := range t.Blocks {
		b := s.blocks[use.Use]
		if b == nil {
			continue
		}
		params := make([]string, 0, len(use.Params))
		for p := range use.Params {
			params = append(params, p)
		}
		sort.Strings(params)
		for _, p := range params {
			if name, ok := refName(use.Params[p]); ok && b.secrets[p] && name != "" {
				out = append(out, SecretRef{Name: name, Path: ptr("/spec/blocks", i, "params", p)})
			}
		}
	}
	return out
}

// checkNoMarkChars refuses a document value or key with a marker
// character (D-088).
func checkNoMarkChars(v any, path string) string {
	switch v := v.(type) {
	case string:
		if strings.ContainsAny(v, markChars) {
			return path
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.ContainsAny(k, markChars) {
				return ptr(path, k)
			}
			if p := checkNoMarkChars(v[k], ptr(path, k)); p != "" {
				return p
			}
		}
	case []any:
		for i, e := range v {
			if p := checkNoMarkChars(e, ptr(path, i)); p != "" {
				return p
			}
		}
	}
	return ""
}

// refSchema is the parameter schema of a secret parameter after parsing:
// the value in a Target is a reference, never a literal.
var refSchema = map[string]any{
	"type":                 "object",
	"required":             []any{"secret"},
	"additionalProperties": false,
	"properties": map[string]any{
		"secret": map[string]any{"type": "string", "pattern": secretName.String()},
	},
}

// secretParams finds the secret parameters of a Block parameter schema
// and returns the schema with each of them replaced by refSchema. A
// writeOnly parameter must be a top-level property with type string and
// no default; writeOnly anywhere else is an error, so nobody believes a
// nested value is protected.
func secretParams(block string, raw []byte, secrets map[string]bool) ([]byte, Errors) {
	var schema map[string]any
	if err := decode(raw, &schema); err != nil {
		return raw, nil // the compile step reports it
	}
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: KindBlock, Name: block, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	props, _ := schema["properties"].(map[string]any)
	for _, name := range sortedAnyKeys(props) {
		prop, _ := props[name].(map[string]any)
		if prop == nil || prop["writeOnly"] != true {
			continue
		}
		path := ptr("/spec/params/properties", name)
		if prop["type"] != "string" {
			errs = append(errs, errf(path, "a secret parameter (writeOnly) must have type string"))
		}
		if _, has := prop["default"]; has {
			errs = append(errs, errf(path+"/default", "a secret parameter cannot have a default; the value would be a literal in the Block"))
		}
		secrets[name] = true
		props[name] = refSchema
	}
	var nested func(v any, path string)
	nested = func(v any, path string) {
		switch v := v.(type) {
		case map[string]any:
			for _, k := range sortedAnyKeys(v) {
				if k == "writeOnly" && v[k] == true {
					errs = append(errs, errf(ptr(path, k), "writeOnly is only allowed on a top-level string parameter (a secret parameter)"))
					continue
				}
				nested(v[k], ptr(path, k))
			}
		case []any:
			for i, e := range v {
				nested(e, ptr(path, i))
			}
		}
	}
	nested(schema, "/spec/params")
	out, err := json.Marshal(schema)
	if err != nil {
		return raw, append(errs, errf("/spec/params", "%v", err))
	}
	return out, errs
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedParams(m map[string]any) []string { return sortedAnyKeys(m) }
