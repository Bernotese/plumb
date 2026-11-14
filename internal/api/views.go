package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// page is the list envelope {items, next}.
type page struct {
	Items any     `json:"items"`
	Next  *string `json:"next"`
}

func encodeCursor(s string) *string {
	c := base64.RawURLEncoding.EncodeToString([]byte(s))
	return &c
}

func decodeCursor(r *http.Request) (string, error) {
	c := r.URL.Query().Get("cursor")
	if c == "" {
		return "", nil
	}
	if len(c) > 512 {
		return "", fmt.Errorf("cursor too long")
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	return string(b), err
}

// limit reads the page size (1…200, default 50).
func limit(r *http.Request) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 200 {
		return 0, fmt.Errorf("limit must be 1…200")
	}
	return n, nil
}

// idCursor reads a cursor that holds an id.
func idCursor(r *http.Request) (int64, error) {
	c, err := decodeCursor(r)
	if err != nil || c == "" {
		return 0, err
	}
	return strconv.ParseInt(c, 10, 64)
}

func utc(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// changes returns a non-nil list, so the JSON is [] and not null.
func changes(c []wire.Change) []wire.Change {
	if c == nil {
		return []wire.Change{}
	}
	return c
}

// --- plans ---

type resourcePlanView struct {
	Resource   string        `json:"resource"`
	Action     wire.Action   `json:"action"`
	Risk       wire.Risk     `json:"risk"`
	Changes    []wire.Change `json:"changes"`
	Unverified bool          `json:"unverified,omitempty"`
	After      []string      `json:"after,omitempty"`      // D-171
	RestartBy  []string      `json:"restart_by,omitempty"` // D-171
	// ConfirmSeconds: the safe apply window (D-300).
	ConfirmSeconds int `json:"confirm_seconds,omitempty"`
	// Summary: lines for the approver (D-300).
	Summary []string `json:"summary,omitempty"`
}

type targetPlanView struct {
	Target    string               `json:"target"`
	Secrets   []plan.PlannedSecret `json:"secrets,omitempty"`
	Resources []resourcePlanView   `json:"resources"`
}

type planView struct {
	ID           int64            `json:"id"`
	Origin       string           `json:"origin"`
	IssueID      *int64           `json:"issue_id"`
	RollbackTo   *int64           `json:"rollback_to"`
	Status       string           `json:"status"`
	Risk         string           `json:"risk"`
	Reason       string           `json:"reason"`
	Author       string           `json:"author"`
	CreatedAt    time.Time        `json:"created_at"`
	BaseRevision *int64           `json:"base_revision"`
	ApprovedBy   *string          `json:"approved_by"`
	ApprovedAt   *time.Time       `json:"approved_at"`
	Revision     *int64           `json:"revision"`
	RunID        *int64           `json:"run_id"`
	HumanOnly    []string         `json:"human_only"`
	Targets      []targetPlanView `json:"targets"`
	Warnings     []string         `json:"warnings"`
	// Images: builds and removals (D-199); missing means none.
	Images []imagePlanView `json:"images,omitempty"`
	// Pins: the pin changes of a pin_refresh Plan (D-335); missing for
	// other origins.
	Pins []pinRefreshView `json:"pins,omitempty"`
	// Documents: what an approval commits (T-170); only in the answers
	// that return one Plan, missing in the list.
	Documents          *[]plan.DocumentChange `json:"documents,omitempty"`
	DocumentsTruncated bool                   `json:"documents_truncated,omitempty"`
	// DocumentsUnavailable: the documents could not be computed (D1 of
	// #267); the UI says "document changes not available".
	DocumentsUnavailable bool `json:"documents_unavailable,omitempty"`
	// The copy rule (D-331, D-352); missing means none.
	GuestDataIssues  []int64               `json:"guest_data_issues,omitempty"`
	GuestDataMatches []plan.GuestDataMatch `json:"guest_data_matches,omitempty"`
}

func (s *Server) planView(ctx context.Context, p store.Plan) (planView, error) {
	v := planView{ID: p.ID, Origin: p.Origin, IssueID: nullID(p.IssueID), RollbackTo: nullID(p.RollbackTo), Status: p.Status, Risk: p.Risk,
		Reason: p.Reason, Author: p.Author, CreatedAt: p.CreatedAt.UTC(), BaseRevision: nullID(p.BaseRevision),
		ApprovedBy: nullString(p.ApprovedBy), ApprovedAt: utc(p.ApprovedAt), HumanOnly: append([]string{}, p.HumanOnly...), Targets: []targetPlanView{}, Warnings: []string{}}
	// Warnings of the origin, for example the secrets an import wrote (D-095).
	var prop struct {
		Notes []string          `json:"notes"`
		Pins  []plan.PinRefresh `json:"pins"`
	}
	if json.Unmarshal(p.Proposal, &prop) == nil {
		v.Warnings = append(v.Warnings, prop.Notes...)
		for _, pin := range prop.Pins { // D-335; only RefreshPin stores them
			v.Pins = append(v.Pins, viewPin(pin))
		}
	}
	if run, err := s.st.RunOfPlan(ctx, p.ID); err == nil {
		v.RunID, v.Revision = &run.ID, &run.RevisionID
		if p.Origin == plan.OriginBuild || p.Origin == plan.OriginAgentUpdate {
			v.Revision = nil // a build or an agent update commits no revision (D-199, D-267)
		}
	} else if err != store.ErrNotFound {
		return v, err
	}
	images, err := imagePlanViews(p.Images)
	if err != nil {
		return v, err
	}
	v.Images = images
	if issues, matches, err := s.st.PlanGuestData(ctx, p.ID); err == nil {
		v.GuestDataIssues = issues
		if err := json.Unmarshal(matches, &v.GuestDataMatches); err != nil {
			return v, err
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	var targets []plan.TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return v, err
	}
	for _, tp := range targets {
		tv := targetPlanView{Target: tp.Target, Secrets: tp.Secrets, Resources: []resourcePlanView{}}
		unverified := 0
		for _, rc := range tp.Resources {
			action := rc.Action
			if action == wire.ActionNone {
				action = wire.ActionUpdate
			}
			tv.Resources = append(tv.Resources, resourcePlanView{Resource: rc.Resource, Action: action, Risk: rc.Risk,
				Changes: changes(rc.Changes), Unverified: rc.Unverified, After: rc.After, RestartBy: rc.RestartBy,
				ConfirmSeconds: rc.ConfirmSeconds, Summary: rc.Summary})
			if rc.Unverified {
				unverified++
			}
		}
		switch {
		case unverified > 0 && tp.Guest == "create":
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s is a new target: %d resources are unverified until the first boot.", tp.Target, unverified))
		case unverified > 0:
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s: %d resources are not observed before the approval: they get a new secret, or a value that the import writes (D-156, D-158).", tp.Target, unverified))
		}
		v.Targets = append(v.Targets, tv)
	}
	switch {
	case p.Status == store.PlanStale && p.Origin == plan.OriginBuild:
		v.Warnings = append(v.Warnings, "The recipe of the Image changed. Build again.")
	case p.Status == store.PlanStale:
		v.Warnings = append(v.Warnings, "The head revision moved or a secret changed. Propose again.")
	}
	return v, nil
}

// --- runs ---

type stepView struct {
	Resource   string        `json:"resource"`
	Action     *wire.Action  `json:"action"`
	Status     string        `json:"status"`
	Changes    []wire.Change `json:"changes"`
	Output     string        `json:"output"`
	RolledBack bool          `json:"rolled_back,omitempty"` // D-300
	StartedAt  *time.Time    `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at"`
}

type targetRunView struct {
	Target string     `json:"target"`
	Status string     `json:"status"`
	Steps  []stepView `json:"steps"`
}

type runView struct {
	ID         int64      `json:"id"`
	PlanID     int64      `json:"plan_id"`
	Revision   int64      `json:"revision"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// WaitsForRestart: changes of the Run that wait for a stop and start
	// of the VM (D-195).
	WaitsForRestart int             `json:"waits_for_restart"`
	Targets         []targetRunView `json:"targets"`
	// Images are the build lanes (D-199); missing means none.
	Images []imageRunView `json:"images,omitempty"`
}

func (s *Server) runView(ctx context.Context, run store.Run) (runView, error) {
	v := runView{ID: run.ID, PlanID: run.PlanID, Revision: run.RevisionID, Status: run.Status,
		StartedAt: run.StartedAt.UTC(), FinishedAt: utc(run.FinishedAt), WaitsForRestart: run.WaitsForRestart, Targets: []targetRunView{}}
	steps, err := s.st.Steps(ctx, run.ID)
	if err != nil {
		return v, err
	}
	index := map[string]int{}
	lanes := map[string]*targetRunView{} // image lanes, by Image name
	var laneOrder []string
	for _, st := range steps {
		var tr *targetRunView
		if name, isImage := imageLane(st.Target); isImage {
			if tr = lanes[name]; tr == nil {
				tr = &targetRunView{Target: st.Target, Status: "succeeded", Steps: []stepView{}}
				lanes[name] = tr
				laneOrder = append(laneOrder, name)
			}
		} else {
			i, ok := index[st.Target]
			if !ok {
				i = len(v.Targets)
				index[st.Target] = i
				v.Targets = append(v.Targets, targetRunView{Target: st.Target, Status: "succeeded", Steps: []stepView{}})
			}
			tr = &v.Targets[i]
		}
		var ch []wire.Change
		if err := json.Unmarshal(st.Changes, &ch); err != nil {
			return v, err
		}
		sv := stepView{Resource: st.Resource, Status: st.Status, Changes: changes(ch), Output: st.Output,
			RolledBack: st.RolledBack, StartedAt: utc(st.StartedAt), FinishedAt: utc(st.FinishedAt)}
		if a := wire.ActionOf(ch); a != wire.ActionNone {
			sv.Action = &a
		}
		tr.Steps = append(tr.Steps, sv)
		switch st.Status {
		case "failed", "refused", "skipped":
			tr.Status = "failed"
		case "pending", "running":
			if tr.Status != "failed" && run.Status == "running" {
				tr.Status = "running"
			}
		}
	}
	if v.Images, err = s.imageRunViews(ctx, run, lanes, laneOrder); err != nil {
		return v, err
	}
	return v, nil
}

// --- issues ---

type resolutionView struct {
	Action string    `json:"action"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

type commentView struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type issueView struct {
	ID         int64           `json:"id"`
	Target     string          `json:"target"`
	Resource   string          `json:"resource"`
	Status     string          `json:"status"`
	Risk       wire.Risk       `json:"risk"`
	OpenedAt   time.Time       `json:"opened_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Changes    []wire.Change   `json:"changes"`
	Actions    []string        `json:"actions"`
	PlanID     *int64          `json:"plan_id"`
	Resolution *resolutionView `json:"resolution"`
	Comments   []commentView   `json:"comments"`
	Raw        bool            `json:"raw"`    // D-301; accept_mode == raw (D-362)
	Source     *sourceView     `json:"source"` // D-301; null for guest:* and agent:*
	// What accept writes (D-362).
	AcceptMode   string            `json:"accept_mode"`
	AcceptReason string            `json:"accept_reason"`
	AcceptParams []acceptParamView `json:"accept_params"`
}

type acceptParamView struct {
	Use   string `json:"use"`
	Index int    `json:"index"`
	Param string `json:"param"`
}

func (s *Server) issueView(ctx context.Context, i store.Issue) (issueView, error) {
	var ch []wire.Change
	if err := json.Unmarshal(i.Changes, &ch); err != nil {
		return issueView{}, err
	}
	v := issueView{ID: i.ID, Target: i.Target, Resource: i.Resource, Status: i.Status, Risk: wire.MaxRisk(ch),
		OpenedAt: i.OpenedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC(), Changes: changes(ch), Actions: []string{},
		PlanID: nullID(i.PlanID), Comments: []commentView{}}
	if i.Status == store.IssueOpen && issue.IgnoreOnly(i.Resource) {
		// ip is create-only: Plumb can neither revert nor accept (D-215).
		v.Actions = append(v.Actions, "ignore_once", "ignore_rule")
	} else if i.Status == store.IssueOpen {
		// A disk grown in Proxmox cannot be reverted: Proxmox cannot
		// shrink it (D-299). Accept takes the bigger size.
		if !(i.Resource == wire.GuestConfigID && plan.RevertShrinks(ch)) {
			v.Actions = append(v.Actions, "revert")
		}
		if issue.AcceptRefused(i.Resource, ch) == nil {
			v.Actions = append([]string{"accept"}, v.Actions...)
		}
		// The list does not depend on the caller; the server checks who
		// may ignore (D-091, D-092).
		v.Actions = append(v.Actions, "ignore_once", "ignore_rule")
	}
	if i.Status == store.IssueResolved {
		v.Resolution = &resolutionView{Action: i.Resolution, By: i.ResolvedBy, At: i.ResolvedAt.UTC()}
	}
	if err := s.issueSource(ctx, &v); err != nil {
		return v, err
	}
	if err := s.issueAccept(ctx, &v, ch); err != nil {
		return v, err
	}
	comments, err := s.st.Comments(ctx, i.ID)
	if err != nil {
		return v, err
	}
	for _, c := range comments {
		v.Comments = append(v.Comments, commentView{ID: c.ID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt.UTC()})
	}
	return v, nil
}

// issueAccept sets Issue.accept_mode, accept_reason and accept_params
// from the head revision (D-362), and Issue.raw to accept_mode == raw.
func (s *Server) issueAccept(ctx context.Context, v *issueView, ch []wire.Change) error {
	allowed := slices.Contains(v.Actions, "accept")
	m := issue.AcceptMode(nil, v.Target, v.Resource, ch, allowed)
	if m.Mode == state.AcceptRaw {
		err := s.headRaw(ctx, v.Target, func(set *state.Set, _ map[string]state.Source) {
			m = issue.AcceptMode(set, v.Target, v.Resource, ch, allowed)
		})
		if err != nil {
			return err
		}
	}
	v.AcceptMode, v.AcceptReason, v.AcceptParams = m.Mode, m.Reason, []acceptParamView{}
	for _, p := range m.Params {
		v.AcceptParams = append(v.AcceptParams, acceptParamView{Use: p.Use, Index: p.Index, Param: p.Param})
	}
	v.Raw = m.Mode == state.AcceptRaw
	return nil
}

// --- events ---

type eventView struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Time    time.Time       `json:"time"`
	Subject string          `json:"subject"`
	Actor   string          `json:"actor"`
	Data    json.RawMessage `json:"data"`
}
