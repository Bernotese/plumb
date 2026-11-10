package blocks

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"text/template"
	"text/template/parse"

	"go.yaml.in/yaml/v3"
)

func plainValues(value any, params map[string]any) error {
	switch value := value.(type) {
	case string:
		tmpl, err := template.New("value").Parse(value)
		if err != nil {
			return err
		}
		for _, node := range tmpl.Tree.Root.Nodes {
			if _, ok := node.(*parse.TextNode); ok {
				continue
			}
			action, ok := node.(*parse.ActionNode)
			if !ok || len(action.Pipe.Decl) != 0 || len(action.Pipe.Cmds) != 1 || len(action.Pipe.Cmds[0].Args) != 1 {
				return fmt.Errorf("only plain parameter values allowed: %s", node)
			}
			field, ok := action.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(field.Ident) != 1 || params[field.Ident[0]] == nil {
				return fmt.Errorf("unknown or non-plain parameter: %s", node)
			}
		}
	case []any:
		for _, child := range value {
			if err := plainValues(child, params); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, child := range value {
			if err := plainValues(child, params); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestStarterParameterMetadataAndTemplates(t *testing.T) {
	paths, err := filepath.Glob("*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			spec := doc["spec"].(map[string]any)
			params := spec["params"].(map[string]any)["properties"].(map[string]any)
			for name, value := range params {
				param := value.(map[string]any)
				if param["type"] != "string" || param["title"] == nil || param["title"] == "" {
					t.Errorf("%s needs a string type and title", name)
				}
				if name != "ssh_key" && param["default"] == nil {
					t.Errorf("%s needs a default", name)
				}
			}
			if err := plainValues(spec["resources"], params); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlainValuesRejectsNonPlainTemplates(t *testing.T) {
	params := map[string]any{"name": true}
	for _, value := range []string{"{{if .name}}yes{{end}}", "{{range .name}}{{.}}{{end}}", "{{template \"other\" .}}", "{{.name | printf \"%s\"}}", "{{.missing}}", "{{.name.field}}", "{{"} {
		if err := plainValues(value, params); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if err := plainValues("hello {{ .name }}", params); err != nil {
		t.Fatal(err)
	}
}
