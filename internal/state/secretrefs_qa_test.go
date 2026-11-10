package state

import "testing"

func TestQASecretNestedReferences(t *testing.T) {
	for name, opts := range map[string]any{
		"nested": map[string]any{"password": ref("app-db")},
		"list":   map[string]any{"passwords": []any{ref("app-db")}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Validate(secDocs("{{ .db_password }}", map[string]any{"db_password": ref("app-db"), "opts": opts}))
			if err == nil {
				t.Fatal("reference inside a non-secret parameter accepted")
			}
		})
	}
}

func TestQASecretCutJoinForgery(t *testing.T) {
	for _, tmpl := range []string{
		`{{ trimPrefix "x" ("x` + bs + `uE000app-db` + bs + `uE001") }}`,
		`{{ replace "START" "` + bs + `uE000" "STARTapp-dbEND" | replace "END" "` + bs + `uE001" }}`,
	} {
		if _, err := renderOne(t, tmpl); err == nil {
			t.Fatal("cut/join forged marker accepted")
		}
	}
}
