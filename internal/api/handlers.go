package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such id.")
		return 0, false
	}
	return id, true
}

func badQuery(w http.ResponseWriter, err error) {
	problem(w, http.StatusBadRequest, "bad-request", "Bad request", err.Error())
}

// fail maps an error of the domain packages to a problem.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var pe *plan.ProposalError
	var se state.Errors
	var ms *plan.MissingSecretsError
	var ao *plan.AgentOutdatedError
	switch {
	case repairProblem(w, err):
	case adoptProblem(w, err):
	case errors.As(err, &ao):
		writeProblem(w, problemBody{Type: "urn:plumb:problem:agent-outdated", Title: "Agent is outdated", Status: http.StatusConflict,
			Detail: ao.Error(), Targets: ao.Targets, Missing: ao.Missing})
	case errors.Is(err, plan.ErrAgentNoUpdate):
		problem(w, http.StatusConflict, "nothing-to-do", "Nothing to do", strings.TrimPrefix(err.Error(), "plan: "))
	case errors.As(err, &ms):
		var fe []fieldError
		for _, e := range ms.Errors {
			fe = append(fe, fieldError{Pointer: e.Path, Message: fmt.Sprintf("%s %q: %s", e.Kind, e.Name, e.Msg)})
		}
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Detail: "The proposal refers to secrets that do not exist.", Errors: fe, Secrets: ms.Names})
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", "")
	case errors.Is(err, store.ErrPlanStale):
		problem(w, http.StatusConflict, "plan-stale", "Plan is stale", "The head revision moved or a secret of the Plan changed. Propose again.")
	case errors.Is(err, store.ErrPlanNotPending):
		problem(w, http.StatusConflict, "plan-not-pending", "Plan is not pending", "The Plan is approved or stale already.")
	case errors.Is(err, store.ErrIssueChanged):
		problem(w, http.StatusConflict, "issue-changed", "Issue changed", "A new report changed the issue. Read it again and decide again.")
	case errors.Is(err, store.ErrIssueNotOpen):
		problem(w, http.StatusConflict, "issue-not-open", "Issue is not open", "Another Plan resolves it, or it is resolved.")
	case errors.Is(err, issue.ErrAcceptSensitive), errors.Is(err, issue.ErrAcceptImage), errors.Is(err, issue.ErrAddressFix),
		errors.Is(err, issue.ErrAcceptFirewall):
		problem(w, http.StatusConflict, "action-not-allowed", "Action not allowed", err.Error())
	case errors.Is(err, plan.ErrRestoredMode):
		problem(w, http.StatusConflict, "restored-mode", "Restored mode", err.Error())
	case errors.Is(err, plan.ErrOffline):
		problem(w, http.StatusConflict, "target-offline", "Target is offline", err.Error())
	case errors.Is(err, store.ErrBuildRunning):
		problem(w, http.StatusConflict, "build-running", "A build is running", err.Error())
	case errors.Is(err, plan.ErrTargetBusy):
		problem(w, http.StatusConflict, "target-busy", "Target is busy", err.Error())
	case errors.As(err, &se):
		var fe []fieldError
		for _, e := range se {
			fe = append(fe, fieldError{Pointer: e.Path, Message: fmt.Sprintf("%s %q: %s", e.Kind, e.Name, e.Msg)})
		}
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Detail: "The documents are not valid.", Errors: uniqueFieldErrors(fe)})
	case errors.As(err, &pe):
		detail := pe.Msg
		if pe.Target != "" {
			detail = "Target " + pe.Target + ": " + pe.Msg
		}
		problem(w, http.StatusUnprocessableEntity, "validation", "Validation failed", detail)
	default:
		s.internal(w, err)
	}
}

// --- documents ---

type docView struct {
	Revision int64           `json:"revision"`
	Document json.RawMessage `json:"document"`
	// Status is a *targetStatus for a Target, an *imageStatus for a built
	// Image, and missing for every other document.
	Status any `json:"status,omitempty"`
	// Raw lists the overrides of a Target (D-301); missing for every
	// other document.
	Raw *[]rawView `json:"raw,omitempty"`
}

type targetStatus struct {
	Phase    string     `json:"phase"`
	Agent    string     `json:"agent"`
	Power    string     `json:"power"`
	LastSeen *time.Time `json:"last_seen"`
	GuestID  *string    `json:"guest_id"`
	// ImageVersion is the built version the guest was cloned from (D-200).
	ImageVersion *int     `json:"image_version,omitempty"`
	IPs          []string `json:"ips"`
	// ReportedIPs is what the agent says (D-214); never in IPs (D-216).
	ReportedIPs     []string   `json:"reported_ips"`
	ReportedAt      *time.Time `json:"reported_at"`
	ReportedIPsMore bool       `json:"reported_ips_more,omitempty"`
	AppliedRevision *int64     `json:"applied_revision"`
	OpenIssues      int        `json:"open_issues"`
	LastRunID       *int64     `json:"last_run_id"`
	// IgnoredDrift is drift that a rule dropped (D-092); missing = none.
	IgnoredDrift []ignoredDriftView `json:"ignored_drift,omitempty"`
	// Waiting are values that wait for a stop and start (D-195); always
	// present.
	Waiting []waitingView `json:"waiting"`
	// The agent (T-092, D-257 … D-259): version of the last hello, update
	// state, what the desired state needs and the agent lacks, last revert.
	AgentVersion  *string          `json:"agent_version"`
	AgentUpdate   string           `json:"agent_update"`
	AgentMissing  []string         `json:"agent_missing"`
	AgentReverted *agentRevertView `json:"agent_reverted"`
	// Repair is the SSH repair readiness (D-384, T-191); null without an agent.
	Repair *plan.RepairStatus `json:"repair"`
	// Adopted: the Target holds a guest a person made (D-386).
	Adopted bool `json:"adopted,omitempty"`
	// NotAdopted: adopted, but no guest is bound: its adopt Run was
	// refused (D-395). Adopt again, or delete the Target.
	NotAdopted bool `json:"not_adopted,omitempty"`
}

type agentRevertView struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type waitingView struct {
	Attr  string    `json:"attr"`
	From  int       `json:"from"`
	To    int       `json:"to"`
	Since time.Time `json:"since"`
}

type ignoredDriftView struct {
	Resource string    `json:"resource"`
	Rule     string    `json:"rule"`
	Attrs    []string  `json:"attrs"`
	LastSeen time.Time `json:"last_seen"`
}

// headDocs returns the documents of one kind in the head revision.
func (s *Server) headDocs(ctx context.Context, kind string) (int64, []store.DocRow, error) {
	head, err := s.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	all, err := s.st.RevisionDocs(ctx, head.ID)
	if err != nil {
		return 0, nil, err
	}
	var out []store.DocRow
	for _, d := range all {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return head.ID, out, nil
}

func (s *Server) docView(ctx context.Context, head int64, d store.DocRow) (docView, error) {
	rev, err := s.st.DocChangedAt(ctx, head, d.Kind, d.Name, d.Hash)
	if err != nil {
		return docView{}, err
	}
	v := docView{Revision: rev, Document: json.RawMessage(d.Body)}
	if d.Kind == state.KindTarget {
		st, err := s.targetStatus(ctx, d.Name)
		if err != nil {
			return docView{}, err
		}
		v.Status = &st
		raw, err := s.targetRaw(ctx, d.Name)
		if err != nil {
			return docView{}, err
		}
		v.Raw = &raw
	}
	if d.Kind == state.KindImage {
		st, err := s.imageStatus(ctx, d)
		if err != nil {
			return docView{}, err
		}
		if st != nil {
			v.Status = st
		}
	}
	return v, nil
}

func (s *Server) targetStatus(ctx context.Context, name string) (targetStatus, error) {
	live, err := s.cfg.Plans.Live(ctx, name)
	if err != nil {
		return targetStatus{}, err
	}
	st := targetStatus{Agent: live.Agent, Power: live.Power, LastSeen: utc(live.LastSeen), GuestID: nullString(live.GuestID), IPs: live.IPs}
	if live.ImageVersion > 0 {
		v := live.ImageVersion
		st.ImageVersion = &v
	}
	if st.IPs == nil {
		st.IPs = []string{}
	}
	st.ReportedIPs, st.ReportedIPsMore = []string{}, live.ReportedMore
	if live.Reported != nil {
		st.ReportedIPs, st.ReportedAt = live.Reported, utc(live.ReportedAt)
	}
	ag, err := s.cfg.Plans.AgentStatus(ctx, name)
	if err != nil {
		return st, err
	}
	if ag.Version != "" {
		st.AgentVersion = &ag.Version
	}
	st.AgentUpdate, st.AgentMissing = ag.Update, ag.Missing
	if st.Repair, err = s.cfg.Plans.RepairStatus(ctx, name); err != nil {
		return st, err
	}
	if rv := ag.Reverted; rv != nil {
		// A guest time that did not parse is the time of the hello (the
		// schema needs a time).
		at, err := time.Parse(time.RFC3339, rv.At)
		if err != nil {
			at = ag.HelloAt
		}
		st.AgentReverted = &agentRevertView{From: rv.From, To: rv.To, Reason: rv.Reason, At: at.UTC()}
	}
	// D-386: an adopted Target (from the head revision).
	if err := s.headRaw(ctx, name, func(set *state.Set, _ map[string]state.Source) { _, st.Adopted = set.AdoptedOf(name) }); err != nil {
		return st, err
	}
	st.NotAdopted = st.Adopted && live.GuestID == ""
	st.Waiting = []waitingView{}
	for _, w := range live.Waiting {
		st.Waiting = append(st.Waiting, waitingView{Attr: w.Attr, From: w.From, To: w.To, Since: w.Since.UTC()})
	}
	issues, err := s.st.Issues(ctx, name)
	if err != nil {
		return st, err
	}
	for _, i := range issues {
		if i.Status == store.IssueOpen {
			st.OpenIssues++
		}
	}
	rev, err := s.st.AppliedRevision(ctx, name)
	if err != nil {
		return st, err
	}
	st.AppliedRevision = nullID(rev)
	if s.cfg.Issues != nil {
		dropped, err := s.cfg.Issues.IgnoredDrift(ctx, name)
		if err != nil {
			return st, err
		}
		for _, d := range dropped {
			st.IgnoredDrift = append(st.IgnoredDrift, ignoredDriftView{Resource: d.Resource, Rule: d.Rule, Attrs: d.Attrs, LastSeen: d.LastSeen.UTC()})
		}
	}
	lastFailed := false
	if run, err := s.st.LastRunOfTarget(ctx, name); err == nil {
		st.LastRunID = &run.ID
		lastFailed = run.Status == "failed"
		if lastFailed && live.Agent == "online" {
			// A failed agent update leaves the old agent working; the
			// status shows it (agent_update, agent_reverted). Not an
			// error while that agent is online (T-102).
			p, err := s.st.GetPlan(ctx, run.PlanID)
			if err != nil {
				return st, err
			}
			lastFailed = p.Origin != plan.OriginAgentUpdate
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return st, err
	}
	switch {
	case live.GuestID == "" && live.Busy:
		st.Phase = "provisioning"
	case live.GuestID == "":
		st.Phase = "pending"
	case live.Busy:
		st.Phase = "applying"
	case live.Agent == "none":
		st.Phase = "enrolling"
	case lastFailed:
		st.Phase = "error"
	case st.OpenIssues > 0:
		st.Phase = "drifted"
	case live.DesiredPower == "stopped" && live.Power == "stopped":
		// Stopped on purpose: its agent is offline and that is no Issue (D-299).
		st.Phase = "stopped"
	case len(st.Waiting) > 0:
		st.Phase = "restart_needed"
	default:
		st.Phase = "ready"
	}
	return st, nil
}

func (s *Server) listDocs(kind string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, _ *principal) {
		n, err := limit(r)
		if err != nil {
			badQuery(w, err)
			return
		}
		after, err := decodeCursor(r)
		if err != nil {
			badQuery(w, err)
			return
		}
		head, docs, err := s.headDocs(r.Context(), kind)
		if err != nil {
			s.internal(w, err)
			return
		}
		items := []docView{}
		var next *string
		for _, d := range docs { // ordered by name
			if after != "" && d.Name <= after {
				continue
			}
			if len(items) == n {
				next = encodeCursor(items[len(items)-1].name())
				break
			}
			v, err := s.docView(r.Context(), head, d)
			if err != nil {
				s.internal(w, err)
				return
			}
			items = append(items, v)
		}
		writeJSON(w, http.StatusOK, page{Items: items, Next: next})
	}
}

func (v docView) name() string {
	var d struct {
		Name string `json:"name"`
	}
	json.Unmarshal(v.Document, &d)
	return d.Name
}

func (s *Server) getDoc(kind string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, _ *principal) {
		head, err := s.st.Head(r.Context())
		if errors.Is(err, store.ErrNotFound) {
			problem(w, http.StatusNotFound, "not-found", "Not found", "There is no revision yet.")
			return
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		d, err := s.st.RevisionDoc(r.Context(), head.ID, kind, r.PathValue("name"))
		if errors.Is(err, store.ErrNotFound) {
			problem(w, http.StatusNotFound, "not-found", "Not found", fmt.Sprintf("%s %q does not exist in the head revision.", kind, r.PathValue("name")))
			return
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		v, err := s.docView(r.Context(), head.ID, d)
		if err != nil {
			s.internal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// --- plans ---

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) writePlan(w http.ResponseWriter, r *http.Request, status int, p store.Plan) {
	v, err := s.planView(r.Context(), p)
	if err != nil {
		s.internal(w, err)
		return
	}
	// Plan.documents (T-170): only for a pending or stale Plan; an
	// approved one has its revision diff. A failure never fails the
	// answer: the field is left out and documents_unavailable says so
	// (review D1 of #267).
	if p.Status != store.PlanApproved {
		docs, cut, err := s.cfg.Plans.PlanDocuments(r.Context(), p)
		if err != nil {
			s.log.Warn("plan documents", "plan", p.ID, "err", err)
			v.DocumentsUnavailable = true
		} else {
			v.Documents, v.DocumentsTruncated = &docs, cut
		}
	}
	if status == http.StatusCreated {
		w.Header().Set("Location", fmt.Sprintf("/api/v1/plans/%d", p.ID))
	}
	writeJSON(w, status, v)
}

func (s *Server) reenroll(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	var req reasonRequest
	if !decode(w, r, &req, true) {
		return
	}
	if len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	if req.Reason == "" {
		req.Reason = "re-enroll " + name
	}
	pl, err := s.cfg.Plans.Propose(r.Context(), plan.Input{Proposal: plan.Proposal{Reenroll: []string{name}},
		Reason: req.Reason, Author: p.name, Origin: plan.OriginReenroll})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

func (s *Server) agentUpdate(w http.ResponseWriter, r *http.Request, p *principal) {
	var req reasonRequest
	if !decode(w, r, &req, true) {
		return
	}
	if len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	pl, err := s.cfg.Plans.ProposeAgentUpdate(r.Context(), name, req.Reason, p.name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

func (s *Server) headTarget(ctx context.Context, name string) (store.DocRow, error) {
	head, err := s.st.Head(ctx)
	if err != nil {
		return store.DocRow{}, err
	}
	return s.st.RevisionDoc(ctx, head.ID, state.KindTarget, name)
}

type proposalRequest struct {
	Reason  string        `json:"reason"`
	Put     []state.Doc   `json:"put"`
	Delete  []plan.DocKey `json:"delete"`
	Targets []string      `json:"targets"` // plan also without a document change (D-090)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request, p *principal) {
	var req proposalRequest
	if !decode(w, r, &req, false) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Errors: []fieldError{{"/reason", "A reason of 1 to 2000 characters is required."}}})
		return
	}
	if len(req.Put)+len(req.Delete)+len(req.Targets) == 0 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Errors: []fieldError{{"/put", "The proposal changes nothing."}}})
		return
	}
	pl, err := s.cfg.Plans.Propose(r.Context(), plan.Input{Proposal: plan.Proposal{Put: req.Put, Delete: req.Delete, Targets: req.Targets},
		Reason: req.Reason, Author: p.name})
	var se state.Errors
	var ms *plan.MissingSecretsError
	detail, missing := "The documents are not valid.", []string(nil)
	if errors.As(err, &ms) {
		se, detail, missing = ms.Errors, "The proposal refers to secrets that do not exist.", ms.Names
	}
	if ms != nil || errors.As(err, &se) {
		// Point the field errors at the request body.
		var fe []fieldError
		for _, e := range se {
			ptr := e.Path
			for i, d := range req.Put {
				if d.Kind == e.Kind && d.Name == e.Name {
					ptr = fmt.Sprintf("/put/%d%s", i, e.Path)
				}
			}
			fe = append(fe, fieldError{Pointer: ptr, Message: fmt.Sprintf("%s %q: %s", e.Kind, e.Name, e.Msg)})
		}
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Detail: detail, Errors: uniqueFieldErrors(fe), Secrets: missing})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	before, err := idCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != store.PlanPending && status != store.PlanApproved && status != store.PlanStale {
		badQuery(w, errors.New("status must be pending, approved or stale"))
		return
	}
	plans, err := s.st.ListPlans(r.Context(), status, before, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []planView{}
	var next *string
	for i, pl := range plans {
		if i == n {
			next = encodeCursor(strconv.FormatInt(plans[n-1].ID, 10))
			break
		}
		v, err := s.planView(r.Context(), pl)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, v)
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, _ *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pl, err := s.st.GetPlan(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusOK, pl)
}

func (s *Server) discardPlan(w http.ResponseWriter, r *http.Request, p *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Plans.Discard(r.Context(), id, p.name); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type approveRequest struct {
	Comment string `json:"comment"`
}

// riskError is a refusal of the approval rule.
type riskError struct{ code, title, detail string }

func (e *riskError) Error() string { return e.detail }

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request, p *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req approveRequest
	if !decode(w, r, &req, true) {
		return
	}
	if len(req.Comment) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "comment is longer than 2000 characters.")
		return
	}
	// The approval rule (ARCHITECTURE §7, D-046, D-329): a session approves
	// by its role (apply up to disruptive, admin every risk); an apply
	// token approves up to its max_risk, never destructive.
	check := func(pl store.Plan) error {
		if p.human() {
			// A session approves by its role (D-329): human-only Plans
			// too, up to the risk that the role allows.
			return approveAsHuman(p, wire.Risk(pl.Risk))
		}
		// D-099: a Plan that changes a Rule (or binds a secret) is for a
		// human approver only, whatever its risk.
		if len(pl.HumanOnly) > 0 {
			return &riskError{"human-required", "Human session required",
				fmt.Sprintf("Only a human session can approve this Plan (%s).", strings.Join(pl.HumanOnly, ", "))}
		}
		risk := wire.Risk(pl.Risk)
		if risk == wire.RiskDestructive {
			return &riskError{"risk-too-high", "Risk exceeds token limit", "Plan risk is destructive. A human must approve."}
		}
		if p.tier != TierApply || risk.Exceeds(p.maxRisk) {
			return &riskError{"risk-too-high", "Risk exceeds token limit",
				fmt.Sprintf("Plan risk is %s. Token max_risk is %s.", risk, p.maxRisk)}
		}
		return nil
	}
	run, err := s.cfg.Plans.Approve(r.Context(), id, p.name, check)
	var re *riskError
	if errors.As(err, &re) {
		problem(w, http.StatusForbidden, re.code, re.title, re.detail)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.runView(r.Context(), run)
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/runs/%d", run.ID))
	writeJSON(w, http.StatusAccepted, v)
}

// --- runs ---

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	before, err := idCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	runs, err := s.st.ListRuns(r.Context(), before, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []runView{}
	var next *string
	for i, run := range runs {
		if i == n {
			next = encodeCursor(strconv.FormatInt(runs[n-1].ID, 10))
			break
		}
		v, err := s.runView(r.Context(), run)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, v)
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, _ *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.st.GetRun(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.runView(r.Context(), run)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// --- issues ---

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	before, err := idCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	q := r.URL.Query()
	status, target := q.Get("status"), q.Get("target")
	if status != "" && status != store.IssueOpen && status != store.IssueResolving && status != store.IssueResolved {
		badQuery(w, errors.New("status must be open, resolving or resolved"))
		return
	}
	issues, err := s.st.ListIssues(r.Context(), status, target, before, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []issueView{}
	var next *string
	for i, iss := range issues {
		if i == n {
			next = encodeCursor(strconv.FormatInt(issues[n-1].ID, 10))
			break
		}
		v, err := s.issueView(r.Context(), iss)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, v)
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request, _ *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	iss, err := s.st.GetIssue(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.issueView(r.Context(), iss)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type resolveRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
	Rule   *struct {
		AllTargets bool     `json:"all_targets"`
		Attrs      []string `json:"attrs"`
	} `json:"rule"`
}

func (s *Server) resolveIssue(w http.ResponseWriter, r *http.Request, p *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req resolveRequest
	if !decode(w, r, &req, false) {
		return
	}
	switch req.Action {
	case issue.ActionAccept, issue.ActionRevert, issue.ActionIgnoreOnce, issue.ActionIgnoreRule:
	default:
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "action must be accept, revert, ignore_once or ignore_rule.")
		return
	}
	if len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	var scope issue.RuleScope
	if req.Rule != nil {
		if req.Action != issue.ActionIgnoreRule {
			problem(w, http.StatusBadRequest, "bad-request", "Bad request", "rule is only for the action ignore_rule.")
			return
		}
		if fe := checkRuleAttrs(req.Rule.Attrs); fe != nil {
			writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
				Status: http.StatusUnprocessableEntity, Errors: fe})
			return
		}
		scope = issue.RuleScope{AllTargets: req.Rule.AllTargets, Attrs: req.Rule.Attrs}
	}
	if _, err := s.st.GetIssue(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	if req.Action == issue.ActionIgnoreOnce {
		s.ignoreOnce(w, r, p, id)
		return
	}
	pl, err := s.cfg.Issues.ResolveWithRule(r.Context(), id, req.Action, p.name, scope)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// checkRuleAttrs checks the attrs of a rule scope like the Rule schema.
func checkRuleAttrs(attrs []string) []fieldError {
	if attrs == nil {
		return nil
	}
	if len(attrs) == 0 || len(attrs) > 50 {
		return []fieldError{{"/rule/attrs", "attrs needs 1 to 50 attributes."}}
	}
	seen := map[string]bool{}
	for i, a := range attrs {
		if a == "" || len(a) > 100 || seen[a] {
			return []fieldError{{fmt.Sprintf("/rule/attrs/%d", i), "Each attribute is 1 to 100 characters and appears once."}}
		}
		seen[a] = true
	}
	return nil
}

// ignoreOnce resolves an Issue without a Plan (D-091). A session with role
// apply (admin for destructive) may; an apply token only when every change is within its max_risk and none is
// sensitive or destructive.
func (s *Server) ignoreOnce(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
	check := func(changes []wire.Change) error {
		if p.human() {
			return ignoreAsHuman(p, changes) // by role (D-329)
		}
		if p.tier != TierApply {
			return &riskError{"human-required", "Human session required",
				"Only a human session or an apply token can ignore drift; a plan token cannot."}
		}
		for _, c := range changes {
			if c.Sensitive || c.Risk == wire.RiskDestructive {
				return &riskError{"human-required", "Human session required",
					fmt.Sprintf("Attribute %q is sensitive or destructive. Only a human can ignore it.", c.Attr)}
			}
			if c.Risk.Exceeds(p.maxRisk) {
				return &riskError{"risk-too-high", "Risk exceeds token limit",
					fmt.Sprintf("Attribute %q has risk %s. Token max_risk is %s.", c.Attr, c.Risk, p.maxRisk)}
			}
		}
		return nil
	}
	iss, err := s.cfg.Issues.IgnoreOnce(r.Context(), id, p.name, check)
	var re *riskError
	if errors.As(err, &re) {
		problem(w, http.StatusForbidden, re.code, re.title, re.detail)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.issueView(r.Context(), iss)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type commentRequest struct {
	Body string `json:"body"`
}

func (s *Server) commentIssue(w http.ResponseWriter, r *http.Request, p *principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req commentRequest
	if !decode(w, r, &req, false) {
		return
	}
	if strings.TrimSpace(req.Body) == "" || len(req.Body) > 10000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "body must have 1 to 10000 characters.")
		return
	}
	if _, err := s.st.GetIssue(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.AddComment(r.Context(), store.Comment{IssueID: id, Author: p.name, Body: req.Body})
	if err != nil {
		s.internal(w, err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Emit(r.Context(), "issue.updated", fmt.Sprintf("issue:%d", id), p.name, map[string]any{"comment": c.ID})
	}
	writeJSON(w, http.StatusCreated, commentView{ID: c.ID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt.UTC()})
}

// uniqueFieldErrors drops a field error that repeats an earlier one with
// the same pointer and message: one schema rule can fail once per
// attribute and map to the same text (T-208, host finding F3).
func uniqueFieldErrors(fe []fieldError) []fieldError {
	seen := make(map[fieldError]bool, len(fe))
	out := fe[:0:0]
	for _, e := range fe {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}
