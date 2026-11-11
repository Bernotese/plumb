package api

// T-055 (D-188, D-189): no Image binding. Every guest create fetches the
// image again; the create-only attribute image is the Image name, so a
// re-made or changed template never touches a guest that exists.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func imageDoc(name, sha string) state.Doc {
	return doc(state.KindImage, name, `{"type":"vm","url":"https://images.invalid/u.img","sha256":"`+strings.Repeat(sha, 64)+`"}`)
}

func targetWith(name, image string, cores int) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"`+image+`","guest":{"cores":`+itoa(int64(cores))+`,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
}

// runPlan plans the request as c, approves it, waits and returns the
// status of the Run.
func (e *env) runPlan(c *client, req map[string]any) string {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", req)
	if code != 201 {
		e.t.Fatalf("%v: plan %d %s", req["reason"], code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := c.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		e.t.Fatalf("%v: approve %d %s", req["reason"], code, body)
	}
	e.eng.Wait()
	run, err := e.st.RunOfPlan(e.ctx, p.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if run.Status != "succeeded" {
		steps, _ := e.st.Steps(e.ctx, run.ID)
		e.t.Errorf("%v: run %s, steps %+v", req["reason"], run.Status, steps)
	}
	return run.Status
}

// noImageBinding fails when any Image binding is in the store.
func (e *env) noImageBinding() {
	e.t.Helper()
	if b, err := e.st.Bindings(e.ctx, state.KindImage); err != nil || len(b) != 0 {
		e.t.Errorf("Image bindings: %+v %v", b, err)
	}
}

// noGuestDrift runs a guest poll round for target (its digest is reset, so
// the round compares) and fails on drift of guest:config.
func (e *env) noGuestDrift(target string) {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
	if err != nil {
		e.t.Fatal(err)
	}
	b.Digest = ""
	if err := e.st.PutBinding(e.ctx, b); err != nil {
		e.t.Fatal(err)
	}
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	if iss, err := e.st.OpenIssue(e.ctx, target, wire.GuestConfigID); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("%s: guest drift issue %+v %v", target, iss, err)
	}
}

// ubuntuRef is the ref of the ubuntu image in the fake (FetchImage is
// idempotent; the call is counted).
func (e *env) ubuntuRef(sha string) driver.ImageRef {
	e.t.Helper()
	ref, err := e.drv.FetchImage(context.Background(), driver.ImageSpec{Name: "ubuntu", Kind: wire.KindVM,
		URL: "https://images.invalid/u.img", SHA256: strings.Repeat(sha, 64)})
	if err != nil {
		e.t.Fatal(err)
	}
	return ref
}

// T-015 run 3: Plan A creates an Image and a Target, Plan B deletes both,
// the template is destroyed outside Plumb, Plan C creates both again with
// the same Image name. The Run succeeds.
func TestImageDeleteAndCreateAgain(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.noImageBinding()
	e.runPlan(adm, map[string]any{"reason": "remove", "delete": []map[string]string{
		{"kind": "Target", "name": "web1"}, {"kind": "Image", "name": "ubuntu"}}})
	e.drv.DestroyTemplate(e.ubuntuRef("0")) // the test cleanup of T-015: qm destroy as root
	e.agent("web2")
	before := e.drv.Calls(fake.OpFetchImage)
	e.runPlan(adm, map[string]any{"reason": "again", "put": []state.Doc{imageDoc("ubuntu", "0"), targetWith("web2", "ubuntu", 2)}})
	if n := e.drv.Calls(fake.OpFetchImage) - before; n != 1 {
		t.Errorf("FetchImage calls: %d, want 1", n)
	}
	e.noImageBinding()
}

// The template is destroyed after the first guest: the second Target with
// the same Image fetches it again. The first guest shows no drift, and an
// unrelated Plan on it works.
func TestImageDestroyedOutside(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.drv.DestroyTemplate(e.ubuntuRef("0"))
	e.agent("web2")
	before := e.drv.Calls(fake.OpFetchImage)
	e.runPlan(adm, map[string]any{"reason": "second", "put": []state.Doc{targetWith("web2", "ubuntu", 2)}})
	if n := e.drv.Calls(fake.OpFetchImage) - before; n != 1 || e.drv.Images() != 1 {
		t.Errorf("FetchImage calls %d, images %d; want 1, 1", n, e.drv.Images())
	}
	e.noGuestDrift("web1")
	e.runPlan(adm, map[string]any{"reason": "cores", "put": []state.Doc{targetWith("web1", "ubuntu", 4)}})
	// allow_restart is false: the new value waits for a cold start (D-194).
	st, err := e.drv.Observe(e.ctx, e.guestID("web1"))
	if err != nil || st.Next().Cores != 4 || st.Spec.ImageName != "ubuntu" {
		t.Errorf("web1 after cores: %+v %v", st, err)
	}
}

// A new checksum: the new guest gets a new template; the old guest has no
// create-only error and no drift.
func TestImageChecksumChange(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	old := e.ubuntuRef("0")
	e.agent("web2")
	e.runPlan(adm, map[string]any{"reason": "new checksum", "put": []state.Doc{imageDoc("ubuntu", "1"), targetWith("web2", "ubuntu", 2)}})
	if e.drv.Images() != 2 || e.ubuntuRef("1") == old {
		t.Errorf("images %d: want a second template for the new checksum", e.drv.Images())
	}
	e.noGuestDrift("web1")
	e.runPlan(adm, map[string]any{"reason": "cores", "put": []state.Doc{targetWith("web1", "ubuntu", 3)}})
	e.noImageBinding()
}

// A Target whose spec.image names another Image is still refused, and the
// text names both Images.
func TestImageChangeOfTargetRefused(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "other image",
		"put": []state.Doc{imageDoc("debian", "2"), targetWith("web1", "debian", 2)}})
	if code == 201 || !strings.Contains(string(body), `\"image\" can only be set when the guest is created`) ||
		!strings.Contains(string(body), "the guest has ubuntu") || !strings.Contains(string(body), "the proposal wants debian") {
		t.Errorf("image change: %d %s", code, body)
	}
}

// Two Targets with the same Image in one Plan, the image exists: both
// guest steps succeed, one image, two fetches.
func TestImageTwoTargetsOnePlan(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.agent("web2")
	e.agent("web3")
	before := e.drv.Calls(fake.OpFetchImage)
	e.runPlan(adm, map[string]any{"reason": "two", "put": []state.Doc{targetWith("web2", "ubuntu", 2), targetWith("web3", "ubuntu", 2)}})
	if n := e.drv.Calls(fake.OpFetchImage) - before; n != 2 || e.drv.Images() != 1 {
		t.Errorf("FetchImage calls %d, images %d; want 2, 1", n, e.drv.Images())
	}
	for _, tg := range []string{"web2", "web3"} {
		if st, err := e.drv.Observe(e.ctx, e.guestID(tg)); err != nil || st.Spec.ImageName != "ubuntu" || st.Spec.Image != "" {
			t.Errorf("%s: %+v %v", tg, st.Spec, err)
		}
	}
}

func (e *env) guestID(target string) driver.GuestID {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
	if err != nil {
		e.t.Fatal(err)
	}
	return driver.GuestID(b.Ref)
}

// T-059: an Image with spec.build passes the save checks, but no Run builds
// before T-062, so a proposal with one is refused (422), not planned.
func TestBuiltImageRefusedUntilT062(t *testing.T) {
	e := newEnv(t, false)
	built := doc(state.KindImage, "web", `{"type":"vm","url":"https://images.invalid/u.img","sha256":"`+strings.Repeat("0", 64)+
		`","build":{"guest":{"disk":10,"bridge":"vmbr0"}}}`)
	code, body := e.admin().do("POST", "/api/v1/plans", map[string]any{"reason": "build", "put": []state.Doc{built}})
	if code != 422 || !strings.Contains(string(body), "not available yet (T-062 part B)") {
		t.Fatalf("propose a built Image: %d %s", code, body)
	}
}
