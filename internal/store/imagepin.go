package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ApprovedPlans returns the approved Plans of one origin, oldest first. The
// pin refresh reads the accepted signature times from them (D-335).
func (s *Store) ApprovedPlans(ctx context.Context, origin string) ([]Plan, error) {
	return s.plansOf(ctx, PlanApproved, origin)
}

// ImagePinsUsed returns every base pin that an Image ever had: the sha256
// and sha512 of each committed version of its document, and the base pin
// of each of its built versions (D-335: a pin used before is a downgrade).
func (s *Store) ImagePinsUsed(ctx context.Context, image string) (map[string]bool, error) {
	used := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM doc WHERE kind = 'Image' AND name = ?`, image)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var d struct {
			Spec struct {
				SHA256 string `json:"sha256"`
				SHA512 string `json:"sha512"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(body), &d) == nil {
			for _, p := range []string{d.Spec.SHA256, d.Spec.SHA512} {
				if p != "" {
					used[p] = true
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var vrows *sql.Rows
	vrows, err = s.db.QueryContext(ctx, `SELECT base_sha256 FROM image_version WHERE image = ?`, image)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var p string
		if err := vrows.Scan(&p); err != nil {
			return nil, err
		}
		if p != "" {
			used[p] = true
		}
	}
	return used, vrows.Err()
}
