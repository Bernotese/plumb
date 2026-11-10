// Package state holds the desired state: documents, their validation, the
// rendering of Blocks into resources, revisions and their diff
// (docs/ARCHITECTURE.md §3). This file has the document contract and the
// canonical form; validate.go, render.go, template.go and commit.go have
// the behaviour.
package state

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Document kinds.
const (
	KindImage  = "Image"
	KindBlock  = "Block"
	KindTarget = "Target"
	KindRule   = "Rule" // drift rule (D-092)
)

// Kinds lists all document kinds.
var Kinds = []string{KindImage, KindBlock, KindTarget, KindRule}

// Doc is one document of the state. Kind and Name identify it.
// Spec is validated against the schema of the kind.
type Doc struct {
	Kind string          `json:"kind" yaml:"kind"`
	Name string          `json:"name" yaml:"name"`
	Spec json.RawMessage `json:"spec" yaml:"spec"`
}

//go:embed schema/*.json
var schemas embed.FS

// schemaFiles maps a document kind to its schema file.
var schemaFiles = map[string]string{
	KindImage:  "schema/image.json",
	KindBlock:  "schema/block.json",
	KindTarget: "schema/target.json",
	KindRule:   "schema/rule.json",
}

// Schema returns the JSON Schema (2020-12) of a whole document of the kind.
// ok is false for an unknown kind.
func Schema(kind string) (schema []byte, ok bool) {
	file, ok := schemaFiles[kind]
	if !ok {
		return nil, false
	}
	b, err := schemas.ReadFile(file)
	if err != nil {
		panic("state: no schema file for kind " + kind) // build mistake; the tests catch it
	}
	return b, true
}

// Canonical returns the canonical JSON body of a document. The store keeps
// this body, and its hash identifies the document for ever, so the form
// must never change:
//
//   - one object with the members "kind", "name" and "spec"
//   - object members sorted by key (byte order), at every level
//   - no whitespace between tokens, no trailing newline
//   - strings as Go's encoding/json writes them, without HTML escaping
//   - numbers in one normal form (see canonNumber): 2048, 2048.0 and
//     2.048e3 are the same number and give the same body, so a change of
//     number spelling is never a document change
func Canonical(d Doc) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(d.Spec))
	dec.UseNumber() // keep number literals; never go through float64
	var spec any
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("state: spec of %s %q is not JSON: %w", d.Kind, d.Name, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("state: spec of %s %q has data after the JSON value", d.Kind, d.Name)
	}
	spec, err := canonValue(spec)
	if err != nil {
		return nil, fmt.Errorf("state: spec of %s %q: %w", d.Kind, d.Name, err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json sorts the keys of a map.
	if err := enc.Encode(map[string]any{"kind": d.Kind, "name": d.Name, "spec": spec}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Hash returns the document hash of a canonical body: SHA-256, lower-case hex.
func Hash(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// CheckCanonical reports an error when body is not the canonical form of
// the document it holds. The store keeps only canonical bodies; this check
// guards every body that comes back from it.
func CheckCanonical(body []byte) (Doc, error) {
	var d Doc
	if err := json.Unmarshal(body, &d); err != nil {
		return Doc{}, fmt.Errorf("state: stored body is not a document: %w", err)
	}
	again, err := Canonical(d)
	if err != nil {
		return Doc{}, err
	}
	if !bytes.Equal(again, body) {
		return Doc{}, fmt.Errorf("state: stored body of %s %q is not canonical", d.Kind, d.Name)
	}
	return d, nil
}

// canonValue replaces every number in a decoded JSON value by its
// canonical text.
func canonValue(v any) (any, error) {
	switch v := v.(type) {
	case json.Number:
		n, err := canonNumber(string(v))
		return json.Number(n), err
	case map[string]any:
		for k, e := range v {
			c, err := canonValue(e)
			if err != nil {
				return nil, err
			}
			v[k] = c
		}
	case []any:
		for i, e := range v {
			c, err := canonValue(e)
			if err != nil {
				return nil, err
			}
			v[i] = c
		}
	}
	return v, nil
}

// Limits of number literals in documents. They keep big.Rat work small.
const (
	maxNumberText     = 100 // characters
	maxNumberExponent = 100 // |exponent| as written in the literal
)

// canonNumber returns the canonical text of a JSON number literal:
//
//   - the exact value, never rounded through float64
//   - an integer value as plain digits: "2048.0", "2.048e3" → "2048"
//   - any other value as a plain decimal with no trailing zeros and no
//     exponent: "0.50" → "0.5", "1e-2" → "0.01"
//   - "-0" → "0"
func canonNumber(s string) (string, error) {
	if len(s) > maxNumberText {
		return "", fmt.Errorf("number %.20s… is longer than %d characters", s, maxNumberText)
	}
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return "", fmt.Errorf("number %s: bad exponent", s)
		}
		exp = e
	}
	// Digits after the point, after the exponent is applied.
	frac := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		frac = len(mant) - i - 1
	}
	frac -= exp
	if exp > maxNumberExponent || exp < -maxNumberExponent {
		return "", fmt.Errorf("number %s is out of range", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", fmt.Errorf("number %s is not valid", s)
	}
	if r.IsInt() {
		return r.Num().String(), nil
	}
	out := r.FloatString(frac) // exact: the value has at most frac decimals
	out = strings.TrimRight(out, "0")
	return out, nil
}
