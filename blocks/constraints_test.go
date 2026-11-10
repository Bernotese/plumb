package blocks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
				title, ok := param["title"].(string)
				if param["type"] != "string" || !ok || title == "" {
					t.Errorf("%s needs a string type and title", name)
				}
				// A secret parameter (writeOnly) has no default by rule (D-088).
				if name != "ssh_key" && param["writeOnly"] != true && param["default"] == nil {
					t.Errorf("%s needs a default", name)
				}
			}
			if err := plainValues(spec["resources"], params); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParametersRejectUnsafeValues(t *testing.T) {
	for _, tc := range []struct {
		block, param string
		invalid      []any
	}{
		{"deploy-user", "name", []any{"root", "-option", "Bad Name", 123}},
		{"deploy-user", "ssh_key", []any{"invalid", "ssh-ed25519 AAAA\ninjected"}},
		{"nginx", "server_name", []any{"_; location /x { alias /etc/; }", "ok\ninjected", "", strings.Repeat("a", 254)}},
		{"motd", "message", []any{strings.Repeat("a", 2001)}},
		{"nginx", "default_site", []any{"yes", "", "present\n"}},
		{"nginx-site", "name", []any{"default", "../x", "a/b", ".hidden", "-x", "A", strings.Repeat("a", 65)}},
		{"nginx-site", "server_name", []any{"_; return 200;", "ok\ninjected", ""}},
		{"nginx-site", "root", []any{"relative", "/var/www/../../etc", "/var/www/./x", "/a b", "/x;", "/x\ny"}},
		{"nginx-site", "state", []any{"enabled"}},
	} {
		t.Run(tc.block+"/"+tc.param, func(t *testing.T) {
			raw, err := os.ReadFile(tc.block + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			params := doc["spec"].(map[string]any)["params"].(map[string]any)
			schema := compileSchemaValue(t, params["properties"].(map[string]any)[tc.param])
			for _, invalid := range tc.invalid {
				if err := schema.Validate(invalid); err == nil {
					t.Errorf("accepted unsafe %s=%q", tc.param, invalid)
				}
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
