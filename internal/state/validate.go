package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Error is one problem in one document. Path is a JSON pointer into the
// document, for example "/spec/blocks/0/params/ssh_key".
type Error struct {
	Kind string
	Name string
	Path string
	Msg  string
	// Err is the cause, if any (for example a wrapped ErrRenderLimit).
	Err error
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Error() string {
	path := e.Path
	if path == "" {
		path = "/"
	}
	return fmt.Sprintf("%s %q: %s: %s", e.Kind, e.Name, path, e.Msg)
}

// Errors is a list of document problems, ordered by kind, name and path.
type Errors []*Error

func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	return strings.Join(lines, "\n")
}

// Unwrap lets errors.Is and errors.As look into every entry.
func (es Errors) Unwrap() []error {
	out := make([]error, len(es))
	for i, e := range es {
		out[i] = e
	}
	return out
}

func (es Errors) sorted() Errors {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	return es
}

// ptr appends JSON pointer tokens to a path.
func ptr(base string, tokens ...any) string {
	var b strings.Builder
	b.WriteString(base)
	for _, t := range tokens {
		s := fmt.Sprint(t)
		s = strings.ReplaceAll(s, "~", "~0")
		s = strings.ReplaceAll(s, "/", "~1")
		b.WriteByte('/')
		b.WriteString(s)
	}
	return b.String()
}

// noLoader refuses every external schema reference. Schemas in documents
// (Block parameters) are user input: a "$ref" must never read a file or
// the network.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema references are not allowed: %s", url)
}

func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	return c
}

// compiled holds the schemas of the documents, of the guest settings and
// of the resource types. They are compiled once.
var compiled = sync.OnceValues(func() (*schemaSet, error) {
	s := &schemaSet{docs: map[string]*jsonschema.Schema{}, resources: map[string]*jsonschema.Schema{}}
	c := newCompiler()
	add := func(raw []byte) (string, error) {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		var head struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil || head.ID == "" {
			return "", fmt.Errorf("schema has no $id")
		}
		return head.ID, c.AddResource(head.ID, doc)
	}
	ids := map[string]string{}
	for _, kind := range Kinds {
		raw, _ := Schema(kind)
		id, err := add(raw)
		if err != nil {
			return nil, fmt.Errorf("state: schema of %s: %w", kind, err)
		}
		ids[kind] = id
	}
	for _, name := range resource.Names() {
		typ, _ := resource.Lookup(name)
		id, err := add(typ.Schema())
		if err != nil {
			return nil, fmt.Errorf("state: schema of resource %s: %w", name, err)
		}
		if s.resources[name], err = c.Compile(id); err != nil {
			return nil, fmt.Errorf("state: schema of resource %s: %w", name, err)
		}
	}
	for kind, id := range ids {
		var err error
		if s.docs[kind], err = c.Compile(id); err != nil {
			return nil, fmt.Errorf("state: schema of %s: %w", kind, err)
		}
	}
	var err error
	if s.guest, err = c.Compile(ids[KindTarget] + "#/properties/spec/properties/guest"); err != nil {
		return nil, fmt.Errorf("state: guest schema: %w", err)
	}
	// Defaults of the guest settings, for the guest:config resource.
	var target struct {
		Properties struct {
			Spec struct {
				Properties struct {
					Guest struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"guest"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	raw, _ := Schema(KindTarget)
	if err := json.Unmarshal(raw, &target); err != nil {
		return nil, err
	}
	if s.guestDefaults, err = defaultsOf(target.Properties.Spec.Properties.Guest.Properties); err != nil {
		return nil, err
	}
	return s, nil
})

type schemaSet struct {
	docs          map[string]*jsonschema.Schema
	resources     map[string]*jsonschema.Schema
	guest         *jsonschema.Schema
	guestDefaults map[string]any
}

func mustSchemas() *schemaSet {
	s, err := compiled()
	if err != nil {
		panic(err) // build mistake; the tests catch it
	}
	return s
}

// schemaErrors turns a validation error into document errors with paths.
// base is prefixed to every instance location.
func schemaErrors(kind, name, base string, err error) Errors {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return Errors{{Kind: kind, Name: name, Path: base, Msg: err.Error()}}
	}
	var out Errors
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			u := e.BasicOutput()
			msg := "invalid"
			if u.Error != nil {
				msg = u.Error.String()
			}
			out = append(out, &Error{Kind: kind, Name: name, Path: base + u.InstanceLocation, Msg: msg})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}

// decode parses JSON and keeps number literals (no float64).
func decode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// defaultsOf reads the "default" of each property of a schema.
func defaultsOf(props map[string]json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	for name, raw := range props {
		var p struct {
			Default json.RawMessage `json:"default"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			continue // not an object; the schema compiler reports it
		}
		if p.Default == nil {
			continue
		}
		var v any
		if err := decode(p.Default, &v); err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

// withDefaults returns a copy of values in which every missing property
// with a default has that default. Defaults are deep-copied.
func withDefaults(values, defaults map[string]any) map[string]any {
	out := make(map[string]any, len(values)+len(defaults))
	for k, v := range defaults {
		out[k] = deepCopy(v)
	}
	for k, v := range values {
		out[k] = deepCopy(v)
	}
	return out
}

func deepCopy(v any) any {
	switch v := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, e := range v {
			s[i] = deepCopy(e)
		}
		return s
	}
	return v
}

// --- parsed documents ---

type imageSpec struct {
	Type string `json:"type"`
}

type blockUse struct {
	Use    string         `json:"use"`
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

// id is the id of the use: the "id" field, or the Block name (D-048).
func (u blockUse) id() string {
	if u.ID != "" {
		return u.ID
	}
	return u.Use
}

type targetSpec struct {
	Type      string                    `json:"type"`
	Image     string                    `json:"image"`
	Guest     map[string]any            `json:"guest"`
	Blocks    []blockUse                `json:"blocks"`
	Overrides map[string]map[string]any `json:"overrides"`
}

type blockResource struct {
	Type  string
	Attrs map[string]any
}

type block struct {
	params    *jsonschema.Schema // nil: no parameters
	props     map[string]bool    // declared parameter names
	defaults  map[string]any
	resources []blockResource
}

// Set is a validated set of documents: every document matches its schema,
// every reference resolves, and every Target renders. Only Validate makes
// a Set, so a *Set is proof of that.
type Set struct {
	docs    []Doc // ordered by kind, name
	images  map[string]imageSpec
	blocks  map[string]*block
	targets map[string]*targetSpec
	limits  Limits
}

// Docs returns the documents of the set, ordered by kind and name.
func (s *Set) Docs() []Doc { return append([]Doc(nil), s.docs...) }

// Targets returns the names of all Targets, sorted.
func (s *Set) Targets() []string {
	names := make([]string, 0, len(s.targets))
	for n := range s.targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Validate checks a full document set with the default render limits.
// The error is of type Errors and lists every problem found.
func Validate(docs []Doc) (*Set, error) { return ValidateWithLimits(docs, DefaultLimits) }

// ValidateWithLimits is Validate with explicit render limits. The Set
// keeps them for its Render calls.
func ValidateWithLimits(docs []Doc, lim Limits) (*Set, error) {
	sch := mustSchemas()
	s := &Set{
		images:  map[string]imageSpec{},
		blocks:  map[string]*block{},
		targets: map[string]*targetSpec{},
		limits:  lim,
	}
	var errs Errors
	seen := map[[2]string]bool{}
	for _, d := range docs {
		key := [2]string{d.Kind, d.Name}
		if seen[key] {
			errs = append(errs, &Error{Kind: d.Kind, Name: d.Name, Msg: "document is twice in the set"})
			continue
		}
		seen[key] = true
		docSchema, ok := sch.docs[d.Kind]
		if !ok {
			errs = append(errs, &Error{Kind: d.Kind, Name: d.Name, Path: "/kind", Msg: "unknown document kind"})
			continue
		}
		// Validate the canonical form, so that what is checked is what is stored.
		body, err := Canonical(d)
		if err != nil {
			errs = append(errs, &Error{Kind: d.Kind, Name: d.Name, Path: "/spec", Msg: err.Error()})
			continue
		}
		var inst any
		if err := decode(body, &inst); err != nil {
			errs = append(errs, &Error{Kind: d.Kind, Name: d.Name, Msg: err.Error()})
			continue
		}
		if err := docSchema.Validate(inst); err != nil {
			errs = append(errs, schemaErrors(d.Kind, d.Name, "", err)...)
			continue
		}
		var canon Doc
		_ = json.Unmarshal(body, &canon)
		s.docs = append(s.docs, canon)
		switch d.Kind {
		case KindImage:
			var spec imageSpec
			_ = decode(canon.Spec, &spec)
			s.images[d.Name] = spec
		case KindBlock:
			b, berrs := parseBlock(d.Name, canon.Spec)
			errs = append(errs, berrs...)
			if b != nil {
				s.blocks[d.Name] = b
			}
		case KindTarget:
			var spec targetSpec
			_ = decode(canon.Spec, &spec)
			s.targets[d.Name] = &spec
		}
	}
	sort.Slice(s.docs, func(i, j int) bool {
		if s.docs[i].Kind != s.docs[j].Kind {
			return s.docs[i].Kind < s.docs[j].Kind
		}
		return s.docs[i].Name < s.docs[j].Name
	})
	for _, name := range s.Targets() {
		if terrs := s.checkTarget(name); len(terrs) > 0 {
			errs = append(errs, terrs...)
			continue
		}
		if _, err := s.Render(name); err != nil {
			var es Errors
			if errors.As(err, &es) {
				errs = append(errs, es...)
			} else {
				errs = append(errs, &Error{Kind: KindTarget, Name: name, Msg: err.Error()})
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}
	return s, nil
}

// parseBlock compiles the parameter schema and parses every template of a
// Block. The Block already matches its document schema.
func parseBlock(name string, spec json.RawMessage) (*block, Errors) {
	var raw struct {
		Params    json.RawMessage             `json:"params"`
		Resources []map[string]map[string]any `json:"resources"`
	}
	_ = decode(spec, &raw)
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: KindBlock, Name: name, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	b := &block{props: map[string]bool{}, defaults: map[string]any{}}
	if raw.Params != nil {
		var p struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(raw.Params, &p)
		for prop := range p.Properties {
			b.props[prop] = true
		}
		var err error
		if b.defaults, err = defaultsOf(p.Properties); err != nil {
			errs = append(errs, errf("/spec/params", "bad default: %v", err))
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw.Params))
		var c *jsonschema.Compiler
		const url = "mem:///params.json"
		if err == nil {
			c = newCompiler()
			if err = c.AddResource(url, doc); err == nil {
				b.params, err = c.Compile(url)
			}
		}
		if err != nil {
			errs = append(errs, errf("/spec/params", "not a valid JSON Schema: %v", err))
		}
		// Each default must itself be a valid value of its property.
		if b.params != nil {
			props := make([]string, 0, len(b.defaults))
			for prop := range b.defaults {
				props = append(props, prop)
			}
			sort.Strings(props)
			for _, prop := range props {
				path := ptr("/spec/params/properties", prop)
				sub, err := c.Compile(url + "#" + path[len("/spec/params"):])
				if err == nil {
					err = sub.Validate(b.defaults[prop])
				}
				if err != nil {
					errs = append(errs, schemaErrors(KindBlock, name, path+"/default", err)...)
				}
			}
		}
	}
	for i, entry := range raw.Resources {
		for typ, attrs := range entry { // exactly one entry (schema)
			path := ptr("/spec/resources", i, typ)
			rt, ok := resource.Lookup(typ)
			if !ok {
				errs = append(errs, errf(path, "unknown resource type %q", typ))
				continue
			}
			if _, ok := attrs[rt.Key]; !ok {
				errs = append(errs, errf(path, "missing key attribute %q", rt.Key))
			}
			walkStrings(attrs, path, func(p, s string) {
				if _, err := parseTemplate(s, nil); err != nil {
					errs = append(errs, errf(p, "%v", err))
				}
			})
			b.resources = append(b.resources, blockResource{Type: typ, Attrs: attrs})
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return b, nil
}

// walkStrings calls fn for every string inside v, with its JSON pointer.
func walkStrings(v any, path string, fn func(path, s string)) {
	switch v := v.(type) {
	case string:
		fn(path, v)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkStrings(v[k], ptr(path, k), fn)
		}
	case []any:
		for i, e := range v {
			walkStrings(e, ptr(path, strconv.Itoa(i)), fn)
		}
	}
}

// checkTarget checks the references of a Target: its image, its Blocks,
// the ids of the Block uses and the parameters.
func (s *Set) checkTarget(name string) Errors {
	t := s.targets[name]
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: KindTarget, Name: name, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	if img, ok := s.images[t.Image]; !ok {
		errs = append(errs, errf("/spec/image", "Image %q does not exist", t.Image))
	} else if img.Type != t.Type {
		errs = append(errs, errf("/spec/image", "Image %q is for %s, the Target is %s", t.Image, img.Type, t.Type))
	}
	ids := map[string]int{}
	for i, use := range t.Blocks {
		path := ptr("/spec/blocks", i)
		if first, dup := ids[use.id()]; dup {
			errs = append(errs, errf(path+"/id",
				"Block use id %q is already used by /spec/blocks/%d; give each use of a Block its own id (D-048)", use.id(), first))
		} else {
			ids[use.id()] = i
		}
		b, ok := s.blocks[use.Use]
		if !ok {
			errs = append(errs, errf(path+"/use", "Block %q does not exist", use.Use))
			continue
		}
		var unknown []string
		for p := range use.Params {
			if !b.props[p] {
				unknown = append(unknown, p)
			}
		}
		sort.Strings(unknown)
		for _, p := range unknown {
			errs = append(errs, errf(ptr(path+"/params", p), "Block %q has no parameter %q", use.Use, p))
		}
		if len(unknown) > 0 || b.params == nil {
			continue
		}
		params := withDefaults(use.Params, b.defaults)
		if err := b.params.Validate(params); err != nil {
			errs = append(errs, schemaErrors(KindTarget, name, path+"/params", err)...)
		}
	}
	return errs
}
