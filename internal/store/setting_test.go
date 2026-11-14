package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSettingsPutAndRead(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	got, err := s.Settings(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("new store: %v, %v; want no setting", got, err)
	}
	if _, err := s.PutSettings(ctx, "user:admin", map[string]json.RawMessage{
		SettingBackupTime: json.RawMessage(`"02:30"`),
		SettingBackupKeep: json.RawMessage(`10`),
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Settings(ctx)
	if v := got[SettingBackupTime]; !v.Set() || string(v.Value) != `"02:30"` || v.ChangedBy != "user:admin" || v.ChangedAt.IsZero() {
		t.Errorf("time = %+v", v)
	}
	// nil removes the value, the row stays with the author.
	if _, err := s.PutSettings(ctx, "user:bob", map[string]json.RawMessage{SettingBackupKeep: nil}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Settings(ctx)
	if v := got[SettingBackupKeep]; v.Set() || v.ChangedBy != "user:bob" {
		t.Errorf("keep after null = %+v", v)
	}
	if string(got[SettingBackupTime].Value) != `"02:30"` {
		t.Errorf("time changed by a keep write: %s", got[SettingBackupTime].Value)
	}
}

// The table takes only known keys and valid JSON; a failed key rolls the
// whole write back.
func TestSettingsRefuseUnknownKey(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.PutSettings(ctx, "user:admin", map[string]json.RawMessage{
		SettingBackupTime: json.RawMessage(`"01:00"`),
		"proxmox.secret":  json.RawMessage(`"x"`),
	}); err == nil {
		t.Fatal("unknown key was stored")
	}
	if _, err := s.PutSettings(ctx, "user:admin", map[string]json.RawMessage{SettingBackupTime: json.RawMessage(`{`)}); err == nil {
		t.Fatal("bad JSON was stored")
	}
	got, _ := s.Settings(ctx)
	if len(got) != 0 {
		t.Errorf("rolled-back write left rows: %v", got)
	}
}
