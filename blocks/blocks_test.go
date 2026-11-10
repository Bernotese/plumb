package blocks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read schema %s: %v", path, err)
	}
	const url = "mem:///schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		t.Fatalf("compile schema %s: %v", path, err)
	}
	return schema
}

func validate(t *testing.T, schema *jsonschema.Schema, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatal(err)
	}
}

func materializeTemplates(value any) any {
	switch value := value.(type) {
	case string:
		if strings.Contains(value, "{{ .name }}") {
			return strings.ReplaceAll(value, "{{ .name }}", "deploy")
		}
		if strings.Contains(value, "{{ .ssh_key }}") {
			return strings.ReplaceAll(value, "{{ .ssh_key }}", "ssh-ed25519 AAAA")
		}
		if strings.Contains(value, "{{") {
			return strings.ReplaceAll(strings.ReplaceAll(value, "{{ .page_title }}", "Welcome"), "{{ .message }}", "Managed by Plumb.")
		}
	case []any:
		for i := range value {
			value[i] = materializeTemplates(value[i])
		}
	case map[string]any:
		for key, child := range value {
			value[key] = materializeTemplates(child)
		}
	}
	return value
}

func TestBlocks(t *testing.T) {
	blockSchema := compileSchema(t, "../internal/state/schema/block.json")
	resourceSchemas := map[string]*jsonschema.Schema{}
	for _, path := range []string{"../internal/resource/schema/directory.json", "../internal/resource/schema/file.json", "../internal/resource/schema/package.json", "../internal/resource/schema/service.json", "../internal/resource/schema/user.json"} {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		resourceSchemas[name] = compileSchema(t, path)
	}
	paths, err := filepath.Glob("*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no Block YAML files found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(raw, &document); err != nil {
				t.Fatalf("parse Block YAML: %v", err)
			}
			validate(t, blockSchema, document)
			spec, ok := document["spec"].(map[string]any)
			if !ok {
				t.Fatal("spec is not an object")
			}
			resources, ok := spec["resources"].([]any)
			if !ok {
				t.Fatal("resources is not an array")
			}
			for _, entry := range resources {
				resource, ok := entry.(map[string]any)
				if !ok || len(resource) != 1 {
					t.Fatalf("resource must have one type: %#v", entry)
				}
				for kind, attributes := range resource {
					schema, ok := resourceSchemas[kind]
					if !ok {
						t.Fatalf("unsupported resource type %q", kind)
					}
					validate(t, schema, materializeTemplates(attributes))
				}
			}
		})
	}
}
