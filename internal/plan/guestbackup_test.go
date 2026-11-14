package plan

import (
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

func TestBackupTargetOf(t *testing.T) {
	b := store.Binding{Kind: state.KindTarget, Name: "db1", Ref: "9001", Gen: 7}
	got, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1",
		Spec: []byte(`{"type":"lxc","guest":{},"backup":{"schedule":"weekly","weekday":"sun","time":"03:30","keep":4}}`)}, b)
	if err != nil || !got.LXC || got.Gen != 7 || got.Ref != "9001" || got.Backup == nil ||
		got.Backup.Hour != 3 || got.Backup.Minute != 30 || got.Backup.Weekday != "sun" || got.Backup.Keep != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	none, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1", Spec: []byte(`{"type":"vm","guest":{}}`)}, b)
	if err != nil || none.Backup != nil || none.LXC {
		t.Fatalf("%+v %v", none, err)
	}
	if _, err := backupTargetOf(state.Doc{Kind: state.KindTarget, Name: "db1", Spec: []byte(`{"type":"vm","backup":{"time":"3"}}`)}, b); err == nil {
		t.Fatal("a bad time passed")
	}
}
