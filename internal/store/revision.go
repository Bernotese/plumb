package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DocRow is one stored document. Body is the canonical JSON that package
// state produced (state.Canonical) and Hash is its SHA-256 (state.Hash).
type DocRow struct {
	Kind string
	Name string
	Hash string
	Body []byte
}

// Revision is an immutable, numbered set of documents.
type Revision struct {
	ID        int64
	Author    string // principal
	Reason    string
	PlanID    int64 // 0: no plan (the seed revision)
	CreatedAt time.Time
}

// NewRevision is the input of CommitRevision.
type NewRevision struct {
	Author string
	Reason string
	PlanID int64 // 0: no plan
	// Docs is the full document set of the new revision, not a delta.
	Docs []DocRow
}

// DocChange is one entry of a revision diff.
type DocChange struct {
	Kind string
	Name string
	Op   DiffOp
	From string // hash in the old revision; empty for added
	To   string // hash in the new revision; empty for removed
}

// DiffOp says how a document differs between two revisions.
type DiffOp string

const (
	DocAdded   DiffOp = "added"
	DocChanged DiffOp = "changed"
	DocRemoved DiffOp = "removed"
)

// CommitRevision stores a new revision with the next number. A document
// body that is already stored (same hash) is shared, not copied. The store
// checks that every hash is the SHA-256 of its body and that kind and name
// match the body, so a row can never lie about its content.
func (s *Store) CommitRevision(ctx context.Context, in NewRevision) (Revision, error) {
	if err := checkNewRevision(in); err != nil {
		return Revision{}, err
	}
	var rev Revision
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		rev, err = s.commitTx(ctx, tx, in)
		return err
	})
	if err != nil {
		return Revision{}, fmt.Errorf("store: commit revision: %w", err)
	}
	return rev, nil
}

func checkNewRevision(in NewRevision) error {
	if in.Author == "" {
		return errors.New("store: revision needs an author")
	}
	seen := make(map[[2]string]bool, len(in.Docs))
	for _, d := range in.Docs {
		if err := checkDocRow(d); err != nil {
			return err
		}
		key := [2]string{d.Kind, d.Name}
		if seen[key] {
			return fmt.Errorf("store: %s %q is twice in the revision", d.Kind, d.Name)
		}
		seen[key] = true
	}
	return nil
}

// commitTx inserts a revision inside a transaction. The input is checked.
func (s *Store) commitTx(ctx context.Context, tx *sql.Tx, in NewRevision) (Revision, error) {
	rev := Revision{Author: in.Author, Reason: in.Reason, PlanID: in.PlanID}
	created := s.now()
	var plan any
	if in.PlanID != 0 {
		plan = in.PlanID
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO revision (id, author, reason, plan_id, created_at)
		 VALUES ((SELECT COALESCE(MAX(id), 0) + 1 FROM revision), ?, ?, ?, ?)`,
		in.Author, in.Reason, plan, created)
	if err != nil {
		return Revision{}, err
	}
	if rev.ID, err = res.LastInsertId(); err != nil {
		return Revision{}, err
	}
	for _, d := range in.Docs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO doc (hash, kind, name, body) VALUES (?, ?, ?, ?) ON CONFLICT (hash) DO NOTHING`,
			d.Hash, d.Kind, d.Name, string(d.Body)); err != nil {
			return Revision{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO revision_doc (revision_id, kind, name, hash) VALUES (?, ?, ?, ?)`,
			rev.ID, d.Kind, d.Name, d.Hash); err != nil {
			return Revision{}, err
		}
	}
	rev.CreatedAt, _ = parseTime(created)
	return rev, nil
}

func checkDocRow(d DocRow) error {
	sum := sha256.Sum256(d.Body)
	if hex.EncodeToString(sum[:]) != d.Hash {
		return fmt.Errorf("store: %s %q: hash does not match the body", d.Kind, d.Name)
	}
	var head struct{ Kind, Name string }
	if err := json.Unmarshal(d.Body, &head); err != nil {
		return fmt.Errorf("store: %s %q: body is not JSON: %w", d.Kind, d.Name, err)
	}
	if head.Kind != d.Kind || head.Name != d.Name || d.Kind == "" || d.Name == "" {
		return fmt.Errorf("store: %s %q: kind and name do not match the body", d.Kind, d.Name)
	}
	return nil
}

const revisionColumns = `id, author, reason, COALESCE(plan_id, 0), created_at`

func scanRevision(row interface{ Scan(...any) error }) (Revision, error) {
	var r Revision
	var created string
	if err := row.Scan(&r.ID, &r.Author, &r.Reason, &r.PlanID, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Revision{}, ErrNotFound
		}
		return Revision{}, err
	}
	t, err := parseTime(created)
	if err != nil {
		return Revision{}, fmt.Errorf("store: revision %d: %w", r.ID, err)
	}
	r.CreatedAt = t
	return r, nil
}

// Head returns the newest revision (the desired state).
// It returns ErrNotFound when there is no revision yet.
func (s *Store) Head(ctx context.Context) (Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx,
		`SELECT `+revisionColumns+` FROM revision ORDER BY id DESC LIMIT 1`))
}

// Revision returns one revision.
func (s *Store) Revision(ctx context.Context, id int64) (Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx,
		`SELECT `+revisionColumns+` FROM revision WHERE id = ?`, id))
}

// Revisions returns up to limit revisions with an id below before, newest
// first. before <= 0 starts at the head. limit is clamped to 1…500.
func (s *Store) Revisions(ctx context.Context, before int64, limit int) ([]Revision, error) {
	limit = min(max(limit, 1), 500)
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+revisionColumns+` FROM revision WHERE id < ? ORDER BY id DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevisionDocs returns all documents of a revision, ordered by kind and name.
func (s *Store) RevisionDocs(ctx context.Context, id int64) ([]DocRow, error) {
	if _, err := s.Revision(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.kind, d.name, d.hash, d.body FROM revision_doc r JOIN doc d ON d.hash = r.hash
		 WHERE r.revision_id = ? ORDER BY r.kind, r.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DocRow
	for rows.Next() {
		var d DocRow
		var body string
		if err := rows.Scan(&d.Kind, &d.Name, &d.Hash, &body); err != nil {
			return nil, err
		}
		d.Body = []byte(body)
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevisionDoc returns one document of a revision.
func (s *Store) RevisionDoc(ctx context.Context, id int64, kind, name string) (DocRow, error) {
	var d DocRow
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT d.kind, d.name, d.hash, d.body FROM revision_doc r JOIN doc d ON d.hash = r.hash
		 WHERE r.revision_id = ? AND r.kind = ? AND r.name = ?`, id, kind, name).
		Scan(&d.Kind, &d.Name, &d.Hash, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return DocRow{}, ErrNotFound
	}
	d.Body = []byte(body)
	return d, err
}

// Doc returns a stored document by its hash.
func (s *Store) Doc(ctx context.Context, hash string) (DocRow, error) {
	d := DocRow{Hash: hash}
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT kind, name, body FROM doc WHERE hash = ?`, hash).
		Scan(&d.Kind, &d.Name, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return DocRow{}, ErrNotFound
	}
	d.Body = []byte(body)
	return d, err
}

// Diff lists the documents that were added, changed or removed from
// revision a to revision b, ordered by kind and name. Revision 0 is the
// empty state. An unknown revision is ErrNotFound.
func (s *Store) Diff(ctx context.Context, a, b int64) ([]DocChange, error) {
	for _, id := range []int64{a, b} {
		if id == 0 {
			continue
		}
		if _, err := s.Revision(ctx, id); err != nil {
			return nil, fmt.Errorf("store: diff: revision %d: %w", id, err)
		}
	}
	// A full outer join of the two document lists, written as a union of
	// two left joins (both sides are keyed by kind and name).
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, name, from_hash, to_hash FROM (
			SELECT a.kind, a.name, a.hash AS from_hash, COALESCE(b.hash, '') AS to_hash
			FROM revision_doc a LEFT JOIN revision_doc b
				ON b.revision_id = ?2 AND b.kind = a.kind AND b.name = a.name
			WHERE a.revision_id = ?1
			UNION ALL
			SELECT b.kind, b.name, '', b.hash
			FROM revision_doc b LEFT JOIN revision_doc a
				ON a.revision_id = ?1 AND a.kind = b.kind AND a.name = b.name
			WHERE b.revision_id = ?2 AND a.hash IS NULL
		) WHERE from_hash <> to_hash ORDER BY kind, name`, a, b)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DocChange
	for rows.Next() {
		var c DocChange
		if err := rows.Scan(&c.Kind, &c.Name, &c.From, &c.To); err != nil {
			return nil, err
		}
		switch {
		case c.From == "":
			c.Op = DocAdded
		case c.To == "":
			c.Op = DocRemoved
		default:
			c.Op = DocChanged
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DocCounts returns the number of documents of each revision with an id
// in [low, high], in one query. A revision without documents has no entry.
func (s *Store) DocCounts(ctx context.Context, low, high int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT revision_id, COUNT(*) FROM revision_doc WHERE revision_id BETWEEN ? AND ? GROUP BY revision_id`, low, high)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
