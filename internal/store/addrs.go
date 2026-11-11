package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// The address list that an agent reports (D-212, D-214). It is display
// data from the guest: root in the guest can send any list. Only the
// status view and the address check of the engine read it (D-216; a source
// test in internal/plan checks the callers of ReportedAddrs).

// ReportedAddrs is the stored list of the agent of a target.
type ReportedAddrs struct {
	List      []string
	Truncated bool
	At        time.Time // when this list arrived
}

// addrsJSON is the form of the agent.addrs column.
type addrsJSON struct {
	List      []string `json:"list"`
	Truncated bool     `json:"truncated"`
}

// ErrAddrsRateLimited is returned by SetAgentAddrs for a change within
// the gap after the last stored change. The change is dropped, not queued.
var ErrAddrsRateLimited = errors.New("store: address list changed too often")

// SetAgentAddrs stores the address list of an agent. A nil list sets it to
// unknown. An equal list writes nothing (changed is false). A new list
// within gap after the last stored list gives ErrAddrsRateLimited (D-212).
// Setting a known list to unknown is not limited: it only writes when a
// list is known, so a flood of alternating reports still writes at most
// twice per gap, and an old agent always clears the list of a newer one
// (D-217). A missing agent is ErrNotFound.
func (s *Store) SetAgentAddrs(ctx context.Context, agentID string, list []string, truncated bool, now time.Time, gap time.Duration) (changed bool, err error) {
	var next sql.NullString
	if list != nil {
		b, err := json.Marshal(addrsJSON{List: list, Truncated: truncated})
		if err != nil {
			return false, err
		}
		next = sql.NullString{String: string(b), Valid: true}
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var cur, at sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT addrs, addrs_at FROM agent WHERE id = ?`, agentID).Scan(&cur, &at)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.Valid == next.Valid && (!cur.Valid || sameAddrs(cur.String, next.String)) {
			return nil
		}
		if !next.Valid {
			_, err = tx.ExecContext(ctx, `UPDATE agent SET addrs = NULL WHERE id = ?`, agentID)
			changed = err == nil
			return err
		}
		if at.Valid {
			last, err := parseTime(at.String)
			if err != nil {
				return err
			}
			if now.Sub(last) < gap {
				return ErrAddrsRateLimited
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE agent SET addrs = ?, addrs_at = ? WHERE id = ?`, next.String, formatTime(now), agentID)
		changed = err == nil
		return err
	})
	return changed, err
}

// sameAddrs compares two stored lists by value.
func sameAddrs(a, b string) bool {
	var x, y addrsJSON
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return x.Truncated == y.Truncated && slices.Equal(x.List, y.List)
}

// ReportedAddrs returns the stored list of the agent of a target. known
// is false when the target has no agent or the list is unknown. Callers:
// only the status view and the address check (D-216).
func (s *Store) ReportedAddrs(ctx context.Context, target string) (r ReportedAddrs, known bool, err error) {
	var cur, at sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT addrs, addrs_at FROM agent WHERE target = ?`, target).Scan(&cur, &at)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !cur.Valid {
		return ReportedAddrs{}, false, nil
	}
	if err != nil {
		return ReportedAddrs{}, false, err
	}
	var v addrsJSON
	if err := json.Unmarshal([]byte(cur.String), &v); err != nil {
		return ReportedAddrs{}, false, err
	}
	if v.List == nil {
		v.List = []string{}
	}
	r = ReportedAddrs{List: v.List, Truncated: v.Truncated}
	if r.At, err = nullTime(at); err != nil {
		return ReportedAddrs{}, false, err
	}
	return r, true, nil
}
