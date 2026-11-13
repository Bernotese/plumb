package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Plan states (D-045).
const (
	PlanPending  = "pending"
	PlanApproved = "approved"
	PlanStale    = "stale"
)

// Plan is a stored proposal with its computed changes.
type Plan struct {
	ID           int64
	BaseRevision int64 // 0: the empty state
	Origin       string
	IssueID      int64 // 0: none
	RollbackTo   int64 // the revision of a rollback (D-131); 0: none
	// HumanOnly lists why only a human session may approve (D-099):
	// "rule-change", "secret-binding". Empty for a normal Plan.
	HumanOnly []string
	Proposal  json.RawMessage
	Changes   json.RawMessage
	// Images is the image part of the Plan (D-199): a JSON array, [] for
	// none.
	Images     json.RawMessage
	Risk       string
	Status     string
	Reason     string
	Author     string
	ApprovedBy string
	CreatedAt  time.Time
	ApprovedAt time.Time
}

// Run is the execution of an approved Plan.
type Run struct {
	ID         int64
	PlanID     int64
	RevisionID int64
	Status     string // running | succeeded | failed
	StartedAt  time.Time
	FinishedAt time.Time
	// WaitsForRestart is the number of changes that the steps recorded
	// with waits_for_restart (D-195). Set by FinishRun.
	WaitsForRestart int
}

// Step is one resource of one target in a Run.
type Step struct {
	RunID      int64
	Seq        int64
	Target     string
	Resource   string
	Status     string
	Changes    json.RawMessage // [wire.Change], already redacted
	Output     string
	StartedAt  time.Time
	FinishedAt time.Time
}

// ErrPlanNotPending is returned when a Plan cannot be approved because it
// is approved or stale already.
var ErrPlanNotPending = errors.New("store: plan is not pending")

// ErrPlanStale is returned when the head moved since the Plan was made.
// The Plan is marked stale.
var ErrPlanStale = errors.New("store: plan is stale: the head revision moved")

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func jsonOr(v json.RawMessage, def string) string {
	if len(v) == 0 {
		return def
	}
	return string(v)
}

// CreatePlan stores a pending Plan.
func (s *Store) CreatePlan(ctx context.Context, p Plan) (Plan, error) {
	created := s.now()
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO plan (base_revision, origin, issue_id, rollback_to, human_only, proposal, changes, images, risk, status, reason, author, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?) RETURNING id`,
		nullID(p.BaseRevision), p.Origin, nullID(p.IssueID), nullID(p.RollbackTo), humanOnlyJSON(p.HumanOnly), jsonOr(p.Proposal, "{}"), jsonOr(p.Changes, "[]"), jsonOr(p.Images, "[]"),
		p.Risk, p.Reason, p.Author, created).Scan(&p.ID)
	if err != nil {
		return Plan{}, fmt.Errorf("store: create plan: %w", err)
	}
	p.Status = PlanPending
	p.CreatedAt, _ = parseTime(created)
	return p, nil
}

const planColumns = `id, COALESCE(base_revision, 0), origin, COALESCE(issue_id, 0), COALESCE(rollback_to, 0), human_only, proposal, changes, images, risk, status,
	reason, author, COALESCE(approved_by, ''), created_at, approved_at`

func scanPlan(row interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var humanOnly, proposal, changes, images, created string
	var approved sql.NullString
	err := row.Scan(&p.ID, &p.BaseRevision, &p.Origin, &p.IssueID, &p.RollbackTo, &humanOnly, &proposal, &changes, &images, &p.Risk, &p.Status,
		&p.Reason, &p.Author, &p.ApprovedBy, &created, &approved)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	if err != nil {
		return Plan{}, err
	}
	p.Proposal, p.Changes, p.Images = json.RawMessage(proposal), json.RawMessage(changes), json.RawMessage(images)
	if err := json.Unmarshal([]byte(humanOnly), &p.HumanOnly); err != nil {
		return Plan{}, fmt.Errorf("store: plan %d: human_only: %w", p.ID, err)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return Plan{}, err
	}
	p.ApprovedAt, err = nullTime(approved)
	return p, err
}

// GetPlan returns one Plan.
func (s *Store) GetPlan(ctx context.Context, id int64) (Plan, error) {
	return scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planColumns+` FROM plan WHERE id = ?`, id))
}

// headTx returns the head revision id, or 0.
func headTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var head int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM revision`).Scan(&head)
	return head, err
}

// ApprovePlan approves a pending Plan in one transaction: it checks that
// the head is still the base of the Plan, commits the revision, marks the
// Plan approved and creates the Run. When the head moved, the Plan is
// marked stale and ErrPlanStale is returned. rev.PlanID is set here.
func (s *Store) ApprovePlan(ctx context.Context, planID int64, approvedBy string, rev NewRevision) (Revision, Run, error) {
	rev.PlanID = planID
	if err := checkNewRevision(rev); err != nil {
		return Revision{}, Run{}, err
	}
	var out Revision
	var run Run
	stale := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var status string
		var base int64
		err := tx.QueryRowContext(ctx, `SELECT status, COALESCE(base_revision, 0) FROM plan WHERE id = ?`, planID).
			Scan(&status, &base)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != PlanPending {
			return ErrPlanNotPending
		}
		head, err := headTx(ctx, tx)
		if err != nil {
			return err
		}
		if head != base {
			stale = true
			_, err := tx.ExecContext(ctx, `UPDATE plan SET status = 'stale' WHERE id = ?`, planID)
			return err // commit the stale mark
		}
		if out, err = s.commitTx(ctx, tx, rev); err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx,
			`UPDATE plan SET status = 'approved', approved_by = ?, approved_at = ? WHERE id = ?`,
			approvedBy, now, planID); err != nil {
			return err
		}
		run = Run{PlanID: planID, RevisionID: out.ID, Status: "running"}
		run.StartedAt, _ = parseTime(now)
		return tx.QueryRowContext(ctx,
			`INSERT INTO run (plan_id, revision_id, status, started_at) VALUES (?, ?, 'running', ?) RETURNING id`,
			planID, out.ID, now).Scan(&run.ID)
	})
	if err != nil {
		return Revision{}, Run{}, err
	}
	if stale {
		return Revision{}, Run{}, ErrPlanStale
	}
	return out, run, nil
}

// ErrHeadMoved is returned by ApproveBuildPlan when the head is not the
// revision that the caller checked. The caller checks again.
var ErrHeadMoved = errors.New("store: the head revision moved")

// ApproveBuildPlan approves a pending Plan of origin build (D-199) or
// agent_update (D-267): it
// commits no revision. The Run is pinned to the head revision, which must
// be head: the revision whose recipe the caller compared with the Plan.
func (s *Store) ApproveBuildPlan(ctx context.Context, planID int64, approvedBy string, head int64) (Run, error) {
	var run Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var status, origin string
		err := tx.QueryRowContext(ctx, `SELECT status, origin FROM plan WHERE id = ?`, planID).Scan(&status, &origin)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if origin != "build" && origin != "agent_update" {
			return fmt.Errorf("store: plan %d has origin %s, not build or agent_update", planID, origin)
		}
		if status != PlanPending {
			return ErrPlanNotPending
		}
		now, err := headTx(ctx, tx)
		if err != nil {
			return err
		}
		if now != head || head == 0 {
			return ErrHeadMoved
		}
		at := s.now()
		if _, err := tx.ExecContext(ctx,
			`UPDATE plan SET status = 'approved', approved_by = ?, approved_at = ? WHERE id = ?`, approvedBy, at, planID); err != nil {
			return err
		}
		run = Run{PlanID: planID, RevisionID: head, Status: "running"}
		run.StartedAt, _ = parseTime(at)
		return tx.QueryRowContext(ctx,
			`INSERT INTO run (plan_id, revision_id, status, started_at) VALUES (?, ?, 'running', ?) RETURNING id`,
			planID, head, at).Scan(&run.ID)
	})
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

// PendingPlans returns the pending Plans of one origin, oldest first.
func (s *Store) PendingPlans(ctx context.Context, origin string) ([]Plan, error) {
	return s.plansOf(ctx, PlanPending, origin)
}

// StalePlans returns the stale Plans of one origin, oldest first. An
// agent_update Plan is stale only after a discard (D-278).
func (s *Store) StalePlans(ctx context.Context, origin string) ([]Plan, error) {
	return s.plansOf(ctx, PlanStale, origin)
}

func (s *Store) plansOf(ctx context.Context, status, origin string) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+planColumns+` FROM plan WHERE status = ? AND origin = ? ORDER BY id`, status, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkStalePlans marks every pending Plan whose base is not the head as
// stale. It returns their ids. A Plan of origin build is not stale by a
// head change: only its recipe counts (D-199); the engine checks it.
func (s *Store) MarkStalePlans(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`UPDATE plan SET status = 'stale'
		 WHERE status = 'pending' AND origin NOT IN ('build', 'agent_update') AND COALESCE(base_revision, 0) <> (SELECT COALESCE(MAX(id), 0) FROM revision)
		 RETURNING id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- runs and steps ---

// GetRun returns one Run.
func (s *Store) GetRun(ctx context.Context, id int64) (Run, error) {
	var r Run
	var started string
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, plan_id, revision_id, status, started_at, finished_at, waits_for_restart FROM run WHERE id = ?`, id).
		Scan(&r.ID, &r.PlanID, &r.RevisionID, &r.Status, &started, &finished, &r.WaitsForRestart)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if r.StartedAt, err = parseTime(started); err != nil {
		return Run{}, err
	}
	r.FinishedAt, err = nullTime(finished)
	return r, err
}

// FinishRun sets the final status of a Run and the number of its
// changes that wait for a restart (D-195).
func (s *Store) FinishRun(ctx context.Context, id int64, status string, waits int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE run SET status = ?, finished_at = ?, waits_for_restart = ? WHERE id = ? AND status = 'running'`,
		status, s.now(), waits, id)
	return err
}

// AddStep appends a step to a Run and returns its sequence number.
func (s *Store) AddStep(ctx context.Context, st Step) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO run_step (run_id, seq, target, resource, status, changes)
		 VALUES (?1, (SELECT COALESCE(MAX(seq), 0) + 1 FROM run_step WHERE run_id = ?1), ?2, ?3, ?4, ?5)
		 RETURNING seq`,
		st.RunID, st.Target, st.Resource, st.Status, jsonOr(st.Changes, "[]")).Scan(&seq)
	return seq, err
}

// UpdateStep sets the status of a step, and its changes and output when
// they are not empty. Running sets started_at; a final status sets
// finished_at.
func (s *Store) UpdateStep(ctx context.Context, runID, seq int64, status string, changes json.RawMessage, output string) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE run_step SET status = ?3,
			changes = CASE WHEN ?4 = '' THEN changes ELSE ?4 END,
			output = CASE WHEN ?5 = '' THEN output ELSE ?5 END,
			started_at = COALESCE(started_at, ?6),
			finished_at = CASE WHEN ?3 IN ('pending', 'running') THEN NULL ELSE ?6 END
		 WHERE run_id = ?1 AND seq = ?2`,
		runID, seq, status, string(changes), output, now)
	return err
}

// Steps returns the steps of a Run in order.
func (s *Store) Steps(ctx context.Context, runID int64) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, seq, target, resource, status, changes, output, started_at, finished_at
		 FROM run_step WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var st Step
		var changes string
		var started, finished sql.NullString
		if err := rows.Scan(&st.RunID, &st.Seq, &st.Target, &st.Resource, &st.Status, &changes, &st.Output, &started, &finished); err != nil {
			return nil, err
		}
		st.Changes = json.RawMessage(changes)
		if st.StartedAt, err = nullTime(started); err != nil {
			return nil, err
		}
		if st.FinishedAt, err = nullTime(finished); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// --- bindings ---

// Binding says what a document is in the driver.
type Binding struct {
	Kind, Name, Ref, Digest string
	// Waiting lists the values that wait for the next stop and start of
	// the guest (D-195). Only Target bindings use it.
	Waiting []Waiting
	// RootGrow is the size in GiB of a VM disk that the driver grew while
	// the agent did not grow the root file system yet (D-310, review M1 of
	// #217): the next Plan of the Target sends grow again. 0: nothing
	// pending. It is stored as the entry RootGrowAttr of the waiting
	// column (no migration) and is never in Waiting.
	RootGrow int
}

// RootGrowAttr is the waiting entry that holds Binding.RootGrow.
const RootGrowAttr = "root_fs"

// Waiting is one value that is set in the hypervisor and waits for the
// next stop and start of the guest. Since is when Plumb first saw this
// attribute with this To in the list.
type Waiting struct {
	Attr  string    `json:"attr"`
	From  int       `json:"from"`
	To    int       `json:"to"`
	Since time.Time `json:"since"`
}

// PutBinding stores or replaces a binding.
func (s *Store) PutBinding(ctx context.Context, b Binding) error {
	w := append([]Waiting(nil), b.Waiting...)
	if b.RootGrow > 0 {
		w = append(w, Waiting{Attr: RootGrowAttr, To: b.RootGrow})
	}
	waiting, err := waitingJSON(w)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO binding (kind, name, ref, digest, waiting, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (kind, name) DO UPDATE SET ref = excluded.ref, digest = excluded.digest, waiting = excluded.waiting, updated_at = excluded.updated_at`,
		b.Kind, b.Name, b.Ref, b.Digest, waiting, s.now())
	return err
}

func waitingJSON(w []Waiting) (string, error) {
	if w == nil {
		w = []Waiting{}
	}
	for i := range w {
		w[i].Since = w[i].Since.UTC()
	}
	raw, err := json.Marshal(w)
	return string(raw), err
}

func scanWaiting(raw string, b *Binding) error {
	var all []Waiting
	if err := json.Unmarshal([]byte(raw), &all); err != nil {
		return fmt.Errorf("store: binding %s/%s: waiting: %w", b.Kind, b.Name, err)
	}
	b.Waiting, b.RootGrow = nil, 0
	for _, w := range all {
		if w.Attr == RootGrowAttr {
			b.RootGrow = w.To
			continue
		}
		b.Waiting = append(b.Waiting, w)
	}
	if len(b.Waiting) == 0 {
		b.Waiting = nil
	}
	return nil
}

// GetBinding returns a binding, or ErrNotFound.
func (s *Store) GetBinding(ctx context.Context, kind, name string) (Binding, error) {
	b := Binding{Kind: kind, Name: name}
	var waiting string
	err := s.db.QueryRowContext(ctx, `SELECT ref, digest, waiting FROM binding WHERE kind = ? AND name = ?`, kind, name).
		Scan(&b.Ref, &b.Digest, &waiting)
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	if err != nil {
		return Binding{}, err
	}
	return b, scanWaiting(waiting, &b)
}

// DeleteBinding removes a binding.
func (s *Store) DeleteBinding(ctx context.Context, kind, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM binding WHERE kind = ? AND name = ?`, kind, name)
	return err
}

// Bindings returns all bindings of a kind, ordered by name.
func (s *Store) Bindings(ctx context.Context, kind string) ([]Binding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, ref, digest, waiting FROM binding WHERE kind = ? ORDER BY name`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Binding
	for rows.Next() {
		b := Binding{Kind: kind}
		var waiting string
		if err := rows.Scan(&b.Name, &b.Ref, &b.Digest, &waiting); err != nil {
			return nil, err
		}
		if err := scanWaiting(waiting, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RunningRuns returns the Runs with status running.
func (s *Store) RunningRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM run WHERE status = 'running' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]Run, 0, len(ids))
	for _, id := range ids {
		r, err := s.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// FailUnfinishedSteps marks every pending or running step of a Run as
// failed with the output.
func (s *Store) FailUnfinishedSteps(ctx context.Context, runID int64, output string) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE run_step SET status = 'failed', output = ?, finished_at = ?, started_at = COALESCE(started_at, ?)
		 WHERE run_id = ? AND status IN ('pending', 'running')`, output, now, now, runID)
	return err
}

// DiscardPlan marks a pending Plan stale. It returns ErrPlanNotPending
// for any other status.
func (s *Store) DiscardPlan(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan SET status = 'stale' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.GetPlan(ctx, id); err != nil {
			return err
		}
		return ErrPlanNotPending
	}
	return nil
}

// MarkPlanStale marks one pending Plan stale. ok is false when the Plan
// was not pending.
func (s *Store) MarkPlanStale(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE plan SET status = 'stale' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func humanOnlyJSON(reasons []string) string {
	if len(reasons) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(reasons)
	return string(b)
}
