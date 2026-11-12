package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Agent is an enrolled agent. One agent per target.
type Agent struct {
	ID             string
	Target         string
	CredentialHash string // SHA-256 (hex) of the secret part of the credential
	Version        string
	Hostname       string
	EnrolledAt     time.Time
	LastSeenAt     time.Time // zero: never seen after enrollment
	// Hello is what the last hello said (T-092); nil before the first.
	Hello *AgentHello
}

// AgentHello is the stored part of a hello, after the guest-text checks
// (ARCHITECTURE §14). Capabilities and Reverted are JSON; nil Capabilities
// means the agent sent none (wire.BaselineCapabilities).
type AgentHello struct {
	Version      string
	BinarySHA256 string
	Capabilities json.RawMessage
	Reverted     json.RawMessage
	At           time.Time
}

// PutEnrollToken stores a new enrollment token hash for a target. It
// replaces the old token of the target and removes the enrolled agent of
// the target, so the old token and the old credential stop working
// (re-enrollment, D-038).
func (s *Store) PutEnrollToken(ctx context.Context, target, hash string, expires time.Time) error {
	if target == "" || hash == "" {
		return errors.New("store: enroll token needs a target and a hash")
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent WHERE target = ?`, target); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO enroll_token (target, hash, expires_at, used_at, created_at) VALUES (?, ?, ?, NULL, ?)
			 ON CONFLICT (target) DO UPDATE SET hash = excluded.hash, expires_at = excluded.expires_at,
			 used_at = NULL, created_at = excluded.created_at`,
			target, hash, formatTime(expires), s.now())
		return err
	})
}

// RevokeAgent removes the enrolled agent and the enrollment token of a
// target in one transaction, so the old credential and an unused token
// stop working (Target delete, D-211). A target with neither is no error.
func (s *Store) RevokeAgent(ctx context.Context, target string) error {
	if target == "" {
		return errors.New("store: revoke needs a target")
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent WHERE target = ?`, target); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM enroll_token WHERE target = ?`, target)
		return err
	})
}

// ErrInvalidToken is returned for a token that is unknown, used or
// expired. The three cases are one error on purpose.
var ErrInvalidToken = errors.New("store: invalid enrollment token")

// Enroll uses an enrollment token and stores the new agent of its target,
// in one transaction. The token must be unused and not expired at now.
// The agent's Target comes from the token. An older agent of the target
// is replaced.
func (s *Store) Enroll(ctx context.Context, tokenHash string, now time.Time, a Agent) (Agent, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`UPDATE enroll_token SET used_at = ?1
			 WHERE hash = ?2 AND used_at IS NULL AND expires_at > ?1
			 RETURNING target`, formatTime(now), tokenHash).Scan(&a.Target)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent WHERE target = ?`, a.Target); err != nil {
			return err
		}
		a.EnrolledAt = now
		a.LastSeenAt = time.Time{}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO agent (id, target, credential_hash, version, hostname, enrolled_at) VALUES (?, ?, ?, ?, ?, ?)`,
			a.ID, a.Target, a.CredentialHash, a.Version, a.Hostname, formatTime(now))
		return err
	})
	if errors.Is(err, ErrInvalidToken) {
		return Agent{}, ErrInvalidToken
	}
	if err != nil {
		return Agent{}, fmt.Errorf("store: enroll: %w", err)
	}
	return a, nil
}

const agentColumns = `id, target, credential_hash, version, hostname, enrolled_at, last_seen_at,
	hello_version, binary_sha256, capabilities, reverted, hello_at`

func scanAgent(row interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var enrolled string
	var seen, hv, sha, caps, rev, hat sql.NullString
	err := row.Scan(&a.ID, &a.Target, &a.CredentialHash, &a.Version, &a.Hostname, &enrolled, &seen,
		&hv, &sha, &caps, &rev, &hat)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if a.EnrolledAt, err = parseTime(enrolled); err != nil {
		return Agent{}, err
	}
	if a.LastSeenAt, err = nullTime(seen); err != nil {
		return Agent{}, err
	}
	if hat.Valid {
		h := &AgentHello{Version: hv.String, BinarySHA256: sha.String}
		if caps.Valid {
			h.Capabilities = json.RawMessage(caps.String)
		}
		if rev.Valid {
			h.Reverted = json.RawMessage(rev.String)
		}
		if h.At, err = parseTime(hat.String); err != nil {
			return Agent{}, err
		}
		a.Hello = h
	}
	return a, nil
}

// SetAgentHello stores the checked hello of an agent. It returns
// ErrNotFound when the agent no longer exists.
func (s *Store) SetAgentHello(ctx context.Context, id string, h AgentHello) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent SET hello_version = ?, binary_sha256 = ?, capabilities = ?, reverted = ?, hello_at = ? WHERE id = ?`,
		h.Version, h.BinarySHA256, nullJSON(h.Capabilities), nullJSON(h.Reverted), formatTime(h.At), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// AgentByID returns an enrolled agent.
func (s *Store) AgentByID(ctx context.Context, id string) (Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent WHERE id = ?`, id))
}

// AgentByTarget returns the enrolled agent of a target.
func (s *Store) AgentByTarget(ctx context.Context, target string) (Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent WHERE target = ?`, target))
}

// TouchAgent records that the agent called in at t. It returns
// ErrNotFound when the agent no longer exists.
func (s *Store) TouchAgent(ctx context.Context, id string, t time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent SET last_seen_at = ? WHERE id = ?`, formatTime(t), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAgentInfo stores the version and hostname from a hello report.
func (s *Store) UpdateAgentInfo(ctx context.Context, id, version, hostname string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent SET version = ?, hostname = ? WHERE id = ?`, version, hostname, id)
	return err
}
