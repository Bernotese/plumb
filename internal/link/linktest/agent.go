// Package linktest has an in-process fake agent for controller tests. It
// speaks the real agent protocol (internal/wire) to a link.Handler, and
// keeps the "guest" in memory: resource id → attributes. It stands in for
// the real agent (internal/agent, T-018) until that exists, and later for
// tests that need a guest without a file system.
package linktest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// Agent is a fake agent. Create it with New and start Run in a goroutine.
type Agent struct {
	handler    http.Handler
	token      func() (string, bool)
	remoteAddr string

	// LeakSensitive makes the agent report clear values of sensitive
	// attributes, like a buggy agent. The controller must redact them.
	LeakSensitive bool

	bodies    [][]byte               // raw body of every command, in order
	onCommand func(wire.CommandType) // see OnCommand
	mu        sync.Mutex
	cred      string
	enrolls   int
	guest     map[string]map[string]any // what is "on the guest"
	watch     []wire.Resource
	failing   map[string]string // resource id → apply error
	commands  []wire.CommandType
	addrs     []string   // the address list of the Hello; nil: none (an old agent)
	hello2    wire.Hello // binary_sha256, capabilities and reverted of the Hello (T-092)

	// seal (D-205, D-207), simulated like the real agent (T-060).
	build     bool   // the build mark of the enrollment
	watched   bool   // a watch arrived once; never taken back
	off       bool   // the guest powered off after seal: Run ends
	onSeal    func() // see OnSeal
	onUpdate  func(wire.UpdateBody) (wire.Item, *wire.Hello) // see OnUpdate
	sealFail  string // a step that fails; "" for none
	sealStuck bool   // the guest does not power off after seal
	noMark    bool   // the agent does not take the build mark
}

// NoBuildMark makes the agent ignore the build mark, like an agent that
// got a normal enrollment: it refuses seal.
func (a *Agent) NoBuildMark(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.noMark = on
}

// OnUpdate makes the agent answer the update command (T-092): fn returns
// the result Item and, when not nil, the Hello of the agent after its
// restart (its BinarySHA256, Capabilities and Reverted become the agent's,
// and a hello is sent after the result). Without a hook the agent answers
// like an agent before slice 3: "unknown command type".
func (a *Agent) OnUpdate(fn func(wire.UpdateBody) (wire.Item, *wire.Hello)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUpdate = fn
}

// OnSeal sets a hook that runs after a seal result was taken, where the
// real agent powers the guest off: a test stops the fake guest there.
func (a *Agent) OnSeal(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onSeal = fn
}

// FailSeal makes the seal step fail ("" clears); the later steps are
// skipped and the guest does not power off.
func (a *Agent) FailSeal(step string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealFail = step
}

// SealStuck makes the guest stay on after a good seal (no power off).
func (a *Agent) SealStuck(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealStuck = on
}

// Off reports whether the agent powered its guest off after seal.
func (a *Agent) Off() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.off
}

// SetAddrs sets the address list that the next Hello holds. nil means no
// list, like an agent from before T-069.
func (a *Agent) SetAddrs(list []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addrs = slices.Clone(list)
}

// SendAddrs sets the address list and sends it as an addrs report. It
// returns the HTTP status.
func (a *Agent) SendAddrs(ctx context.Context, list []string) int {
	a.SetAddrs(list)
	return a.Report(ctx, wire.Report{Type: wire.ReportAddrs, Addrs: list})
}

// SetHello sets the slice 3 fields of the next Hellos: BinarySHA256,
// Capabilities and Reverted of h (T-092). The rest of h is ignored.
func (a *Agent) SetHello(h wire.Hello) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hello2 = h
}

// hello is the Hello of the agent.
func (a *Agent) hello() *wire.Hello {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &wire.Hello{Version: "test", Hostname: "fake", OS: "fake 1", Addrs: slices.Clone(a.addrs),
		BinarySHA256: a.hello2.BinarySHA256, Capabilities: a.hello2.Capabilities, Reverted: a.hello2.Reverted}
}

// New returns an agent that talks to h. token reads the token source, as
// the real agent reads the SMBIOS serial; ok is false while there is none.
func New(h http.Handler, token func() (string, bool)) *Agent {
	return &Agent{handler: h, token: token, remoteAddr: "10.99.0.2:4000",
		guest: map[string]map[string]any{}, failing: map[string]string{}}
}

// SetHandler points the agent at another controller, as after a
// controller restart. The credential stays.
func (a *Agent) SetHandler(h http.Handler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = h
}

// Report sends a raw report with the agent's credential, like a buggy or
// hostile agent. It returns the HTTP status.
func (a *Agent) Report(ctx context.Context, rep wire.Report) int {
	a.mu.Lock()
	cred := a.cred
	a.mu.Unlock()
	code, _, _ := a.do(ctx, "POST", wire.PathReport, cred, rep)
	return code
}

// Credential returns the agent's current credential ("" before the
// enrollment), so a test can play an attacker with a copy of it.
func (a *Agent) Credential() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cred
}

// Enrolls returns how often the agent enrolled.
func (a *Agent) Enrolls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enrolls
}

// Commands returns the types of the commands the agent received.
func (a *Agent) Commands() []wire.CommandType {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.commands)
}

// Guest returns a copy of one resource on the guest, or nil.
func (a *Agent) Guest(id string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r, ok := a.guest[id]; ok {
		return maps.Clone(r)
	}
	return nil
}

// FailApply makes every apply of the resource fail with msg ("" clears).
func (a *Agent) FailApply(id, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msg == "" {
		delete(a.failing, id)
	} else {
		a.failing[id] = msg
	}
}

// Change changes a resource on the guest from outside Plumb (nil
// removes it). When the resource is watched, the agent reports drift, or
// "unchanged" when the guest matches again.
func (a *Agent) Change(ctx context.Context, id string, attrs map[string]any) error {
	a.mu.Lock()
	if attrs == nil {
		delete(a.guest, id)
	} else {
		a.guest[id] = maps.Clone(attrs)
	}
	var item *wire.Item
	for _, r := range a.watch {
		if r.ID == id {
			it := a.observe(r)
			item = &it
		}
	}
	a.mu.Unlock()
	if item == nil {
		return nil
	}
	return a.report(ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{*item}})
}

// Run enrolls, polls and answers commands until ctx ends. On a 401 it
// drops its credential and enrolls again with the current token.
func (a *Agent) Run(ctx context.Context) {
	backoff := 20 * time.Millisecond
	for ctx.Err() == nil {
		a.mu.Lock()
		cred, off := a.cred, a.off
		a.mu.Unlock()
		if off {
			return // the guest is off
		}
		if cred == "" {
			// Like the real agent (T-018): back off while enrollment fails,
			// so the enroll rate limit of link is not hit.
			if a.enroll(ctx) {
				backoff = 20 * time.Millisecond
			} else {
				sleep(ctx, backoff)
				backoff = min(backoff*2, 400*time.Millisecond)
			}
			continue
		}
		cmd, code, err := a.poll(ctx, cred)
		switch {
		case code == http.StatusUnauthorized:
			a.mu.Lock()
			a.cred = ""
			a.mu.Unlock()
		case err != nil || code == http.StatusTooManyRequests:
			sleep(ctx, 20*time.Millisecond)
		case cmd != nil:
			a.handle(ctx, *cmd)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (a *Agent) do(ctx context.Context, method, path, cred string, body any) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, r)
	req.RemoteAddr = a.remoteAddr
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	a.mu.Lock()
	h := a.handler
	a.mu.Unlock()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), ctx.Err()
}

func (a *Agent) enroll(ctx context.Context) bool {
	tok, ok := a.token()
	if !ok {
		return false
	}
	code, body, err := a.do(ctx, "POST", wire.PathEnroll, "", wire.EnrollRequest{Token: tok, Hostname: "fake", Version: "test"})
	if err != nil || code != http.StatusOK {
		return false
	}
	var resp wire.EnrollResponse
	if json.Unmarshal(body, &resp) != nil {
		return false
	}
	a.mu.Lock()
	// The build mark counts only at a first enrollment from an empty
	// state; a later one clears it (T-058 review S1, T-060).
	a.build = resp.Build && a.enrolls == 0 && !a.watched && !a.noMark
	a.cred = resp.Credential
	a.enrolls++
	a.mu.Unlock()
	a.report(ctx, wire.Report{Type: wire.ReportHello, Hello: a.hello()})
	return true
}

func (a *Agent) poll(ctx context.Context, cred string) (*wire.Command, int, error) {
	code, body, err := a.do(ctx, "GET", wire.PathPoll+"?wait=1", cred, nil)
	if err != nil || code != http.StatusOK {
		return nil, code, err
	}
	var cmd wire.Command
	if err := json.Unmarshal(body, &cmd); err != nil {
		return nil, code, err
	}
	return &cmd, code, nil
}

// report sends a report and retries on 503.
func (a *Agent) update(ctx context.Context, cmd wire.Command) {
	a.mu.Lock()
	fn := a.onUpdate
	a.mu.Unlock()
	if fn == nil {
		a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Error: `unknown command type "update"`})
		return
	}
	var body wire.UpdateBody
	_ = json.Unmarshal(cmd.Body, &body)
	item, after := fn(body)
	a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Items: []wire.Item{item}})
	if after != nil {
		a.SetHello(*after)
		a.report(ctx, wire.Report{Type: wire.ReportHello, Hello: a.hello()})
	}
}

func (a *Agent) report(ctx context.Context, rep wire.Report) error {
	for ctx.Err() == nil {
		a.mu.Lock()
		cred := a.cred
		a.mu.Unlock()
		code, body, err := a.do(ctx, "POST", wire.PathReport, cred, rep)
		if err != nil {
			return err
		}
		switch code {
		case http.StatusNoContent:
			return nil
		case http.StatusServiceUnavailable, http.StatusTooManyRequests:
			sleep(ctx, 20*time.Millisecond)
		default:
			return fmt.Errorf("linktest: report %s: %d %s", rep.Type, code, body)
		}
	}
	return ctx.Err()
}

func (a *Agent) handle(ctx context.Context, cmd wire.Command) {
	a.mu.Lock()
	a.commands = append(a.commands, cmd.Type)
	a.bodies = append(a.bodies, append([]byte(nil), cmd.Body...))
	hook := a.onCommand
	a.mu.Unlock()
	if hook != nil {
		hook(cmd.Type)
	}
	var body wire.ApplyBody // the widest body: resources, plan, max risk
	if err := json.Unmarshal(cmd.Body, &body); err != nil {
		a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Error: "bad body"})
		return
	}
	switch cmd.Type {
	case wire.CommandUpdate:
		a.update(ctx, cmd)
	case wire.CommandSeal:
		a.seal(ctx, cmd)
	case wire.CommandWatch:
		// Like the real agent: store the list and report what differs now
		// (open drift is reported again after a watch, T-018).
		a.mu.Lock()
		a.watched = true
		a.watch = body.Resources
		var drift []wire.Item
		for _, it := range a.observeAll(body.Resources) {
			if it.Status == wire.StatusDiffers {
				drift = append(drift, it)
			}
		}
		a.mu.Unlock()
		if len(drift) > 0 {
			a.report(ctx, wire.Report{Type: wire.ReportDrift, Items: drift})
		}
	case wire.CommandObserve:
		a.mu.Lock()
		items := a.observeAll(body.Resources)
		a.mu.Unlock()
		a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Items: items})
	case wire.CommandApply:
		a.apply(ctx, cmd.ID, body)
	}
}

// observeAll observes the resources and adds restart changes (D-036).
// The caller holds a.mu.
func (a *Agent) observeAll(resources []wire.Resource) []wire.Item {
	changed := map[string]bool{}
	items := make([]wire.Item, len(resources))
	for i, r := range resources {
		items[i] = a.observe(r)
		changed[r.ID] = items[i].Status == wire.StatusDiffers
	}
	for i, r := range resources {
		list, _ := r.Attrs["restart_on"].([]any)
		for _, v := range list {
			if id, _ := v.(string); changed[id] {
				items[i].Status = wire.StatusDiffers
				items[i].Changes = append(items[i].Changes, wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive})
				break
			}
		}
	}
	return items
}

// observe compares one desired resource with the guest. The caller holds a.mu.
func (a *Agent) observe(r wire.Resource) wire.Item {
	have, exists := a.guest[r.ID]
	item := wire.Item{Resource: r.ID, Status: wire.StatusUnchanged}
	if !exists {
		item.Changes = append(item.Changes, wire.Change{Attr: wire.AttrState, From: wire.StateAbsent, To: wire.StatePresent, Risk: wire.RiskReversible})
	}
	for _, attr := range slices.Sorted(maps.Keys(r.Attrs)) {
		if attr == "restart_on" {
			continue
		}
		want := r.Attrs[attr]
		var got any
		if exists {
			got = have[attr]
		}
		if exists && reflect.DeepEqual(norm(got), norm(want)) {
			continue
		}
		c := wire.Change{Attr: attr, From: got, To: want, Risk: wire.RiskReversible}
		if slices.Contains(r.Sensitive, attr) && !a.LeakSensitive {
			c = wire.Change{Attr: attr, Sensitive: true, Risk: wire.RiskReversible}
		}
		item.Changes = append(item.Changes, c)
	}
	if len(item.Changes) > 0 {
		item.Status = wire.StatusDiffers
	}
	return item
}

// norm makes values from JSON and from Go literals comparable.
func norm(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

// apply follows the agent rule of D-075: a resource changes only when its
// new difference is covered by the planned changes (or it is unverified),
// and never above max_risk.
func (a *Agent) apply(ctx context.Context, id string, body wire.ApplyBody) {
	byID := map[string]wire.Resource{}
	for _, r := range body.Resources {
		byID[r.ID] = r
	}
	plans := map[string]wire.Planned{}
	for _, p := range body.Planned {
		if _, ok := byID[p.Resource]; !ok {
			a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: id, Error: "planned resource " + p.Resource + " is not in the resources"})
			return
		}
		if _, dup := plans[p.Resource]; dup {
			a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: id, Error: "resource " + p.Resource + " is planned twice"})
			return
		}
		plans[p.Resource] = p
	}
	a.mu.Lock()
	// Like the real agent (beginApply, D-055): the applied resources join
	// the watch list now, not when the next watch command arrives. Else a
	// guest change between this apply and that watch is compared with the
	// old desired values.
	for _, r := range body.Resources {
		if i := slices.IndexFunc(a.watch, func(w wire.Resource) bool { return w.ID == r.ID }); i >= 0 {
			a.watch[i] = r
		} else {
			a.watch = append(a.watch, r)
		}
	}
	items := a.observeAll(body.Resources)
	a.mu.Unlock()
	for i := range items {
		it := &items[i]
		a.report(ctx, wire.Report{Type: wire.ReportProgress, CommandID: id,
			Items: []wire.Item{{Resource: it.Resource, Status: wire.StatusRunning}}})
		if it.Status == wire.StatusUnchanged {
			continue
		}
		if p, ok := plans[it.Resource]; !ok || (!p.Unverified && !wire.Covers(p.Changes, it.Changes)) {
			it.Status = wire.StatusRefused
			it.Output = "the guest differs from the approved Plan; plan again"
			continue
		}
		if wire.MaxRisk(it.Changes).Exceeds(body.MaxRisk) {
			it.Status = wire.StatusRefused
			it.Output = "a change is above the approved risk " + string(body.MaxRisk)
			continue
		}
		a.mu.Lock()
		if msg, bad := a.failing[it.Resource]; bad {
			it.Status, it.Output = wire.StatusFailed, msg
		} else {
			attrs := maps.Clone(byID[it.Resource].Attrs)
			delete(attrs, "restart_on")
			a.guest[it.Resource] = attrs
			it.Status = wire.StatusApplied
		}
		a.mu.Unlock()
	}
	a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: id, Items: items})
}

// Bodies returns the raw body of every command the agent got, in order.
func (a *Agent) Bodies() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]byte(nil), a.bodies...)
}

// OnCommand sets a test hook that runs before the agent handles each
// command (nil clears it). It can change the controller while a Plan
// observes.
func (a *Agent) OnCommand(f func(wire.CommandType)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onCommand = f
}

// seal answers like the real agent (D-205, D-207): refused without the
// build mark or after a watch; else one Item per step, then the files go
// and the guest powers off (OnSeal).
func (a *Agent) seal(ctx context.Context, cmd wire.Command) {
	var body wire.SealBody
	if err := json.Unmarshal(cmd.Body, &body); err != nil {
		a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Error: "bad seal body"})
		return
	}
	a.mu.Lock()
	allowed, fail, stuck, hook := a.build && !a.watched, a.sealFail, a.sealStuck, a.onSeal
	a.mu.Unlock()
	if !allowed {
		a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID,
			Items: []wire.Item{{Resource: wire.SealItemID(wire.SealStepGuard), Status: wire.StatusRefused, Output: "not a build guest"}}})
		return
	}
	var items []wire.Item
	failed := false
	for _, step := range wire.SealSteps {
		it := wire.Item{Resource: wire.SealItemID(step), Status: wire.StatusApplied}
		switch {
		case failed:
			it.Status, it.Output = wire.StatusSkipped, "an earlier step failed"
		case step == fail:
			it.Status, it.Output, failed = wire.StatusFailed, "simulated failure", true
		case step == wire.SealStepInitramfs:
			it.Status, it.Output = wire.StatusSkipped, "no dracut"
		case step == wire.SealStepUpgrade && !body.Upgraded:
			it.Status = wire.StatusSkipped
		}
		items = append(items, it)
	}
	if err := a.report(ctx, wire.Report{Type: wire.ReportResult, CommandID: cmd.ID, Items: items}); err != nil || failed || stuck {
		return
	}
	a.mu.Lock()
	a.cred, a.off = "", true // the credential is removed; the guest goes off
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
}
