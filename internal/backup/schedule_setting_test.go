package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

// A stored setting wins over the flag/env value; null goes back to it
// (D-361). The source of each value follows.
func TestScheduleSettingWins(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Time, c.TimeSource, c.Keep = "04:30", SourceEnv, 7 })
	if got := f.svc.ScheduleInfo(); got != (Schedule{Time: "04:30", Keep: 7, TimeSource: SourceEnv, KeepSource: SourceDefault}) {
		t.Fatalf("start: %+v", got)
	}
	old, next, err := f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetTime: true, Time: strp("02:30"), SetKeep: true, Keep: intp(10)})
	if err != nil {
		t.Fatal(err)
	}
	if old.Time != "04:30" || next != (Schedule{Time: "02:30", Keep: 10, TimeSource: SourceSetting, KeepSource: SourceSetting}) {
		t.Fatalf("change: old %+v next %+v", old, next)
	}
	// install.json follows keep, so plumb backup now keeps the same number.
	in, err := LoadInstall(f.data)
	if err != nil || in.Keep != 10 {
		t.Errorf("install.json keep = %d, %v; want 10", in.Keep, err)
	}
	// A restart with the same env keeps the stored values.
	f2, err := New(f.ctx, Config{Store: f.st, DataDir: f.data, Time: "04:30", TimeSource: SourceEnv, Keep: 7, Serve: true, CopyStatus: f.copy, Now: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if got := f2.ScheduleInfo(); got.Time != "02:30" || got.Keep != 10 || got.TimeSource != SourceSetting {
		t.Errorf("after restart: %+v", got)
	}
	// null removes only the named field.
	_, next, err = f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if next != (Schedule{Time: "04:30", Keep: 10, TimeSource: SourceEnv, KeepSource: SourceSetting}) {
		t.Errorf("after null time: %+v", next)
	}
	// Bad values change nothing.
	for _, c := range []ScheduleChange{
		{SetTime: true, Time: strp("24:00")}, {SetTime: true, Time: strp("+1:00")},
		{SetKeep: true, Keep: intp(0)}, {SetKeep: true, Keep: intp(101)},
	} {
		if _, _, err := f.svc.ChangeSchedule(f.ctx, "user:admin", c); !errors.Is(err, ErrBadSchedule) {
			t.Errorf("%+v: err %v, want ErrBadSchedule", c, err)
		}
	}
	if got := f.svc.ScheduleInfo(); got.Time != "04:30" || got.Keep != 10 {
		t.Errorf("after bad values: %+v", got)
	}
}

// The schedule takes a new time without a restart: two minutes ahead runs
// then (host step A13.2); a time moved back before now does not run at once.
func TestScheduleChangeWithoutRestart(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Time = "03:00" })
	f.clock.set(time.Date(2026, 11, 13, 3, 5, 0, 0, time.UTC))
	f.backup(SystemActor) // today's 03:00 is done
	f.clock.set(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	if _, _, err := f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetTime: true, Time: strp("10:02")}); err != nil {
		t.Fatal(err)
	}
	f.clock.set(time.Date(2026, 11, 13, 10, 1, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("10:01: not due yet")
	}
	f.clock.set(time.Date(2026, 11, 13, 10, 2, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("10:02: due without a restart")
	}
	f.backup(SystemActor)
	// Back to 05:00 at 10:30: today's 05:00 lies before the change.
	f.clock.set(time.Date(2026, 11, 13, 10, 30, 0, 0, time.UTC))
	if _, _, err := f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetTime: true, Time: strp("05:00")}); err != nil {
		t.Fatal(err)
	}
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("time moved back: no backup at once")
	}
	f.clock.set(time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("next day 05:00: due")
	}
}

// A lower keep applies at the next retention.
func TestScheduleKeepRetention(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Keep = 5 })
	for i := 0; i < 4; i++ {
		f.clock.set(f.clock.now().Add(time.Hour))
		f.backup("user:admin")
	}
	if _, _, err := f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetKeep: true, Keep: intp(2)}); err != nil {
		t.Fatal(err)
	}
	f.clock.set(f.clock.now().Add(time.Hour))
	f.backup("user:admin")
	items, err := f.svc.Items(f.ctx)
	if err != nil || len(items) != 2 {
		t.Errorf("after keep 2: %d backups, %v", len(items), err)
	}
}

// A stored value that is not valid stops the start: only ChangeSchedule
// writes it.
func TestScheduleBadStoredValue(t *testing.T) {
	f := newFixture(t, nil)
	if _, err := f.st.PutSettings(f.ctx, "user:x", map[string]json.RawMessage{store.SettingBackupKeep: json.RawMessage(`500`)}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(f.data, DirName, InstallFile))
	if _, err := New(f.ctx, Config{Store: f.st, DataDir: f.data, Serve: true, CopyStatus: f.copy}); err == nil {
		t.Error("keep 500 from the store accepted")
	}
}

// Review n1 of #282: a change never makes the next backup later than the
// old schedule would. Old 03:00, last backup yesterday 03:00; at 02:00
// the time moves to 01:00: today's 01:00 lies before the change, but the
// old 03:00 is still due (no 46 h gap). The bound survives a restart.
func TestScheduleChangeNeverDelays(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Time = "03:00" })
	f.clock.set(time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC))
	f.backup(SystemActor)
	f.clock.set(time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC))
	if _, _, err := f.svc.ChangeSchedule(f.ctx, "user:admin", ScheduleChange{SetTime: true, Time: strp("01:00")}); err != nil {
		t.Fatal(err)
	}
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("02:00: today's 01:00 lies before the change: not due")
	}
	// A restart keeps the bound.
	svc, err := New(f.ctx, Config{Store: f.st, DataDir: f.data, Time: "03:00", Serve: true, CopyStatus: f.copy, Now: f.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.set(time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC))
	for _, s := range []*Service{f.svc, svc} {
		if due, _ := s.Due(f.ctx); !due {
			t.Error("03:00: the old schedule's backup is due")
		}
	}
	f.backup(SystemActor)
	if due, _ := f.svc.Due(f.ctx); due {
		t.Error("after the 03:00 backup: not due")
	}
	f.clock.set(time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC))
	if due, _ := f.svc.Due(f.ctx); !due {
		t.Error("next day 01:00: due by the new time")
	}
}
