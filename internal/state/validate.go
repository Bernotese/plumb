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
	"github.com/Bernotese/plumb/internal/wire"
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
	s := &schemaSet{docs: map[string]*jsonschema.Schema{}, resources: map[string]*jsonschema.Schema{},
		props: map[string]map[string]*jsonschema.Schema{}}
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
		var head struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(typ.Schema(), &head); err != nil {
			return nil, fmt.Errorf("state: schema of resource %s: %w", name, err)
		}
		s.props[name] = map[string]*jsonschema.Schema{}
		for attr := range head.Properties {
			if s.props[name][attr], err = c.Compile(id + "#" + ptr("/properties", attr)); err != nil {
				return nil, fmt.Errorf("state: schema of resource %s attribute %s: %w", name, attr, err)
			}
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
	// Defaults of the build guest of an Image (D-197).
	var image struct {
		Properties struct {
			Spec struct {
				Properties struct {
					Build struct {
						Properties struct {
							Guest struct {
								Properties map[string]json.RawMessage `json:"properties"`
							} `json:"guest"`
						} `json:"properties"`
					} `json:"build"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	raw, _ = Schema(KindImage)
	if err := json.Unmarshal(raw, &image); err != nil {
		return nil, err
	}
	if s.buildGuestDefaults, err = defaultsOf(image.Properties.Spec.Properties.Build.Properties.Guest.Properties); err != nil {
		return nil, err
	}
	return s, nil
})

type schemaSet struct {
	docs      map[string]*jsonschema.Schema
	resources map[string]*jsonschema.Schema
	// props: per resource type, the schema of each attribute (its entry
	// in properties), for the attribute check of a Block at save (T-101).
	props         map[string]map[string]*jsonschema.Schema
	guest         *jsonschema.Schema
	guestDefaults map[string]any
	// buildGuestDefaults: defaults of spec.build.guest of an Image.
	buildGuestDefaults map[string]any
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
	Type      string      `json:"type"`
	URL       string      `json:"url"`
	SHA256    string      `json:"sha256"`
	SHA512    string      `json:"sha512"` // lxc only (D-224)
	Build     *imageBuild `json:"build"`
	Retention *struct {
		Keep int `json:"keep"`
	} `json:"retention"`
	Schedule json.RawMessage `json:"schedule"`
}

// imageBuild is spec.build of an Image (D-197).
type imageBuild struct {
	Blocks  []blockUse     `json:"blocks"`
	Upgrade *bool          `json:"upgrade"` // nil: the default, true
	Guest   map[string]any `json:"guest"`
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
	Type  string         `json:"type"`
	Image string         `json:"image"`
	Guest map[string]any `json:"guest"`
	// AllowRestart: a Run may restart the running VM (D-194). A rule of
	// Plumb, not a Proxmox setting; the schema default is false.
	AllowRestart bool                      `json:"allow_restart"`
	Blocks       []blockUse                `json:"blocks"`
	Overrides    map[string]map[string]any `json:"overrides"`
}

type blockResource struct {
	Type  string
	Attrs map[string]any
}

type block struct {
	params    *jsonschema.Schema // nil: no parameters
	props     map[string]bool    // declared parameter names
	secrets   map[string]bool    // secret parameters: writeOnly strings (D-088)
	defaults  map[string]any
	resources []blockResource
	after     []string // spec.after: resource ids that each use comes after (D-167)
	restarts  []string // spec.restarts: services that restart when a use changes (D-172)
}

// Set is a validated set of documents: every document matches its schema,
// every reference resolves, and every Target renders. Only Validate makes
// a Set, so a *Set is proof of that.
type Set struct {
	docs    []Doc // ordered by kind, name
	images  map[string]imageSpec
	blocks  map[string]*block
	targets map[string]*targetSpec
	rules   map[string]Rule
	limits  Limits
	// invalid holds documents that are in the input but have errors, so
	// a reference to them is "invalid", not "does not exist".
	invalid map[[2]string]bool
	// stored: the set is a stored revision (Load). An apply-order cycle of
	// a Target is then no error; it goes to cycles (D-168).
	stored bool
	cycles map[string]string // Target → cycle text
}

// Cycles returns the Targets of a stored revision whose apply order has a
// cycle, with the text of the cycle. Such a revision is from before the
// save-time check of D-168: it still loads, so that a proposal can repair
// it, and its applies fail at the agent (D-055). A set from Validate has
// none.
func (s *Set) Cycles() map[string]string {
	out := make(map[string]string, len(s.cycles))
	for t, msg := range s.cycles {
		out[t] = msg
	}
	return out
}

// Docs returns the documents of the set, ordered by kind and name.
// Each Spec is a copy, so a caller cannot change the validated Set.
func (s *Set) Docs() []Doc {
	out := make([]Doc, len(s.docs))
	for i, d := range s.docs {
		d.Spec = append(json.RawMessage(nil), d.Spec...)
		out[i] = d
	}
	return out
}

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
// A zero field of lim takes its value from DefaultLimits.
func ValidateWithLimits(docs []Doc, lim Limits) (*Set, error) { return validate(docs, lim, false) }

// validate makes a Set. stored is true for the documents of a stored
// revision (Load): an apply-order cycle is then kept as a warning.
func validate(docs []Doc, lim Limits, stored bool) (*Set, error) {
	lim = lim.withDefaults()
	sch := mustSchemas()
	s := &Set{
		images:  map[string]imageSpec{},
		blocks:  map[string]*block{},
		targets: map[string]*targetSpec{},
		rules:   map[string]Rule{},
		limits:  lim,
		invalid: map[[2]string]bool{},
		stored:  stored,
		cycles:  map[string]string{},
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
		s.invalid[key] = true // cleared below when the document is accepted
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
		if p := checkNoMarkChars(inst, ""); p != "" {
			errs = append(errs, &Error{Kind: d.Kind, Name: d.Name, Path: p,
				Msg: "the characters U+E000 and U+E001 are reserved for secret markers and cannot be in a document"})
			continue
		}
		if err := docSchema.Validate(inst); err != nil {
			errs = append(errs, schemaErrors(d.Kind, d.Name, "", err)...)
			continue
		}
		var canon Doc
		_ = json.Unmarshal(body, &canon)
		if d.Kind == KindImage {
			var spec imageSpec
			_ = decode(canon.Spec, &spec)
			if ierrs := checkImageSpec(d.Name, spec); len(ierrs) > 0 {
				errs = append(errs, ierrs...)
				continue
			}
		}
		if d.Kind == KindBlock {
			b, berrs := parseBlock(d.Name, canon.Spec)
			if len(berrs) > 0 {
				errs = append(errs, berrs...)
				continue
			}
			s.blocks[d.Name] = b
		}
		delete(s.invalid, key)
		s.docs = append(s.docs, canon)
		switch d.Kind {
		case KindImage:
			var spec imageSpec
			_ = decode(canon.Spec, &spec)
			s.images[d.Name] = spec
		case KindTarget:
			var spec targetSpec
			_ = decode(canon.Spec, &spec)
			s.targets[d.Name] = &spec
		case KindRule:
			r := Rule{Name: d.Name}
			_ = decode(canon.Spec, &r)
			s.rules[d.Name] = r
		}
	}
	// A Rule names an existing Target or "*" (D-092).
	for _, name := range sortedKeys(s.rules) {
		if r := s.rules[name]; r.Target != RuleAllTargets && s.targets[r.Target] == nil {
			errs = append(errs, &Error{Kind: KindRule, Name: name, Path: "/spec/target",
				Msg: fmt.Sprintf("Target %q %s", r.Target, s.missing(KindTarget, r.Target))})
		}
	}
	sort.Slice(s.docs, func(i, j int) bool {
		if s.docs[i].Kind != s.docs[j].Kind {
			return s.docs[i].Kind < s.docs[j].Kind
		}
		return s.docs[i].Name < s.docs[j].Name
	})
	// One total budget for all Targets (D-052): the cost of a save or a
	// load must not grow without bound with the number of Targets.
	total := newTotal(lim)
	for _, name := range s.Targets() {
		if terrs := s.checkTarget(name); len(terrs) > 0 {
			errs = append(errs, terrs...)
			continue
		}
		if err := total.exhausted(); err != nil {
			errs = append(errs, &Error{Kind: KindTarget, Name: name, Msg: err.Error(), Err: err})
			break
		}
		r, err := s.render(name, total)
		if err == nil && r.cycle != "" {
			s.cycles[name] = r.cycle
		}
		if err != nil {
			var es Errors
			if errors.As(err, &es) {
				errs = append(errs, es...)
			} else {
				errs = append(errs, &Error{Kind: KindTarget, Name: name, Msg: err.Error()})
			}
			if errors.Is(err, ErrTotalBudget) {
				break // one error, not one per remaining Target
			}
		}
	}
	// The build of each built Image renders like a Target, on the same
	// budget, so a change of a shared Block is checked for it too (n3).
	for _, name := range sortedKeys(s.images) {
		u, ok := s.imageUnit(name)
		if !ok {
			continue
		}
		if err := total.exhausted(); err != nil {
			errs = append(errs, &Error{Kind: KindImage, Name: name, Msg: err.Error(), Err: err})
			break
		}
		if _, err := s.renderUnit(u, total); err != nil {
			var es Errors
			if errors.As(err, &es) {
				errs = append(errs, es...)
			} else {
				errs = append(errs, &Error{Kind: KindImage, Name: name, Msg: err.Error()})
			}
			if errors.Is(err, ErrTotalBudget) {
				break
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
		After     []string                    `json:"after"`
		Restarts  []string                    `json:"restarts"`
	}
	_ = decode(spec, &raw)
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: KindBlock, Name: name, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	b := &block{props: map[string]bool{}, secrets: map[string]bool{}, defaults: map[string]any{}}
	// after names resources that the agent applies (D-168). Whether they
	// are on a Target is a question of the Target: render checks it.
	for i, id := range raw.After {
		typ, _, _ := wire.SplitID(id)
		if _, ok := resource.Lookup(typ); !ok {
			errs = append(errs, errf(ptr("/spec/after", i), "after: %q has no known resource type", id))
		}
	}
	b.after, b.restarts = raw.After, raw.Restarts
	if raw.Params != nil {
		var p struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(raw.Params, &p)
		for prop := range p.Properties {
			b.props[prop] = true
		}
		var serrs Errors
		raw.Params, serrs = secretParams(name, raw.Params, b.secrets)
		errs = append(errs, serrs...)
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

// checkUnit checks a Target or the build of an Image before render.
func (s *Set) checkUnit(u *unit) Errors {
	if u.kind == KindImage {
		return s.checkUses(u)
	}
	return s.checkTarget(u.name)
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
		errs = append(errs, errf("/spec/image", "Image %q %s", t.Image, s.missing(KindImage, t.Image)))
	} else if img.Type != t.Type {
		errs = append(errs, errf("/spec/image", "Image %q is for %s, the Target is %s", t.Image, img.Type, t.Type))
	} else if img.Build != nil {
		// A disk cannot shrink: the template has the disk of the build
		// guest (D-202, host A4).
		want, _ := intOf(img.Build.Guest["disk"])
		if have, ok := intOf(t.Guest["disk"]); ok && have < want {
			errs = append(errs, errf("/spec/guest/disk",
				"disk %d GiB is smaller than the disk of Image %q (%d GiB); a disk cannot shrink (D-202)", have, t.Image, want))
		}
	}
	// A Run may stop the guest only with allow_restart (D-291, D-299),
	// also when an override of guest:config sets the power.
	if !t.AllowRestart {
		if p, _ := t.Guest["power"].(string); p == "stopped" {
			errs = append(errs, errf("/spec/guest/power", "power: stopped needs allow_restart: true (a Run may stop the guest only then, D-291)"))
		}
		if p, _ := t.Overrides["guest:config"]["power"].(string); p == "stopped" {
			errs = append(errs, errf("/spec/overrides/guest:config/power", "power: stopped needs allow_restart: true (a Run may stop the guest only then, D-291)"))
		}
	}
	if t.Type == string(wire.KindLXC) {
		// Proxmox has no cloud-init for containers: keys go to root (D-226).
		if _, ok := t.Guest["user"]; ok {
			errs = append(errs, errf("/spec/guest/user", "an LXC Target has no bootstrap user; use a Block such as deploy-user (D-226)"))
		}
		if have, ok := intOf(t.Guest["disk"]); ok && have < minLXCDisk {
			errs = append(errs, errf("/spec/guest/disk", "the root file system of an LXC Target needs at least %d GiB", minLXCDisk))
		}
		// Proxmox writes ssh-public-keys only at a create from a template
		// file, never at a clone of a version (D-251).
		if img, ok := s.images[t.Image]; ok && img.Build != nil {
			if keys, _ := t.Guest["ssh_keys"].([]any); len(keys) > 0 {
				errs = append(errs, errf("/spec/guest/ssh_keys", "a container from the built Image %q gets no SSH keys from Proxmox; add them with a Block such as deploy-user (D-251)", t.Image))
			}
		}
		// Proxmox keeps no copy of a container's keys; the driver writes
		// one description line per key, and the description holds 8 KiB
		// (D-249).
		if keys, ok := t.Guest["ssh_keys"].([]any); ok {
			n := 0
			for i, k := range keys {
				if s, ok := k.(string); ok {
					n += len(lxcKeyLine) + len(s) + 1
					// The line must come back byte-equal from the
					// description (review S1 of #162).
					if strings.TrimSpace(s) != s || strings.IndexFunc(s, isControl) >= 0 {
						errs = append(errs, errf(fmt.Sprintf("/spec/guest/ssh_keys/%d", i), "an SSH key of an LXC Target must not start or end with white space or hold a control character (D-249)"))
					}
				}
			}
			if n > maxLXCKeyBytes {
				errs = append(errs, errf("/spec/guest/ssh_keys", "the SSH keys of an LXC Target take %d bytes; at most %d fit (Plumb keeps them in the container description, D-249)", n, maxLXCKeyBytes))
			}
		}
	}
	// guest and overrides never hold a reference, at any depth (D-088).
	if at := findRef(t.Guest, "/spec/guest"); at != "" {
		errs = append(errs, errf(at, "guest settings cannot hold a secret reference (D-088)"))
	}
	for id, attrs := range t.Overrides {
		for attr, v := range attrs {
			if at := findRef(v, ptr("/spec/overrides", id, attr)); at != "" {
				errs = append(errs, errf(at, "an override cannot hold a secret reference (D-088)"))
			}
		}
	}
	u, _ := s.targetUnit(name)
	return append(errs, s.checkUses(u)...)
}

// lxcKeyLine and maxLXCKeyBytes: the description lines of the SSH keys
// of a container (proxmox driver, D-249). Proxmox allows 8192 bytes; the
// rest holds the image marker, the version and the footer.
const (
	lxcKeyLine     = "plumb-ssh-key: "
	maxLXCKeyBytes = 7168
)

// isControl is a control character of ASCII (with tab and NUL).
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// minLXCDisk is the smallest root file system of an LXC Target in GiB
// (a fresh Ubuntu root fs uses about 0.7 GB, T-038 host E1).
const minLXCDisk = 2

// checkImageSpec checks the rules of an Image that depend on its type; the
// schema checks the shape only, so that each rule has a clear text. vm: an
// https:// url and a sha256 pin. lxc: an http(s) url and a sha512 pin from
// the signed Proxmox index (D-224); a build guest of at least 2 GiB
// without a user (T-082).
func checkImageSpec(name string, img imageSpec) Errors {
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: KindImage, Name: name, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	if img.Type == string(wire.KindLXC) {
		if img.SHA256 != "" {
			errs = append(errs, errf("/spec/sha256", "an LXC Image pins sha512, not sha256 (D-224)"))
		}
		if img.SHA512 == "" {
			errs = append(errs, errf("/spec", "an LXC Image needs sha512: the sha512sum of the template in the signed Proxmox index (D-224)"))
		}
		if img.Build != nil {
			if have, ok := intOf(img.Build.Guest["disk"]); ok && have < minLXCDisk {
				errs = append(errs, errf("/spec/build/guest/disk", "the root file system of an LXC build needs at least %d GiB", minLXCDisk))
			}
			if _, ok := img.Build.Guest["user"]; ok {
				errs = append(errs, errf("/spec/build/guest/user", "an LXC build has no bootstrap user (D-226)"))
			}
		}
		return errs
	}
	if !strings.HasPrefix(img.URL, "https://") {
		errs = append(errs, errf("/spec/url", "a VM Image needs an https:// url"))
	}
	if img.SHA512 != "" {
		errs = append(errs, errf("/spec/sha512", "a VM Image pins sha256, not sha512"))
	}
	if img.SHA256 == "" {
		errs = append(errs, errf("/spec", "a VM Image needs sha256"))
	}
	return errs
}

// checkUses checks the Block uses of a unit: ids, Blocks, parameters and
// secret references. The Blocks of an Image take no secret at all (D-202).
func (s *Set) checkUses(u *unit) Errors {
	errf := func(path, format string, args ...any) *Error {
		return &Error{Kind: u.kind, Name: u.name, Path: path, Msg: fmt.Sprintf(format, args...)}
	}
	var errs Errors
	ids := map[string]int{}
	for i, use := range u.blocks {
		path := ptr(u.blocksPath, i)
		if first, dup := ids[use.id()]; dup {
			errs = append(errs, errf(path+"/id",
				"Block use id %q is already used by %s/%d; give each use of a Block its own id (D-048)", use.id(), u.blocksPath, first))
		} else {
			ids[use.id()] = i
		}
		b, ok := s.blocks[use.Use]
		if !ok {
			errs = append(errs, errf(path+"/use", "Block %q %s", use.Use, s.missing(KindBlock, use.Use)))
			continue
		}
		if u.kind == KindImage && len(b.secrets) > 0 {
			// Also a secret parameter without a value: the Block is made
			// for secrets, and a later Target-only change must not reach
			// a template (D-202, n3).
			errs = append(errs, errf(path+"/use", "Block %q has the secret parameter %q; the Blocks of an Image cannot use secrets (D-202)",
				use.Use, sortedKeys(b.secrets)[0]))
			continue
		}
		var unknown []string
		for p := range use.Params {
			if !b.props[p] {
				unknown = append(unknown, p)
			}
		}
		refErrs := len(errs)
		for _, p := range sortedKeys(use.Params) {
			name, isRef := refName(use.Params[p])
			ppath := ptr(path+"/params", p)
			switch {
			case b.secrets[p] && !isRef:
				errs = append(errs, errf(ppath, "parameter %q of Block %q is a secret parameter: its value must be {secret: <name>}, never a literal", p, use.Use))
			case b.secrets[p] && name == "":
				errs = append(errs, errf(ppath, "a secret reference is {secret: <name>} with a secret name and nothing else"))
			case !b.secrets[p] && isRef:
				errs = append(errs, errf(ppath, "parameter %q of Block %q takes no secret reference; only a parameter with type string and writeOnly: true does", p, use.Use))
			case !b.secrets[p]:
				// No reference inside a list or object either (D-088, D-139).
				if at := findRef(use.Params[p], ppath); at != "" {
					errs = append(errs, errf(at, "a secret reference must be the whole value of a secret parameter (type string, writeOnly: true); it cannot be inside a list or an object"))
				}
			}
		}
		sort.Strings(unknown)
		for _, p := range unknown {
			errs = append(errs, errf(ptr(path+"/params", p), "Block %q has no parameter %q", use.Use, p))
		}
		if len(unknown) > 0 || b.params == nil || len(errs) > refErrs {
			continue // one error per mistake: no second schema error for it
		}
		params := withDefaults(use.Params, b.defaults)
		if err := b.params.Validate(params); err != nil {
			errs = append(errs, schemaErrors(u.kind, u.name, path+"/params", err)...)
		}
	}
	return errs
}

// intOf reads a whole number from a decoded document value.
func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case float64:
		return int(n), n == float64(int(n))
	case int:
		return n, true
	}
	return 0, false
}

// missing explains why a referenced document is not usable.
func (s *Set) missing(kind, name string) string {
	if s.invalid[[2]string{kind, name}] {
		return "is invalid (see its own errors)"
	}
	return "does not exist"
}
