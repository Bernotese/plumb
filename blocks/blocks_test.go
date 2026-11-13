package blocks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/Bernotese/plumb/internal/resource"
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
	return compileSchemaValue(t, doc)
}

func compileSchemaValue(t *testing.T, doc any) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	const url = "mem:///schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
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

func materializeTemplates(t *testing.T, value any, params map[string]any) any {
	t.Helper()
	switch value := value.(type) {
	case string:
		tmpl, err := template.New("value").Option("missingkey=error").Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, params); err != nil {
			t.Fatal(err)
		}
		return out.String()
	case []any:
		for i := range value {
			value[i] = materializeTemplates(t, value[i], params)
		}
	case map[string]any:
		for key, child := range value {
			value[key] = materializeTemplates(t, child, params)
		}
	}
	return value
}

func TestBlocks(t *testing.T) {
	blockSchema := compileSchema(t, "../internal/state/schema/block.json")
	resourceSchemas := map[string]*jsonschema.Schema{}
	// This fixed list is the slice allowlist; new types require a slice decision.
	for _, path := range []string{
		"../internal/resource/schema/directory.json",
		"../internal/resource/schema/file.json",
		"../internal/resource/schema/package.json",
		"../internal/resource/schema/service.json",
		"../internal/resource/schema/symlink.json", // T-020
		"../internal/resource/schema/user.json",
		"../internal/resource/schema/apt_repository.json", // D-260, T-095
		"../internal/resource/schema/unit.json",           // D-260, T-095
		"../internal/resource/schema/sysctl.json",         // D-260, T-095
		"../internal/resource/schema/firewall.json",       // D-295, T-119
	} {
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
			paramSchema, _ := spec["params"].(map[string]any)
			if paramSchema == nil {
				paramSchema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			sample := map[string]any{}
			for name, value := range paramSchema["properties"].(map[string]any) {
				prop := value.(map[string]any)
				if def, ok := prop["default"]; ok {
					validate(t, compileSchemaValue(t, prop), def)
					sample[name] = def
				}
			}
			fixtures := map[string]map[string]any{"deploy-user": {"ssh_key": "ssh-ed25519 AAAA"}, "app-env": {"password": "x"},
				"docker-compose": {"compose": "services: {}\n", "env": "X=1\n"}}
			required, _ := paramSchema["required"].([]any)
			for _, value := range required {
				name := value.(string)
				fixture, ok := fixtures[document["name"].(string)][name]
				if !ok {
					t.Fatalf("missing required parameter fixture: %s", name)
				}
				sample[name] = fixture
			}
			validate(t, compileSchemaValue(t, paramSchema), sample)
			resources, ok := spec["resources"].([]any)
			if !ok {
				t.Fatal("resources is not an array")
			}
			ids := map[string]bool{}
			var restarts []string
			for _, entry := range resources {
				entryMap, ok := entry.(map[string]any)
				if !ok || len(entryMap) != 1 {
					t.Fatalf("resource must have one type: %#v", entry)
				}
				for kind, attributes := range entryMap {
					schema, ok := resourceSchemas[kind]
					if !ok {
						t.Fatalf("unsupported resource type %q", kind)
					}
					rendered := materializeTemplates(t, attributes, sample).(map[string]any)
					validate(t, schema, rendered)
					typ, _ := resource.Lookup(kind)
					ids[kind+":"+rendered[typ.Key].(string)] = true
					if refs, ok := rendered["restart_on"].([]any); ok {
						for _, ref := range refs {
							restarts = append(restarts, ref.(string))
						}
					}
				}
			}
			for _, ref := range restarts {
				if !ids[ref] {
					t.Errorf("restart_on references missing resource %q", ref)
				}
			}
		})
	}
}
