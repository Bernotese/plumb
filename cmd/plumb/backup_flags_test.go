package main

import (
	"io"
	"testing"
)

// --backup-time and --backup-keep (T-112, D-302), with env defaults.
func TestBackupFlags(t *testing.T) {
	f, err := parseServe([]string{"--dev"}, io.Discard)
	if err != nil || f.backupTime != "03:00" || f.backupKeep != 7 {
		t.Fatalf("defaults: %+v %v", f, err)
	}
	t.Setenv("PLUMB_BACKUP_TIME", "04:30")
	t.Setenv("PLUMB_BACKUP_KEEP", "14")
	f, err = parseServe([]string{"--dev"}, io.Discard)
	if err != nil || f.backupTime != "04:30" || f.backupKeep != 14 {
		t.Fatalf("env: %+v %v", f, err)
	}
	f, err = parseServe([]string{"--dev", "--backup-time", "23:59", "--backup-keep", "100"}, io.Discard)
	if err != nil || f.backupTime != "23:59" || f.backupKeep != 100 {
		t.Fatalf("flags: %+v %v", f, err)
	}
	for _, bad := range [][]string{{"--backup-time", "24:00"}, {"--backup-time", "3:00"}, {"--backup-keep", "0"}, {"--backup-keep", "101"}, {"--backup-keep", "x"}} {
		if _, err := parseServe(append([]string{"--dev"}, bad...), io.Discard); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// The source of each backup value: env when the env or the flag names it
// (D-361, GET /settings).
func TestBackupFlagSources(t *testing.T) {
	t.Setenv("PLUMB_BACKUP_TIME", "")
	t.Setenv("PLUMB_BACKUP_KEEP", "")
	f, err := parseServe([]string{"--dev"}, io.Discard)
	if err != nil || f.backupTimeSrc != "default" || f.backupKeepSrc != "default" {
		t.Fatalf("defaults: %+v %v", f, err)
	}
	t.Setenv("PLUMB_BACKUP_TIME", "04:30")
	f, _ = parseServe([]string{"--dev", "--backup-keep", "3"}, io.Discard)
	if f.backupTimeSrc != "env" || f.backupKeepSrc != "env" {
		t.Errorf("env and flag: %+v", f)
	}
}
