package api

// T-152 part B: POST /images/{name}/refresh and the scheduled refresh
// (D-335, D-346).

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/trust"
)

const ubuntuURL = "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img"

// fakePins is a vendor list that a test sets.
type fakePins struct {
	mu    sync.Mutex
	res   trust.Result
	err   error
	calls int
}

func (f *fakePins) set(sum string, signedAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.res = trust.Result{Source: trust.Ubuntu, Signed: true, Fingerprint: trust.Fingerprints[trust.Ubuntu], SignedAt: signedAt,
		File: "ubuntu-26.04-server-cloudimg-amd64.img", Algorithm: "sha256", Sum: sum}
	f.err = nil
}

func (f *fakePins) Resolve(_ context.Context, src trust.Source, url string) (trust.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := trust.CheckURL(src, url); err != nil {
		return trust.Result{}, err
	}
	return f.res, f.err
}

func (f *fakePins) opt() func(*plan.Config) { return func(c *plan.Config) { c.Pins = f } }

func verifiedImage(name, sha string) state.Doc {
	return doc(state.KindImage, name, `{"type":"vm","url":"`+ubuntuURL+`","sha256":"`+sha+`","verify":{"source":"ubuntu-cloud"}}`)
}

type pinPlan struct {
	ID        int64    `json:"id"`
	Origin    string   `json:"origin"`
	HumanOnly []string `json:"human_only"`
}

func TestRefreshPinStockImage(t *testing.T) {
	pins := &fakePins{}
	e := newEnv(t, false, pins.opt())
	adm := e.admin()
	old, newer := strings.Repeat("1", 64), strings.Repeat("2", 64)
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": []state.Doc{verifiedImage("ubu", old)}})

	// The current pin: 200 unchanged, no Plan.
	t1 := time.Date(2026, 10, 20, 3, 55, 10, 0, time.UTC)
	pins.set(old, t1)
	code, body := adm.do("POST", "/api/v1/images/ubu/refresh", nil)
	if code != 200 || !strings.Contains(string(body), `"status":"unchanged"`) || !strings.Contains(string(body), `"last_signed_at":null`) {
		t.Fatalf("unchanged: %d %s", code, body)
	}

	// A newer pin: a pin_refresh Plan that changes only the pin. The first
	// refresh has no accepted signature time: a person approves it.
	pins.set(newer, t1.Add(24*time.Hour))
	code, body = adm.do("POST", "/api/v1/images/ubu/refresh", nil)
	var p pinPlan
	json.Unmarshal(body, &p)
	if code != 201 || p.Origin != plan.OriginPinRefresh {
		t.Fatalf("refresh: %d %s", code, body)
	}
	stored, err := e.st.GetPlan(e.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var prop plan.Proposal
	if err := json.Unmarshal(stored.Proposal, &prop); err != nil || len(prop.Pins) != 1 || len(prop.Put) != 1 {
		t.Fatalf("proposal %s", stored.Proposal)
	}
	pin := prop.Pins[0]
	if pin.Old != old || pin.New != newer || pin.LastSignedAt != nil || pin.Verified() || pin.UsedBefore {
		t.Fatalf("pin %+v", pin)
	}
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyUnverifiedPin) {
		t.Fatalf("first refresh not human-only: %v", p.HumanOnly)
	}
	if !strings.Contains(string(body), `"pins":[{`) || !strings.Contains(string(body), `"last_signed_at":null`) {
		t.Fatalf("plan view without pins: %s", body)
	}
	if code, b := e.token(TierApply, "disruptive").do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 || problemType(b) != "human-required" {
		t.Fatalf("apply token approves a first refresh: %d %s", code, b)
	}
	want, _ := json.Marshal(map[string]any{"type": "vm", "url": ubuntuURL, "sha256": newer, "verify": map[string]any{"source": "ubuntu-cloud"}})
	var a, b any
	json.Unmarshal(want, &a)
	json.Unmarshal(prop.Put[0].Spec, &b)
	if ja, jb := mustJSON(a), mustJSON(b); ja != jb {
		t.Fatalf("put spec %s, want %s", jb, ja)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()

	// The next list is newer and has a new pin: verified now.
	newest := strings.Repeat("3", 64)
	pins.set(newest, t1.Add(48*time.Hour))
	got, err := e.eng.ResolvePin(e.ctx, "ubu")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSignedAt == nil || !got.LastSignedAt.Equal(t1.Add(24*time.Hour)) || !got.Verified() {
		t.Fatalf("second refresh %+v", got)
	}
	// The same signature time again (a replay): not verified.
	pins.set(newest, t1.Add(24*time.Hour))
	if got, _ := e.eng.ResolvePin(e.ctx, "ubu"); got.Verified() {
		t.Fatalf("replayed list verified: %+v", got)
	}
	// Back to the old pin with a newer list (a downgrade): used before.
	pins.set(old, t1.Add(72*time.Hour))
	if got, _ := e.eng.ResolvePin(e.ctx, "ubu"); !got.UsedBefore || got.Verified() {
		t.Fatalf("downgrade verified: %+v", got)
	}
}

func TestRefreshPinErrors(t *testing.T) {
	pins := &fakePins{}
	e := newEnv(t, false, pins.opt())
	adm := e.admin()
	e.runPlanAnyStatus(adm, map[string]any{"reason": "images", "put": []state.Doc{
		imageDoc("plain", "a"), verifiedImage("ubu", strings.Repeat("1", 64))}})
	if code, body := adm.do("POST", "/api/v1/images/plain/refresh", nil); code != 422 {
		t.Errorf("no verify: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/images/nope/refresh", nil); code != 404 {
		t.Errorf("unknown image: %d %s", code, body)
	}
	pins.mu.Lock()
	pins.err = trust.ErrSignature
	pins.mu.Unlock()
	code, body := adm.do("POST", "/api/v1/images/ubu/refresh", nil)
	if code != 502 || problemType(body) != "pin-source-failed" || !strings.Contains(string(body), "not valid for the pinned key") {
		t.Errorf("source failure: %d %s", code, body)
	}
	// A plan token may ask for a refresh (the approval rules decide later).
	tok := e.token(TierPlan, "")
	pins.set(strings.Repeat("2", 64), time.Now())
	if code, body := tok.do("POST", "/api/v1/images/ubu/refresh", nil); code != 201 {
		t.Errorf("plan token: %d %s", code, body)
	}
}

// D-346: a scheduled build of an Image with verify asks the source first.
// A refresh that a person must approve leaves the build on the old pin and
// says so in the reason; a second slot does not make the refresh again.
func TestScheduledRefreshWaitsForPerson(t *testing.T) {
	pins := &fakePins{}
	c := &clock{}
	c.set("2026-11-11 10:00")
	e := newEnv(t, false, withBuilds, c.opt(), pins.opt())
	e.buildAgents(nil)
	adm := e.admin()
	docs := scheduledImage(`{"every":"day","at":"03:00"}`)
	docs[1] = doc(state.KindImage, "web", `{"type":"vm","url":"`+ubuntuURL+`","sha256":"`+builtSHA+
		`","verify":{"source":"ubuntu-cloud"},"build":{"blocks":[{"use":"banner"}],"guest":{"disk":10,"bridge":"vmbr0"}},"schedule":{"every":"day","at":"03:00"}}`)
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": docs})
	e.tick()
	pins.set(strings.Repeat("b", 64), time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC))

	c.set("2026-11-12 03:01")
	e.tick()
	builds := e.schedulePlans()
	if len(builds) != 1 || !strings.Contains(builds[0].Reason, "a newer base waits for approval (Plan ") {
		t.Fatalf("scheduled builds: %+v", builds)
	}
	pending, err := e.st.PendingPlans(e.ctx, plan.OriginPinRefresh)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending refreshes: %d %v", len(pending), err)
	}
	c.set("2026-11-13 03:01")
	e.tick()
	if pending, _ := e.st.PendingPlans(e.ctx, plan.OriginPinRefresh); len(pending) != 1 {
		t.Fatalf("a second refresh Plan: %d", len(pending))
	}
	if n := len(e.schedulePlans()); n != 2 {
		t.Fatalf("scheduled builds after two slots: %d", n)
	}

	// A failed source never stops the scheduled build.
	pins.mu.Lock()
	pins.err = trust.ErrFetch
	pins.mu.Unlock()
	c.set("2026-11-14 03:01")
	e.tick()
	builds = e.schedulePlans()
	last := builds[0]
	for _, b := range builds {
		if b.ID > last.ID {
			last = b
		}
	}
	if len(builds) != 3 || !strings.Contains(last.Reason, "could not be checked") {
		t.Fatalf("build after a source failure: %d builds, last %q", len(builds), last.Reason)
	}
}

func TestPinVerified(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	for _, c := range []struct {
		name string
		p    plan.PinRefresh
		want bool
	}{
		{"newer signed", plan.PinRefresh{Signed: true, SignedAt: &t2, LastSignedAt: &t1}, true},
		{"first refresh", plan.PinRefresh{Signed: true, SignedAt: &t2}, false},
		{"same time", plan.PinRefresh{Signed: true, SignedAt: &t1, LastSignedAt: &t1}, false},
		{"older", plan.PinRefresh{Signed: true, SignedAt: &t1, LastSignedAt: &t2}, false},
		{"used before", plan.PinRefresh{Signed: true, SignedAt: &t2, LastSignedAt: &t1, UsedBefore: true}, false},
		{"unsigned", plan.PinRefresh{SignedAt: &t2, LastSignedAt: &t1}, false},
	} {
		if got := c.p.Verified(); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// D-335, D-346: after a person accepted one refresh, a newer signed list
// for a built Image is approved by system at the slot; its recipe change
// is the build (no scheduled build with the old pin). The same refresh by
// the API has no image-recipe reason, so an apply token may approve it.
func TestScheduledRefreshBySystem(t *testing.T) {
	pins := &fakePins{}
	c := &clock{}
	c.set("2026-11-11 10:00")
	e := newEnv(t, false, withBuilds, c.opt(), pins.opt())
	e.buildAgents(nil)
	adm := e.admin()
	docs := scheduledImage(`{"every":"day","at":"03:00"}`)
	docs[1] = doc(state.KindImage, "web", `{"type":"vm","url":"`+ubuntuURL+`","sha256":"`+builtSHA+
		`","verify":{"source":"ubuntu-cloud"},"build":{"blocks":[{"use":"banner"}],"guest":{"disk":10,"bridge":"vmbr0"}},"schedule":{"every":"day","at":"03:00"}}`)
	e.runPlanAnyStatus(adm, map[string]any{"reason": "image", "put": docs})
	e.tick()

	// The first refresh: a person approves it (no stored signature time).
	t1 := time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC)
	pins.set(strings.Repeat("b", 64), t1)
	code, body := adm.do("POST", "/api/v1/images/web/refresh", nil)
	var first pinPlan
	json.Unmarshal(body, &first)
	if code != 201 || !slices.Contains(first.HumanOnly, plan.HumanOnlyUnverifiedPin) || !slices.Contains(first.HumanOnly, plan.HumanOnlyImageRecipe) {
		t.Fatalf("first refresh: %d %s", code, body)
	}
	if code, b := adm.do("POST", "/api/v1/plans/"+itoa(first.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve first: %d %s", code, b)
	}
	e.eng.Wait()

	// A newer signed list with a new pin: verified. Through the API it has
	// no human-only reason.
	pins.set(strings.Repeat("c", 64), t1.Add(24*time.Hour))
	code, body = adm.do("POST", "/api/v1/images/web/refresh", nil)
	var second pinPlan
	json.Unmarshal(body, &second)
	if code != 201 || len(second.HumanOnly) != 0 {
		t.Fatalf("verified refresh: %d %s", code, body)
	}
	if code, b := adm.do("DELETE", "/api/v1/plans/"+itoa(second.ID), nil); code != 204 {
		t.Fatalf("drop: %d %s", code, b)
	}

	// At the slot: system approves the refresh, which builds; no
	// scheduled build Plan with the old pin.
	before := len(e.schedulePlans())
	c.set("2026-11-12 03:01")
	e.tick()
	if n := len(e.schedulePlans()); n != before {
		t.Fatalf("a scheduled build with the old pin ran too: %d → %d", before, n)
	}
	approved, err := e.st.ApprovedPlans(e.ctx, plan.OriginPinRefresh)
	if err != nil || len(approved) != 2 || approved[1].ApprovedBy != plan.PrincipalSystem {
		t.Fatalf("system refresh: %d %v %+v", len(approved), err, approved)
	}
	if !strings.Contains(string(approved[1].Images), `"build"`) {
		t.Fatalf("the system refresh holds no build: %s", approved[1].Images)
	}
	_, img := e.headImage("web")
	if !strings.Contains(img, strings.Repeat("c", 64)) {
		t.Fatalf("head pin: %s", img)
	}
}

// headImage returns the head revision and the Image document text.
func (e *env) headImage(name string) (int64, string) {
	e.t.Helper()
	head, err := e.st.Head(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	row, err := e.st.RevisionDoc(e.ctx, head.ID, state.KindImage, name)
	if err != nil {
		e.t.Fatal(err)
	}
	return head.ID, string(row.Body)
}
