package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func exportDocs(t *testing.T) []Doc {
	t.Helper()
	block, _ := json.Marshal(map[string]any{"title": "Message of the day",
		"params": map[string]any{"type": "object", "properties": map[string]any{
			"text": map[string]any{"type": "string", "default": "Managed by Plumb."},
			"pw":   map[string]any{"type": "string", "writeOnly": true}}},
		"resources": []any{map[string]any{"file": map[string]any{"path": "/etc/motd", "content": "{{ .text }}\n-- managed \nline 3\n", "mode": "0644"}}}})
	target, _ := json.Marshal(map[string]any{"type": "vm", "image": "ubuntu",
		"guest":  map[string]any{"cores": 2, "memory": 2048, "disk": 20.5e0, "bridge": "vmbr0"},
		"blocks": []any{map[string]any{"use": "motd", "params": map[string]any{"text": "yes", "pw": map[string]any{"secret": "app-db"}}}}})
	docs := []Doc{
		{Kind: KindBlock, Name: "motd", Spec: block},
		{Kind: KindImage, Name: "ubuntu", Spec: json.RawMessage(secImage)},
		{Kind: KindTarget, Name: "web1", Spec: target},
	}
	set, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	return set.Docs()
}

var exportTime = time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

func encode(t *testing.T, h ExportHeader, docs []Doc, bundle any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, h, docs, bundle); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExportGolden(t *testing.T) {
	h := ExportHeader{Format: ExportFormat, Revision: 7, ExportedAt: exportTime, Version: "0.2.0", Secrets: []string{"app-db"}}
	got := encode(t, h, exportDocs(t), nil)
	const golden = "testdata/export/basic.yaml"
	if *update {
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("export differs from %s:\n%s", golden, got)
	}
	// Stable: the same input gives the same bytes; only exported_at moves.
	h2 := h
	h2.ExportedAt = exportTime.Add(time.Hour)
	other := encode(t, h2, exportDocs(t), nil)
	if bytes.Equal(got, other) || !bytes.Equal(bytes.Replace(other, []byte("10:30:00Z"), []byte("09:30:00Z"), 1), got) {
		t.Fatal("output depends on more than exported_at")
	}
}

// Decode(Encode(docs)) gives the same canonical documents, also for
// strings that a literal block cannot hold.
func TestExportRoundTrip(t *testing.T) {
	docs := exportDocs(t)
	tricky := []string{"a NLb", "trailing space NLx", "tabTABinNL", " leadingNLx", "crCRNLlf", "endsNLNL", "NLstarts", "ümlaut ✓NLok", "yes", "123", "null", "~", "- dash", "key: value", "#hash", "'quote", "\"dq\"", ""}
	for i := range tricky {
		tricky[i] = strings.NewReplacer("NL", "\n", "TAB", "\t", "CR", "\r").Replace(tricky[i])
	}
	spec, _ := json.Marshal(map[string]any{"type": "vm", "url": "https://images.invalid/u.img", "sha256": strings.Repeat("0", 64),
		"title": strings.Join(tricky, "|"), "description": tricky[0] + tricky[1]})
	var raw map[string]any
	json.Unmarshal(spec, &raw)
	for i, s := range tricky {
		raw["x"+string(rune('a'+i))] = s
	}
	h := ExportHeader{Format: ExportFormat, Revision: 1, ExportedAt: exportTime}
	all := append(docs, Doc{Kind: "Odd", Name: "x", Spec: exportJSON(raw)})
	out := encode(t, h, all, map[string]any{"kind": KindSecretBundle, "data": "abc"})
	f, err := Decode(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.Docs) != len(all) || f.Header.Revision != 1 || f.Header.Format != 1 || string(f.Bundle) != `{"data":"abc","kind":"SecretBundle"}` {
		t.Fatalf("decoded %+v", f)
	}
	for i := range all {
		a, _ := Canonical(all[i])
		b, _ := Canonical(f.Docs[i])
		if !bytes.Equal(a, b) {
			t.Errorf("document %d:\n%s\n%s", i, a, b)
		}
	}
}

func exportJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestExportDecodeRefuses(t *testing.T) {
	head := "kind: Export\nformat: 1\nrevision: 1\nexported_at: \"2026-11-10T09:30:00Z\"\nversion: x\nsecrets: []\n"
	doc := "---\nkind: Image\nname: a\nspec: {type: vm}\n"
	bomb := "---\nkind: Image\nname: a\nspec:\n  a: &a [x, x, x, x, x, x, x, x, x]\n  b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a]\n  c: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b]\n  d: [*c, *c, *c, *c, *c, *c, *c, *c, *c]\n"
	for name, text := range map[string]string{
		"empty":          "",
		"no header":      "kind: Image\nname: a\nspec: {}\n",
		"format 2":       strings.Replace(head, "format: 1", "format: 2", 1) + doc,
		"no format":      strings.Replace(head, "format: 1\n", "", 1) + doc,
		"anchor":         head + "---\nkind: Image\nname: a\nspec: &s {type: vm}\n",
		"alias bomb":     head + bomb,
		"merge key":      head + "---\nkind: Image\nname: a\nspec:\n  <<: {type: vm}\n",
		"custom tag":     head + "---\nkind: Image\nname: a\nspec: !thing {type: vm}\n",
		"extra field":    head + "---\nkind: Image\nname: a\nspec: {}\nmore: 1\n",
		"no spec":        head + "---\nkind: Image\nname: a\n",
		"twice":          head + doc + doc,
		"duplicate key":  head + "---\nkind: Image\nname: a\nspec: {a: 1, a: 2}\n",
		"bundle first":   head + "---\nkind: SecretBundle\ndata: x\n" + doc,
		"not yaml":       head + "---\nkind: [\n",
		"inf":            head + "---\nkind: Image\nname: a\nspec: {a: .inf}\n",
		"non-string key": head + "---\nkind: Image\nname: a\nspec: {1: x}\n",
	} {
		_, err := Decode([]byte(text))
		var ee *ExportError
		if !errors.As(err, &ee) {
			t.Errorf("%s: %v", name, err)
		}
	}
	big := make([]byte, MaxExportBytes+1)
	if _, err := Decode(big); !errors.Is(err, ErrExportFormat) {
		t.Errorf("9 MiB: %v", err)
	}
	if f, err := Decode([]byte(head + doc)); err != nil || len(f.Docs) != 1 || f.Bundle != nil {
		t.Errorf("valid file: %+v %v", f, err)
	}
}
