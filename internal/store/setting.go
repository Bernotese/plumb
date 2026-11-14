package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Setting keys (D-361). The table accepts no other key.
const (
	SettingBackupTime = "backup.time"
	SettingBackupKeep = "backup.keep"
)

// Setting is one stored controller setting. Value "null" means not set.
type Setting struct {
	Key       string
	Value     json.RawMessage
	ChangedBy string
	ChangedAt time.Time
}

// Set reports whether the setting holds a value (not JSON null).
func (s Setting) Set() bool { return len(s.Value) > 0 && string(s.Value) != "null" }

// Settings returns every stored setting by key.
func (s *Store) Settings(ctx context.Context) (map[string]Setting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, changed_by, changed_at FROM setting`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Setting{}
	for rows.Next() {
		var st Setting
		var v, at string
		if err := rows.Scan(&st.Key, &v, &st.ChangedBy, &at); err != nil {
			return nil, err
		}
		st.Value = json.RawMessage(v)
		if st.ChangedAt, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("setting %s: %w", st.Key, err)
		}
		out[st.Key] = st
	}
	return out, rows.Err()
}

// PutSettings writes the given keys in one transaction. A nil or "null"
// value marks the key as not set. It returns the rows as written.
func (s *Store) PutSettings(ctx context.Context, by string, values map[string]json.RawMessage) (map[string]Setting, error) {
	out := map[string]Setting{}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		at, _ := parseTime(now)
		for key, v := range values {
			if len(v) == 0 {
				v = json.RawMessage("null")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value, changed_by, changed_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, changed_by = excluded.changed_by, changed_at = excluded.changed_at`,
				key, string(v), by, now); err != nil {
				return err
			}
			out[key] = Setting{Key: key, Value: v, ChangedBy: by, ChangedAt: at}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
