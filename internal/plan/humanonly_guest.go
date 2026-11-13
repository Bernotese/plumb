package plan

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// The copy rule (D-331, D-352): a Plan that sets a value, or adds a list
// element, that a guest reported as drift in the last 30 days, on any
// Target, is human-only with reason accept-guest-data.
//
// The check compares the desired state, not the observed changes: it
// hashes every value of the render of each touched Target that is new
// against the render of the base. A copy of a reported value into the
// same Target shows no observed change (the guest has the value already),
// so the planned changes alone would miss it.

// GuestValueWindow is how long a reported value counts after its drift
// ended, and how long a Target stays flagged after its cap was reached.
const GuestValueWindow = 30 * 24 * time.Hour

// maxMatchElement bounds the element shown in a match (review of #243: a
// whole file can be one value).
const maxMatchElement = 200

// GuestDataMatch is one entry of Plan.guest_data_matches. Element is the
// Plan's own value (never the guest's), cut to maxMatchElement.
type GuestDataMatch struct {
	IssueID  int64  `json:"issue_id,omitempty"`
	Target   string `json:"target"`
	Resource string `json:"resource"`
	Attr     string `json:"attr"`
	Element  string `json:"element,omitempty"`
}

// guestData is the result of the copy rule for one Plan.
type guestData struct {
	flagged []string // touched Targets whose cap was reached
	issues  []int64
	matches []GuestDataMatch
}

func (g guestData) humanOnly() bool { return len(g.flagged) > 0 || len(g.matches) > 0 }

// fallbackGuestKey keys the hashes when the engine has no secret store
// (tests only; serve always has one). Hashes then do not outlive the
// process.
var fallbackGuestKey = func() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}()

func (e *Engine) guestHash(v string) string {
	if e.cfg.Secrets != nil {
		return e.cfg.Secrets.GuestValueHash(v)
	}
	m := hmac.New(sha256.New, fallbackGuestKey)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

// GuestValues returns the hashed values of a drift item that the copy
// rule stores: for each change that is not sensitive, the elements of the
// reported value that the desired value does not have. Only items that a
// guest reports count: agent resources and guest:address (its list comes
// from the agent); guest:config comes from Proxmox, and the controller's
// own items (agent:values, the Issues of a restore) carry no guest data.
func (e *Engine) GuestValues(it wire.Item) []store.GuestValue {
	typ, _, ok := wire.SplitID(it.Resource)
	if !ok || it.Status != wire.StatusDiffers || !wire.AgentType(typ) && it.Resource != wire.GuestAddressID {
		return nil
	}
	var out []store.GuestValue
	seen := map[store.GuestValue]bool{}
	for _, c := range it.Changes {
		if c.Sensitive || c.From == nil {
			continue
		}
		want := state.GuestValueElems(typ, c.Attr, c.To)
		for _, el := range state.GuestValueElems(typ, c.Attr, c.From) {
			if _, found := slices.BinarySearch(want, el); found {
				continue // the desired state has it: not guest data
			}
			v := store.GuestValue{Resource: it.Resource, Attr: c.Attr, Hash: e.guestHash(el)}
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// planElem is a new value of the planned state.
type planElem struct {
	target, resource, attr, elem string
}

// newElems returns the values of the render of target in next that its
// render in base does not have, keyed by hash. Sensitive attributes are
// skipped: their value is a secret, never guest data the controller knows.
func (e *Engine) newElems(base, next *state.Set, target string, out map[string][]planElem) error {
	before, err := renderOrNil(base, target)
	if err != nil {
		return err
	}
	after, err := renderOrNil(next, target)
	if err != nil || after == nil {
		return err
	}
	old := map[string]wire.Resource{}
	if before != nil {
		old[before.Guest.ID] = before.Guest
		for _, r := range before.Resources {
			old[r.ID] = r
		}
	}
	for _, r := range append([]wire.Resource{after.Guest}, after.Resources...) {
		if r.ID == "" {
			continue
		}
		prev, had := old[r.ID]
		for attr, v := range r.Attrs {
			if slices.Contains(r.Sensitive, attr) {
				continue
			}
			var was []string
			if had && !slices.Contains(prev.Sensitive, attr) {
				was = state.GuestValueElems(prev.Type, attr, prev.Attrs[attr])
			}
			for _, el := range state.GuestValueElems(r.Type, attr, v) {
				if _, found := slices.BinarySearch(was, el); found {
					continue
				}
				h := e.guestHash(el)
				out[h] = append(out[h], planElem{target, r.ID, attr, el})
			}
		}
	}
	return nil
}

// checkGuestData runs the copy rule for a Plan from base to next that
// touches targets.
func (e *Engine) checkGuestData(ctx context.Context, base, next *state.Set, targets []string) (guestData, error) {
	var g guestData
	flagged, err := e.st.GuestValueFlagged(ctx, targets, e.cfg.Now())
	if err != nil {
		return g, err
	}
	g.flagged = flagged
	elems := map[string][]planElem{}
	for _, t := range targets {
		if err := e.newElems(base, next, t, elems); err != nil {
			return g, err
		}
	}
	if len(elems) == 0 {
		return g, nil
	}
	hashes := make([]string, 0, len(elems))
	for h := range elems {
		hashes = append(hashes, h)
	}
	slices.Sort(hashes)
	rows, err := e.st.MatchGuestValues(ctx, hashes)
	if err != nil {
		return g, err
	}
	seen := map[GuestDataMatch]bool{}
	for _, row := range rows {
		if row.IssueID > 0 && !slices.Contains(g.issues, row.IssueID) {
			g.issues = append(g.issues, row.IssueID)
		}
		for _, pe := range elems[row.Hash] {
			m := GuestDataMatch{IssueID: row.IssueID, Target: pe.target, Resource: pe.resource, Attr: pe.attr, Element: cutElement(pe.elem)}
			if !seen[m] {
				seen[m] = true
				g.matches = append(g.matches, m)
			}
		}
	}
	slices.Sort(g.issues)
	slices.SortFunc(g.matches, func(a, b GuestDataMatch) int {
		for _, c := range [][2]string{{a.Target, b.Target}, {a.Resource, b.Resource}, {a.Attr, b.Attr}, {a.Element, b.Element}} {
			if c[0] != c[1] {
				if c[0] < c[1] {
					return -1
				}
				return 1
			}
		}
		return int(a.IssueID - b.IssueID)
	})
	return g, nil
}

// cutElement keeps the first maxMatchElement bytes of s (whole runes) and
// marks the cut.
func cutElement(s string) string {
	if len(s) <= maxMatchElement {
		return s
	}
	n := maxMatchElement
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// withGuestReason adds accept-guest-data to reasons when g needs it.
func withGuestReason(reasons []string, g guestData) []string {
	if g.humanOnly() && !slices.Contains(reasons, HumanOnlyAcceptGuestData) {
		reasons = append(reasons, HumanOnlyAcceptGuestData)
	}
	return reasons
}

// storeGuestData records g for a new pending Plan.
func (e *Engine) storeGuestData(ctx context.Context, p store.Plan, g guestData) error {
	if len(g.issues) == 0 && len(g.matches) == 0 {
		return nil
	}
	ms, err := json.Marshal(g.matches)
	if err != nil {
		return err
	}
	_, err = e.st.SetPlanGuestData(ctx, p.ID, p.HumanOnly, g.issues, ms)
	return err
}

// recheckGuestData runs the copy rule again at approval (a report that
// arrived after the Plan counts) and records what is new. It never
// removes a reason or a match. It returns the Plan with its reasons.
func (e *Engine) recheckGuestData(ctx context.Context, p store.Plan) (store.Plan, error) {
	if p.Origin == OriginBuild || p.Origin == OriginAgentUpdate {
		return p, nil // no document change, no Target values (D-199, D-267)
	}
	var prop Proposal
	if err := json.Unmarshal(p.Proposal, &prop); err != nil {
		return p, err
	}
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil {
		return p, err
	}
	names := make([]string, len(targets))
	for i, tp := range targets {
		names[i] = tp.Target
	}
	var base *state.Set
	var err error
	if p.BaseRevision == 0 {
		base, err = state.Validate(nil)
	} else {
		base, err = e.load(ctx, p.BaseRevision)
	}
	if err != nil {
		return p, err
	}
	next, err := apply(base, prop)
	if err != nil {
		return p, err
	}
	g, err := e.checkGuestData(ctx, base, next, names)
	if err != nil {
		return p, err
	}
	oldIssues, oldMatches, err := e.planGuestData(ctx, p.ID)
	if err != nil {
		return p, err
	}
	issues, matches := slices.Clone(oldIssues), slices.Clone(oldMatches)
	for _, id := range g.issues {
		if !slices.Contains(issues, id) {
			issues = append(issues, id)
		}
	}
	for _, m := range g.matches {
		if !slices.Contains(matches, m) {
			matches = append(matches, m)
		}
	}
	reasons := withGuestReason(slices.Clone(p.HumanOnly), g)
	if len(reasons) == len(p.HumanOnly) && len(issues) == len(oldIssues) && len(matches) == len(oldMatches) {
		return p, nil
	}
	slices.Sort(issues)
	ms, err := json.Marshal(matches)
	if err != nil {
		return p, err
	}
	ok, err := e.st.SetPlanGuestData(ctx, p.ID, reasons, issues, ms)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, store.ErrPlanNotPending
	}
	p.HumanOnly = reasons
	return p, nil
}

// planGuestData reads what the copy rule recorded for a Plan.
func (e *Engine) planGuestData(ctx context.Context, id int64) ([]int64, []GuestDataMatch, error) {
	issues, raw, err := e.st.PlanGuestData(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var matches []GuestDataMatch
	if err := json.Unmarshal(raw, &matches); err != nil {
		return nil, nil, err
	}
	return issues, matches, nil
}
