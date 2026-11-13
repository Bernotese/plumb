package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/trust"
)

// OriginPinRefresh: POST /images/{name}/refresh or the build schedule
// changes only the pin of an Image (D-335, D-346).
const OriginPinRefresh = "pin_refresh"

// PinSource resolves the newest pin of a base image; *trust.Client is the
// real one. Tests give a fake.
type PinSource interface {
	Resolve(ctx context.Context, src trust.Source, imageURL string) (trust.Result, error)
}

// PinRefresh is one pin change of a pin_refresh Plan (api PinRefresh). It
// is stored in the proposal of the Plan; no client field can set it.
type PinRefresh struct {
	Image          string     `json:"image"`
	Source         string     `json:"source"`
	Signed         bool       `json:"signed"`
	KeyFingerprint string     `json:"key_fingerprint,omitempty"`
	File           string     `json:"file"`
	Algorithm      string     `json:"algorithm"`
	Old            string     `json:"old,omitempty"`
	New            string     `json:"new"`
	UsedBefore     bool       `json:"used_before"`
	SignedAt       *time.Time `json:"signed_at,omitempty"`
	LastSignedAt   *time.Time `json:"last_signed_at,omitempty"`
}

// Verified reports whether the pin change may be approved without a person
// (D-335): a signed list, a signature strictly newer than the last list
// this Image accepted from the source (none stored: a person approves),
// and a pin that the Image never used.
func (p PinRefresh) Verified() bool {
	return p.Signed && p.SignedAt != nil && p.LastSignedAt != nil && p.SignedAt.After(*p.LastSignedAt) && !p.UsedBefore
}

// ErrNoVerify: the Image has no verify part, so it has no pin source.
var ErrNoVerify = errors.New("plan: the Image has no verify")

// PinSourceError wraps a failure of the vendor list (502 pin-source-failed).
type PinSourceError struct{ Err error }

func (e *PinSourceError) Error() string { return "checksum source failed: " + e.Err.Error() }
func (e *PinSourceError) Unwrap() error { return e.Err }

// Detail names the failed step for a client: the text of the trust error
// kind, never fetched data.
func (e *PinSourceError) Detail() string {
	for _, known := range []error{trust.ErrSource, trust.ErrFetch, trust.ErrTooLarge, trust.ErrSignature, trust.ErrNoEntry, trust.ErrDuplicate, trust.ErrFormat} {
		if errors.Is(e.Err, known) {
			return known.Error()
		}
	}
	return "the checksum list could not be fetched or checked"
}

func (e *Engine) pinSource() PinSource {
	if e.cfg.Pins != nil {
		return e.cfg.Pins
	}
	return &trust.Client{}
}

// ResolvePin finds the newest pin of an Image at the head and compares it
// with the history of the Image. It changes nothing.
func (e *Engine) ResolvePin(ctx context.Context, image string) (PinRefresh, error) {
	_, base, err := e.base(ctx)
	if err != nil {
		return PinRefresh{}, err
	}
	info, ok := base.ImageInfo(image)
	if !ok {
		return PinRefresh{}, fmt.Errorf("plan: Image %q: %w", image, store.ErrNotFound)
	}
	if info.Verify == "" {
		return PinRefresh{}, &ProposalError{Msg: fmt.Sprintf("Image %q has no verify part; set verify.source first", image), Err: ErrNoVerify}
	}
	res, err := e.pinSource().Resolve(ctx, trust.Source(info.Verify), info.URL)
	if err != nil {
		return PinRefresh{}, &PinSourceError{Err: err}
	}
	out := PinRefresh{Image: image, Source: info.Verify, Signed: res.Signed, KeyFingerprint: res.Fingerprint,
		File: res.File, Algorithm: res.Algorithm, Old: info.Pin(), New: res.Sum}
	if res.Signed {
		t := res.SignedAt.UTC()
		out.SignedAt = &t
	}
	if out.LastSignedAt, err = e.lastSignedAt(ctx, image, info.Verify); err != nil {
		return PinRefresh{}, err
	}
	if out.New != out.Old {
		used, err := e.st.ImagePinsUsed(ctx, image)
		if err != nil {
			return PinRefresh{}, err
		}
		out.UsedBefore = used[out.New]
	} else {
		out.UsedBefore = true // the current pin: nothing to approve
	}
	return out, nil
}

// lastSignedAt is the newest signature time of an approved pin_refresh of
// the Image from the source; nil when there is none.
func (e *Engine) lastSignedAt(ctx context.Context, image, source string) (*time.Time, error) {
	plans, err := e.st.ApprovedPlans(ctx, OriginPinRefresh)
	if err != nil {
		return nil, err
	}
	var last *time.Time
	for _, p := range plans {
		var prop Proposal
		if json.Unmarshal(p.Proposal, &prop) != nil {
			continue
		}
		for _, pin := range prop.Pins {
			if pin.Image == image && pin.Source == source && pin.SignedAt != nil && (last == nil || pin.SignedAt.After(*last)) {
				t := *pin.SignedAt
				last = &t
			}
		}
	}
	return last, nil
}

// RefreshPin makes a Plan of origin pin_refresh that changes only the pin
// of the Image to the newest one of its source. It returns the pin and a
// zero Plan when the pin is current (no Plan).
func (e *Engine) RefreshPin(ctx context.Context, image, author string) (store.Plan, PinRefresh, error) {
	pin, err := e.ResolvePin(ctx, image)
	if err != nil {
		return store.Plan{}, PinRefresh{}, err
	}
	if pin.New == pin.Old {
		return store.Plan{}, pin, nil
	}
	head, err := e.st.Head(ctx)
	if err != nil {
		return store.Plan{}, PinRefresh{}, err
	}
	row, err := e.st.RevisionDoc(ctx, head.ID, state.KindImage, image)
	if err != nil {
		return store.Plan{}, PinRefresh{}, err
	}
	doc, err := withPin(row.Body, pin)
	if err != nil {
		return store.Plan{}, PinRefresh{}, err
	}
	reason := fmt.Sprintf("Refresh the base pin of %s from %s.", image, pin.Source)
	p, err := e.Propose(ctx, Input{Origin: OriginPinRefresh, Author: author, Reason: reason, Base: head.ID,
		Proposal: Proposal{Put: []state.Doc{doc}, Pins: []PinRefresh{pin}}})
	return p, pin, err
}

// withPin returns the Image document with only the pin changed.
func withPin(body []byte, pin PinRefresh) (state.Doc, error) {
	var d struct {
		Kind string         `json:"kind"`
		Name string         `json:"name"`
		Spec map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return state.Doc{}, err
	}
	delete(d.Spec, "sha256")
	delete(d.Spec, "sha512")
	d.Spec[pin.Algorithm] = pin.New
	spec, err := json.Marshal(d.Spec)
	if err != nil {
		return state.Doc{}, err
	}
	return state.Doc{Kind: d.Kind, Name: d.Name, Spec: spec}, nil
}

// scheduledRefresh runs before a scheduled build of an Image with verify
// (D-346). It returns true when an approved pin_refresh Plan builds the
// Image now (its pin change changes the recipe, so the Plan holds the
// build), else a note for the scheduled build with the old pin: a refresh
// that waits for a person, or a failed source. A failure never stops the
// scheduled build.
func (e *Engine) scheduledRefresh(ctx context.Context, image string) (bool, string) {
	pin, err := e.ResolvePin(ctx, image)
	if err != nil {
		e.log.Warn("scheduled pin refresh", "image", image, "err", err)
		return false, fmt.Sprintf("the newest base pin could not be checked (%v); built from the current pin", err)
	}
	if pin.New == pin.Old {
		return false, ""
	}
	// A refresh to the same pin that already waits for a person is not
	// made again at every slot.
	if pending, err := e.st.PendingPlans(ctx, OriginPinRefresh); err == nil {
		for _, p := range pending {
			var prop Proposal
			if json.Unmarshal(p.Proposal, &prop) == nil && len(prop.Pins) == 1 && prop.Pins[0].Image == image && prop.Pins[0].New == pin.New {
				return false, fmt.Sprintf("a newer base waits for approval (Plan %d); built from the current pin", p.ID)
			}
		}
	}
	p, _, err := e.RefreshPin(ctx, image, PrincipalSystem)
	if err != nil {
		e.log.Warn("scheduled pin refresh", "image", image, "err", err)
		return false, fmt.Sprintf("the newest base pin could not be proposed (%v); built from the current pin", err)
	}
	if p.ID == 0 {
		return false, ""
	}
	if !pin.Verified() || len(p.HumanOnly) > 0 {
		return false, fmt.Sprintf("a newer base waits for approval (Plan %d); built from the current pin", p.ID)
	}
	e.mu.Lock()
	if e.schedPlans == nil {
		e.schedPlans = map[int64]bool{}
	}
	e.schedPlans[p.ID] = true
	e.mu.Unlock()
	if _, err := e.Approve(ctx, p.ID, PrincipalSystem, e.pinSystemCheck(ctx)); err != nil {
		e.scheduledDone(p.ID)
		e.markStale(context.WithoutCancel(ctx), p.ID)
		e.log.Warn("scheduled pin refresh: approve", "image", image, "plan", p.ID, "err", err)
		return false, fmt.Sprintf("the newer base (Plan %d) could not be approved (%v); built from the current pin", p.ID, err)
	}
	return true, ""
}

// pinSystemCheck is the approval rule of system for a pin_refresh Plan
// (D-335, D-346): author system, no human-only reason, exactly one pin
// change that is verified, a proposal that puts only that Image, a
// document that differs from the base only in the pin, no Target, and
// image builds only of that Image.
func (e *Engine) pinSystemCheck(ctx context.Context) CheckFunc {
	return func(p store.Plan) error {
		if p.Author != PrincipalSystem || p.Origin != OriginPinRefresh || len(p.HumanOnly) > 0 {
			return ErrNotSystemPlan
		}
		var prop Proposal
		if err := json.Unmarshal(p.Proposal, &prop); err != nil || len(prop.Pins) != 1 || !prop.Pins[0].Verified() ||
			len(prop.Put) != 1 || len(prop.Delete) > 0 || len(prop.Targets) > 0 || len(prop.Reenroll) > 0 {
			return ErrNotSystemPlan
		}
		pin, put := prop.Pins[0], prop.Put[0]
		if put.Kind != state.KindImage || put.Name != pin.Image {
			return ErrNotSystemPlan
		}
		var targets []TargetPlan
		if err := json.Unmarshal(p.Changes, &targets); err != nil || len(targets) > 0 {
			return ErrNotSystemPlan
		}
		images, err := imagePlans(p)
		if err != nil {
			return ErrNotSystemPlan
		}
		for _, ip := range images {
			if ip.Image != pin.Image {
				return ErrNotSystemPlan
			}
		}
		row, err := e.st.RevisionDoc(ctx, p.BaseRevision, state.KindImage, pin.Image)
		if err != nil {
			return ErrNotSystemPlan
		}
		if !onlyPinDiffers(row.Body, put, pin) {
			return ErrNotSystemPlan
		}
		return nil
	}
}

// onlyPinDiffers reports whether put is the base document with the new pin
// and nothing else changed.
func onlyPinDiffers(base []byte, put state.Doc, pin PinRefresh) bool {
	want, err := withPin(base, pin)
	if err != nil || want.Kind != put.Kind || want.Name != put.Name {
		return false
	}
	var a, b any
	if json.Unmarshal(want.Spec, &a) != nil || json.Unmarshal(put.Spec, &b) != nil {
		return false
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
