package state

import (
	"bytes"
	"testing"
)

func TestSetDocsSnapshotIsolation(t *testing.T) {
	set, err := Validate(fixture(target(`"blocks":[{"use":"motd"}]`)))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range set.Docs() {
		if d.Kind != KindBlock || d.Name != "motd" {
			continue
		}
		// A caller can edit its snapshot before proposing a new revision.
		before := bytes.Clone(d.Spec)
		edited := bytes.ReplaceAll(d.Spec, []byte(`"file"`), []byte(`"evil"`))
		copy(d.Spec, edited)
		for _, original := range set.Docs() {
			if original.Kind == d.Kind && original.Name == d.Name && !bytes.Equal(original.Spec, before) {
				t.Fatal("editing Docs() changed the validated Set; Commit can persist an unvalidated resource")
			}
		}
		return
	}
	t.Fatal("fixture has no motd Block")
}
