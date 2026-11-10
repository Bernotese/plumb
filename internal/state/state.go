// Package state holds the desired state: documents, their validation, the
// rendering of Blocks into resources, revisions and their diff
// (docs/ARCHITECTURE.md §3). This file has only the document contract;
// the behaviour comes with later cards.
package state

import (
	"embed"
	"encoding/json"
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
