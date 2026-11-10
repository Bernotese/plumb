package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Export file (D-094): one YAML stream. The first document is the header
// (kind Export), then every document of the revision sorted by kind and
// name, then optionally one SecretBundle. The same revision gives the same
// bytes, except exported_at.

// ExportFormat is the only known format.
const ExportFormat = 1

// Export file kinds besides the document kinds.
const (
	KindExport       = "Export"
	KindSecretBundle = "SecretBundle"
)

// MaxExportBytes is the largest export file that Decode reads (D-095).
const MaxExportBytes = 8 << 20

// ExportHeader is the first document of an export file.
type ExportHeader struct {
	Format     int
	Revision   int64
	ExportedAt time.Time
	Version    string   // Plumb version
	Secrets    []string // names of all secrets, sorted
}

// Encode writes an export file. docs must be sorted by kind and name (as
// Set.Docs and store.RevisionDocs give them). bundle, when not nil, is
// the last document; it must marshal to a YAML mapping with kind
// SecretBundle.
func Encode(w io.Writer, h ExportHeader, docs []Doc, bundle any) error {
	secrets := h.Secrets
	if secrets == nil {
		secrets = []string{}
	}
	head := mapping(
		"kind", str(KindExport),
		"format", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(h.Format)},
		"revision", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(h.Revision)},
		"exported_at", str(h.ExportedAt.UTC().Format(time.RFC3339)),
		"version", str(h.Version),
		"secrets", flowStrings(secrets),
	)
	nodes := []*yaml.Node{head}
	for _, d := range docs {
		var spec any
		if err := decode(d.Spec, &spec); err != nil {
			return fmt.Errorf("state: export %s %q: %w", d.Kind, d.Name, err)
		}
		nodes = append(nodes, mapping("kind", str(d.Kind), "name", str(d.Name), "spec", toNode(spec)))
	}
	if bundle != nil {
		var n yaml.Node
		if err := n.Encode(bundle); err != nil {
			return err
		}
		nodes = append(nodes, &n)
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	for _, n := range nodes {
		if err := enc.Encode(n); err != nil {
			return err
		}
	}
	return enc.Close()
}

func str(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	// A multi-line string is a literal block, so a file content reads as
	// it is. Only when the block keeps every byte (Decode checks the
	// round trip in tests).
	if strings.Contains(s, "\n") && literalSafe(s) {
		n.Style = yaml.LiteralStyle
	}
	return n
}

var unsafeLiteral = regexp.MustCompile(`[ \t]\n|[\r\t]|^[ \t\n]|[^\x20-\x7e\n\x{a0}-\x{d7ff}\x{e000}-\x{fffd}\x{10000}-\x{10ffff}]`)

func literalSafe(s string) bool { return !unsafeLiteral.MatchString(s) }

func mapping(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(kv); i += 2 {
		n.Content = append(n.Content, str(kv[i].(string)), kv[i+1].(*yaml.Node))
	}
	return n
}

func flowStrings(list []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range list {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
	}
	return n
}

// toNode converts a decoded JSON value (numbers as json.Number) to YAML.
// Keys are sorted, so the output is stable.
func toNode(v any) *yaml.Node {
	switch v := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(string(v), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(v)}
	case string:
		return str(v)
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range v {
			n.Content = append(n.Content, toNode(e))
		}
		return n
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range keys {
			n.Content = append(n.Content, str(k), toNode(v[k]))
		}
		return n
	}
	panic(fmt.Sprintf("state: export: unexpected value %T", v))
}

// --- decode ---

// ExportFile is a decoded export file.
type ExportFile struct {
	Header ExportHeader
	Docs   []Doc
	// Bundle is the SecretBundle document as JSON, or nil. Package secret
	// reads it.
	Bundle json.RawMessage
}

// ExportError is an error of one document of an export file. Doc is the
// 0-based index in the stream (0 is the header), Path a JSON pointer
// inside it.
type ExportError struct {
	Doc  int
	Path string
	Msg  string
}

func (e *ExportError) Error() string {
	return fmt.Sprintf("export file: document %d%s: %s", e.Doc, e.Path, e.Msg)
}

// ErrExportFormat is wrapped by every ExportError.
var ErrExportFormat = errors.New("export file")

func (e *ExportError) Unwrap() error { return ErrExportFormat }

// Decode reads an export file. It refuses a file above MaxExportBytes,
// anchors and aliases, merge keys, custom tags, an unknown format and a
// document that is not {kind, name, spec}. The documents are not
// validated here; Validate does that.
func Decode(raw []byte) (*ExportFile, error) {
	if len(raw) > MaxExportBytes {
		return nil, &ExportError{Msg: fmt.Sprintf("the file is larger than %d bytes", MaxExportBytes)}
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var nodes []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &ExportError{Doc: len(nodes), Msg: "not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
		}
		nodes = append(nodes, &n)
	}
	if len(nodes) == 0 {
		return nil, &ExportError{Msg: "the file is empty"}
	}
	f := &ExportFile{}
	seen := map[[2]string]bool{}
	for i, n := range nodes {
		v, err := fromNode(n, "")
		if err != nil {
			err.Doc = i
			return nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, &ExportError{Doc: i, Msg: "a document must be a mapping"}
		}
		kind, _ := m["kind"].(string)
		switch {
		case i == 0:
			if kind != KindExport {
				return nil, &ExportError{Doc: 0, Path: "/kind", Msg: "the first document must be the header (kind: Export)"}
			}
			h, err := header(m)
			if err != nil {
				return nil, err
			}
			f.Header = h
		case kind == KindSecretBundle:
			if i != len(nodes)-1 {
				return nil, &ExportError{Doc: i, Path: "/kind", Msg: "the SecretBundle must be the last document"}
			}
			f.Bundle, _ = json.Marshal(m)
		default:
			name, _ := m["name"].(string)
			for k := range m {
				if k != "kind" && k != "name" && k != "spec" {
					return nil, &ExportError{Doc: i, Path: "/" + k, Msg: "a document has only kind, name and spec"}
				}
			}
			if kind == "" || name == "" || m["spec"] == nil {
				return nil, &ExportError{Doc: i, Msg: "a document needs kind, name and spec"}
			}
			if seen[[2]string{kind, name}] {
				return nil, &ExportError{Doc: i, Msg: fmt.Sprintf("%s %q is twice in the file", kind, name)}
			}
			seen[[2]string{kind, name}] = true
			spec, _ := json.Marshal(m["spec"])
			f.Docs = append(f.Docs, Doc{Kind: kind, Name: name, Spec: spec})
		}
	}
	return f, nil
}

func header(m map[string]any) (ExportHeader, error) {
	var h ExportHeader
	if format, _ := m["format"].(json.Number); string(format) != fmt.Sprint(ExportFormat) {
		return h, &ExportError{Path: "/format", Msg: fmt.Sprintf("unknown format %v; this Plumb reads format %d", m["format"], ExportFormat)}
	}
	h.Format = ExportFormat
	if rev, ok := m["revision"].(json.Number); ok {
		h.Revision, _ = rev.Int64()
	}
	if at, ok := m["exported_at"].(string); ok {
		h.ExportedAt, _ = time.Parse(time.RFC3339, at)
	}
	h.Version, _ = m["version"].(string)
	if list, ok := m["secrets"].([]any); ok {
		for _, s := range list {
			if name, ok := s.(string); ok {
				h.Secrets = append(h.Secrets, name)
			}
		}
	}
	return h, nil
}

// fromNode converts a YAML node to a JSON value. Numbers become
// json.Number with their literal, so no value goes through float64.
func fromNode(n *yaml.Node, path string) (any, *ExportError) {
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return nil, &ExportError{Path: path, Msg: "YAML anchors and aliases are not allowed"}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, &ExportError{Path: path, Msg: "empty document"}
		}
		return fromNode(n.Content[0], path)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || (k.Tag != "!!str" && k.ShortTag() != "!!str") {
				if k.ShortTag() == "!!merge" {
					return nil, &ExportError{Path: path, Msg: "YAML merge keys are not allowed"}
				}
				return nil, &ExportError{Path: path, Msg: "a key must be a string"}
			}
			if _, dup := m[k.Value]; dup {
				return nil, &ExportError{Path: ptr(path, k.Value), Msg: "the key is twice in the mapping"}
			}
			v, err := fromNode(n.Content[i+1], ptr(path, k.Value))
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, len(n.Content))
		for i, c := range n.Content {
			v, err := fromNode(c, ptr(path, i))
			if err != nil {
				return nil, err
			}
			s[i] = v
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!str", "!!timestamp", "!!binary":
			return n.Value, nil // the text as written
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, &ExportError{Path: path, Msg: "not a boolean"}
			}
			return b, nil
		case "!!int", "!!float":
			num := json.Number(strings.ReplaceAll(n.Value, "_", ""))
			if !json.Valid([]byte(num)) {
				return nil, &ExportError{Path: path, Msg: fmt.Sprintf("%q is not a JSON number", n.Value)}
			}
			return num, nil
		}
		return nil, &ExportError{Path: path, Msg: fmt.Sprintf("the YAML tag %s is not allowed", n.Tag)}
	}
	return nil, &ExportError{Path: path, Msg: "unknown YAML element"}
}
