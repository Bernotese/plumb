// Package state holds the desired state: documents, their validation, the
// rendering of Blocks into resources, revisions and their diff
// (docs/ARCHITECTURE.md §3). This file has only the document contract;
// the behaviour comes with later cards.
package state

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Document kinds.
const (
	KindImage  = "Image"
	KindBlock  = "Block"
	KindTarget = "Target"
)

// Kinds lists all document kinds.
var Kinds = []string{KindImage, KindBlock, KindTarget}

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
//   - numbers exactly as they were written in the input
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
