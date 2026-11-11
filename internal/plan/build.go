package plan

// The build lane of an image version (T-062 part B; D-199, D-203 … D-209):
// base → build guest → enroll → apply → seal → stop → template → retention.
// Every exit before the template revokes the build agent, removes the
// build guest with the guard of T-073 and marks the version failed (S4).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Step ids of a build lane (run_step.resource; the target is image:<name>).
const (
	StepImageBase      = "image:base"
	StepImageSeal      = "image:seal"
	StepImageTemplate  = "image:template"
	StepImageRetention = "image:retention"
)

// Defaults of the build timeouts (T-039 Plan §2).
const (
	DefaultBuildEnrollTimeout = 20 * time.Minute
	DefaultSealTimeout        = 5 * time.Minute
)

// buildLink is the link name of the build guest of an Image (D-203).
func buildLink(image string) string { return wire.BuildLinkPrefix + image }

// BuildDigest is the build=<digest> line of a version (D-198): SHA-256
// over image name, version, recipe hash, base checksum and Run id.
func BuildDigest(image string, version int, recipe, base string, runID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("plumb-image-build\x00%s\x00%d\x00%s\x00%s\x00%d", image, version, recipe, base, runID)))
	return hex.EncodeToString(sum[:])
}

// lane is the state of one build lane.
type lane struct {
	e       *Engine
	s       *stepper
	image   string
	link    string
	version int
	guest   driver.GuestID // the build guest while it exists
	release func()         // the disk reservation
}

func (e *Engine) imageVersionEvent(ctx context.Context, image string, version int, status string, runID int64) {
	e.emit(ctx, "image.version", "image:"+image, "system", map[string]any{"version": version, "status": status, "run": runID})
}

// runImage is one image lane of a Run. set is the pinned revision. It
// returns whether every step succeeded.
func (e *Engine) runImage(ctx context.Context, p store.Plan, run store.Run, set *state.Set, ip ImagePlan) bool {
	s := &stepper{e: e, run: run, target: imageSlot(ip.Image), seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	if ip.Build == nil {
		s.add(ctx, StepImageRetention)
		e.retentionStep(ctx, s, set, ip.Image, 0)
		return s.ok
	}
	r, err := set.RenderImage(ip.Image)
	if err != nil {
		s.add(ctx, StepImageBase)
		s.set(ctx, StepImageBase, wire.StatusFailed, nil, "the Image does not render: "+err.Error())
		return false
	}
	info, _ := set.ImageInfo(ip.Image)
	for _, id := range append(append([]string{StepImageBase, wire.GuestConfigID}, resourceIDs(r.Resources)...),
		StepImageSeal, StepImageTemplate, StepImageRetention) {
		s.add(ctx, id)
	}
	l := &lane{e: e, s: s, image: ip.Image, link: buildLink(ip.Image)}
	ok := l.build(ctx, p, run, info, r)
	if !ok {
		l.fail(ctx, run)
		return false
	}
	e.retentionStep(ctx, s, set, ip.Image, l.version)
	return s.ok
}

func resourceIDs(rs []wire.Resource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// build runs the steps up to the template. On false the caller cleans up.
func (l *lane) build(ctx context.Context, p store.Plan, run store.Run, info state.ImageInfo, r *state.RenderedImage) bool {
	e, s := l.e, l.s
	// A rest of an earlier build of this Image goes first (D-203, S4).
	if err := e.removeBuildGuests(ctx, l.image); err != nil {
		s.set(ctx, StepImageBase, wire.StatusFailed, nil, "an old build guest could not be removed: "+err.Error())
		return false
	}
	v, err := e.st.AddImageVersion(ctx, store.ImageVersion{Image: l.image, Trigger: buildTrigger(p, l.image), RunID: run.ID,
		RevisionID: run.RevisionID, RecipeSHA256: r.RecipeSHA256, BaseSHA256: info.SHA256, DiskGB: r.Recipe.DiskGB},
		func(n int) string { return BuildDigest(l.image, n, r.RecipeSHA256, info.SHA256, run.ID) })
	if err != nil {
		s.set(ctx, StepImageBase, wire.StatusFailed, nil, err.Error())
		return false
	}
	l.version = v.Version
	e.imageVersionEvent(ctx, l.image, v.Version, store.ImageBuilding, run.ID)

	// 1. The base template.
	s.set(ctx, StepImageBase, wire.StatusRunning, nil, "")
	base, err := e.cfg.Driver.FetchImage(ctx, driver.ImageSpec{Name: l.image, Kind: info.Type, URL: r.Recipe.URL, SHA256: info.SHA256})
	if err != nil {
		s.set(ctx, StepImageBase, wire.StatusFailed, nil, err.Error())
		return false
	}
	s.set(ctx, StepImageBase, wire.StatusApplied, nil, fmt.Sprintf("base %s", info.SHA256[:12])+planNotes(p))

	// 2. The build guest, under the disk gate (D-203, T-074): its disk
	// counts until the lane ends.
	s.set(ctx, wire.GuestConfigID, wire.StatusRunning, nil, "")
	spec, err := buildSpec(l.image, v.Version, base, r)
	if err != nil {
		s.set(ctx, wire.GuestConfigID, wire.StatusFailed, nil, err.Error())
		return false
	}
	release, err := e.disk.Reserve(ctx, DiskBytes(spec.DiskGB), fmt.Sprintf("the %d GiB build disk of Image %s v%d", spec.DiskGB, l.image, v.Version))
	if err != nil {
		s.set(ctx, wire.GuestConfigID, wire.StatusFailed, nil, fmt.Sprintf("%v; nothing was made", err))
		return false
	}
	l.release = release
	hellos := e.hellos(l.link)
	token, _, err := e.cfg.Agents.NewEnrollToken(ctx, l.link)
	if err != nil {
		s.set(ctx, wire.GuestConfigID, wire.StatusFailed, nil, err.Error())
		return false
	}
	gid, err := e.cfg.Driver.CreateBuild(ctx, spec, driver.Bootstrap{Token: token})
	if err != nil {
		s.set(ctx, wire.GuestConfigID, wire.StatusFailed, nil, err.Error())
		return false
	}
	l.guest = gid
	s.set(ctx, wire.GuestConfigID, wire.StatusApplied, nil, fmt.Sprintf("build guest %s", gid))

	// 3. The agent of the build guest enrolls (build mark, no watch).
	if err := e.waitOnlineFor(ctx, l.link, hellos, e.cfg.BuildEnrollTimeout); err != nil {
		s.failRest(ctx, err.Error())
		return false
	}

	// 4. Apply: every resource is a create on a new guest, no risk limit,
	// no secret resolver (D-201, D-202): the values are the rendered ones.
	planned := make([]wire.Planned, len(r.Resources))
	for i, res := range r.Resources {
		planned[i] = wire.Planned{Resource: res.ID, Unverified: true}
	}
	rep, err := e.command(ctx, l.link, wire.CommandApply,
		wire.ApplyBody{PlanID: p.ID, Resources: r.Resources, Planned: planned, MaxRisk: wire.RiskDestructive}, e.cfg.ApplyTimeout,
		func(pr wire.Report) {
			for _, it := range pr.Items {
				if it.Status == wire.StatusRunning {
					s.set(ctx, it.Resource, wire.StatusRunning, nil, "")
				}
			}
		})
	reported := map[string]bool{}
	for _, it := range rep.Items {
		reported[it.Resource] = true
		s.set(ctx, it.Resource, it.Status, it.Changes, it.Output)
	}
	if err != nil {
		s.failRest(ctx, err.Error())
		return false
	}
	for _, res := range r.Resources {
		if !reported[res.ID] {
			s.set(ctx, res.ID, wire.StatusFailed, nil, "the agent did not report this resource")
		}
	}
	if !s.ok {
		return false // fail marks the rest: nothing was converted
	}

	// 5. Seal, as a handshake (B1): the controller takes the result and
	// revokes the agent; it sends no stop. The agent removes its files and
	// powers the guest off; the controller waits for stopped.
	s.set(ctx, StepImageSeal, wire.StatusRunning, nil, "")
	deadline := time.Now().Add(e.cfg.SealTimeout)
	rep, err = e.command(ctx, l.link, wire.CommandSeal, wire.SealBody{PlanID: p.ID, Upgraded: r.Recipe.Upgrade}, e.cfg.SealTimeout, nil)
	if rerr := e.cfg.Agents.Revoke(context.WithoutCancel(ctx), l.link); rerr != nil {
		e.log.Error("revoke build agent", "image", l.image, "err", rerr)
	}
	summary, sealOK := sealSummary(rep)
	if err != nil || !sealOK {
		if err != nil {
			summary = strings.TrimSpace(summary + "\n" + err.Error())
		}
		s.set(ctx, StepImageSeal, wire.StatusFailed, nil, summary)
		return false
	}
	if err := e.waitStopped(ctx, gid, deadline); err != nil {
		s.set(ctx, StepImageSeal, wire.StatusFailed, nil, summary+"\n"+err.Error())
		return false
	}
	s.set(ctx, StepImageSeal, wire.StatusApplied, nil, summary+"\nthe guest powered itself off")

	// 6. The template.
	s.set(ctx, StepImageTemplate, wire.StatusRunning, nil, "")
	ref, err := e.cfg.Driver.MakeImage(ctx, gid, driver.BuiltImage{Image: l.image, Version: v.Version, Digest: v.Digest})
	if err != nil {
		s.set(ctx, StepImageTemplate, wire.StatusFailed, nil, err.Error())
		return false
	}
	l.guest = "" // it is the template now
	l.done()
	if err := e.st.FinishImageVersion(context.WithoutCancel(ctx), l.image, v.Version, store.ImageReady); err != nil {
		s.set(ctx, StepImageTemplate, wire.StatusFailed, nil, err.Error())
		return false
	}
	e.imageVersionEvent(ctx, l.image, v.Version, store.ImageReady, run.ID)
	s.set(ctx, StepImageTemplate, wire.StatusApplied, nil, fmt.Sprintf("%s (%s)", VersionTemplate(l.image, v.Version), ref))
	return true
}

// done releases the disk reservation once.
func (l *lane) done() {
	if l.release != nil {
		l.release()
		l.release = nil
	}
}

// fail is the one exit of a failed build (S4): revoke, remove the guest
// with the guard, version failed, every open step failed. It uses a
// context without the cancel: a closing engine still cleans up.
func (l *lane) fail(ctx context.Context, run store.Run) {
	e := l.e
	ctx = context.WithoutCancel(ctx)
	defer l.done()
	if err := e.cfg.Agents.Revoke(ctx, l.link); err != nil {
		e.log.Error("revoke build agent", "image", l.image, "err", err)
	}
	left := ""
	if l.guest != "" {
		if err := e.cfg.Driver.RemoveBuild(ctx, l.guest, l.image, l.version); err != nil && !errors.Is(err, driver.ErrNotFound) {
			left = fmt.Sprintf("; the build guest %s is left: %v", l.guest, err)
			e.log.Error("remove build guest", "image", l.image, "guest", l.guest, "err", err)
		}
	}
	if l.version > 0 {
		if err := e.st.FinishImageVersion(ctx, l.image, l.version, store.ImageFailed); err != nil {
			e.log.Error("image version failed", "image", l.image, "version", l.version, "err", err)
		}
		e.imageVersionEvent(ctx, l.image, l.version, store.ImageFailed, run.ID)
	}
	l.s.failRest(ctx, "the build failed; nothing was converted"+left)
}

// planNotes returns the disk notes of a Plan for the Run: a scheduled
// build has no person who read the Plan, so the Run records them (T-061
// review: "thin-pool metadata use is unknown").
func planNotes(p store.Plan) string {
	var prop Proposal
	if json.Unmarshal(p.Proposal, &prop) != nil {
		return ""
	}
	out := ""
	for _, n := range prop.Notes {
		if strings.Contains(n, "thin-pool metadata") || strings.Contains(n, "would not start now") {
			out += "\n" + n
		}
	}
	return out
}

// buildTrigger is the trigger of an Image in a Plan.
func buildTrigger(p store.Plan, image string) string {
	images, _ := imagePlans(p)
	for _, ip := range images {
		if ip.Image == image && ip.Build != nil {
			return ip.Build.Trigger
		}
	}
	return store.TriggerChange
}

// buildSpec turns the rendered build guest into a driver spec.
func buildSpec(image string, version int, base driver.ImageRef, r *state.RenderedImage) (driver.BuildSpec, error) {
	a := r.Guest.Attrs
	spec := driver.BuildSpec{Image: image, Version: version, Base: base, Upgrade: r.Recipe.Upgrade}
	var err error
	if spec.Cores, err = driver.IntValue(a[driver.AttrCores]); err != nil {
		return spec, err
	}
	if spec.MemoryMB, err = driver.IntValue(a[driver.AttrMemory]); err != nil {
		return spec, err
	}
	if spec.DiskGB, err = driver.IntValue(a[driver.AttrDisk]); err != nil {
		return spec, err
	}
	spec.Bridge, _ = a[driver.AttrBridge].(string)
	spec.IP, _ = a[driver.AttrIP].(string)
	spec.Gateway, _ = a[driver.AttrGateway].(string)
	return spec, nil
}

// sealSummary checks the seal result: every step applied or skipped, the
// guard applied (D-205, D-207). The summary has one line per step.
func sealSummary(rep wire.Report) (string, bool) {
	ok := rep.Error == "" && len(rep.Items) > 0
	guard := false
	var lines []string
	for _, it := range rep.Items {
		line := it.Resource + ": " + string(it.Status)
		if it.Output != "" {
			line += " (" + it.Output + ")"
		}
		lines = append(lines, line)
		switch it.Status {
		case wire.StatusApplied, wire.StatusSkipped:
		default:
			ok = false
		}
		if it.Resource == wire.SealItemID(wire.SealStepGuard) && it.Status == wire.StatusApplied {
			guard = true
		}
	}
	if rep.Error != "" {
		lines = append(lines, rep.Error)
	}
	return strings.Join(lines, "\n"), ok && guard
}

// waitOnlineFor is waitOnline with its own timeout.
func (e *Engine) waitOnlineFor(ctx context.Context, link string, after int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !e.cfg.Agents.Online(link) || e.hellos(link) <= after {
		select {
		case <-tick.C:
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("the agent of the build guest did not come online within %v", timeout)
			}
			return ctx.Err()
		}
	}
	return nil
}

// observeRetries is how many reads in a row may fail before a build lane
// gives up on the power state (review N2 of #133).
const observeRetries = 3

// waitStopped waits until the build guest is stopped, until deadline. A
// guest that does not power off itself is a failed build: the controller
// never stops it, so a template never holds a running agent's files (B1).
// A read that fails is tried again; observeRetries failures in a row end
// the wait.
func (e *Engine) waitStopped(ctx context.Context, gid driver.GuestID, deadline time.Time) error {
	failures := 0
	for {
		st, err := e.cfg.Driver.Observe(ctx, gid)
		switch {
		case errors.Is(err, driver.ErrNotFound):
			return fmt.Errorf("the build guest is gone: %w", err)
		case err != nil:
			if failures++; failures >= observeRetries {
				return fmt.Errorf("the build guest cannot be read (%d tries): %w", failures, err)
			}
		case st.Power == driver.PowerStopped:
			return nil
		default:
			failures = 0
		}
		if !time.Now().Before(deadline) {
			return errors.New("the build guest did not power itself off after seal; it is removed, not converted")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.cfg.PowerPoll):
		}
	}
}

// removeBuildGuests removes every build guest of an Image (all Images for
// image ""), after the driver's guard. The agent of the link goes first.
func (e *Engine) removeBuildGuests(ctx context.Context, image string) error {
	guests, err := e.cfg.Driver.ListBuilds(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range guests {
		if (image != "" && g.Image != image) || g.Template {
			continue // a converted rest stays; the Image status shows it
		}
		if err := e.cfg.Agents.Revoke(ctx, buildLink(g.Image)); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := e.cfg.Driver.RemoveBuild(ctx, g.ID, g.Image, g.Version); err != nil && !errors.Is(err, driver.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// LeftTemplate is a build guest that was converted but never named as a
// version (T-061 N1). Plumb never adopts or removes it; the Image status
// shows it so that a person can remove it.
type LeftTemplate struct {
	Name    string
	GuestID string
}

// LeftTemplates lists the converted build rests of an Image.
func (e *Engine) LeftTemplates(ctx context.Context, image string) ([]LeftTemplate, error) {
	guests, err := e.cfg.Driver.ListBuilds(ctx)
	if errors.Is(err, driver.ErrNotImplemented) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LeftTemplate
	for _, g := range guests {
		if g.Template && g.Image == image {
			out = append(out, LeftTemplate{Name: fmt.Sprintf("plumb-build-%s-v%d", g.Image, g.Version), GuestID: string(g.ID)})
		}
	}
	return out, nil
}

// --- retention (D-209) ---

// retentionStep runs the step image:retention: it keeps the newest keep
// ready versions, never the version of this Run (built), never fewer than
// keep ready ones, and removes older versions, rests of failed builds and
// base templates with another checksum. An Image that is not built in the
// set (deleted, or stock now) loses every template. It runs only after the
// new version is ready (n2): the caller calls it only then. A template
// that the driver's guard refuses is left and named in the output.
func (e *Engine) retentionStep(ctx context.Context, s *stepper, set *state.Set, image string, built int) {
	ctx = context.WithoutCancel(ctx) // the version is ready; finish the clean-up
	s.set(ctx, StepImageRetention, wire.StatusRunning, nil, "")
	info, ok := set.ImageInfo(image)
	keep := 0
	if ok && info.Built {
		keep = info.Keep
	}
	removed, left, err := e.retain(ctx, image, keep, info.SHA256, built)
	if err != nil {
		s.set(ctx, StepImageRetention, wire.StatusFailed, nil, err.Error())
		return
	}
	var lines []string
	for _, r := range removed {
		lines = append(lines, "removed "+r)
	}
	lines = append(lines, left...)
	status := wire.StatusApplied
	if len(removed) == 0 && len(left) == 0 {
		status = wire.StatusUnchanged
	}
	s.set(ctx, StepImageRetention, status, nil, strings.Join(lines, "\n"))
}

// retain applies the retention rule. keep 0 removes every template of the
// Image (sha256 is then ignored). It returns what it removed and what it
// left (with the reason).
func (e *Engine) retain(ctx context.Context, image string, keep int, sha256 string, built int) (removed, left []string, err error) {
	versions, err := e.st.ImageVersions(ctx, image)
	if err != nil {
		return nil, nil, err
	}
	objs, err := e.cfg.Driver.ListImages(ctx, image)
	if err != nil {
		return nil, nil, err
	}
	tpl := map[int]driver.ImageObject{}
	for _, o := range objs {
		if o.Kind == driver.ImageVersionTemplate {
			tpl[o.Version] = o
		}
	}
	remove := func(o driver.ImageObject, version int) {
		if version > 0 {
			if !e.startRemove(image, version) {
				left = append(left, fmt.Sprintf("left %s: a create clones from it now; the next build tries again", o.Name))
				return
			}
			defer e.endRemove(image, version)
		}
		if err := e.cfg.Driver.RemoveImage(ctx, o); err != nil {
			left = append(left, fmt.Sprintf("left %s: %v", o.Name, err))
			return
		}
		removed = append(removed, o.Name)
		if version > 0 {
			if err := e.st.RemoveImageVersion(ctx, image, version); err != nil && !errors.Is(err, store.ErrImageVersionState) {
				left = append(left, fmt.Sprintf("version %d: %v", version, err))
			}
		}
	}
	kept := 0
	for _, v := range versions { // newest first
		o, exists := tpl[v.Version]
		switch v.Status {
		case store.ImageReady:
			if !exists {
				// The template is gone: the row says so now (D-198).
				_ = e.st.RemoveImageVersion(ctx, image, v.Version)
				continue
			}
			if v.Version == built || kept < keep {
				kept++
				continue
			}
			remove(o, v.Version)
		case store.ImageFailed:
			// A rest of a failed build: a template that a late conversion
			// left (T-061 review N1). It is never adopted.
			if exists && v.Version != built {
				remove(o, v.Version)
			}
		}
	}
	for _, o := range objs {
		if o.Kind == driver.ImageBaseTemplate && (keep == 0 || o.Digest != sha256) {
			remove(o, 0)
		}
	}
	return removed, left, nil
}

// --- Target from a version (D-200) ---

// imageKey names one version of an Image.
type imageKey struct {
	image   string
	version int
}

// pinVersion marks a version as the clone source of a create. It fails
// when retention removes the version now.
func (e *Engine) pinVersion(image string, version int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := imageKey{image, version}
	if e.removing[k] {
		return false
	}
	e.pinned[k]++
	return true
}

func (e *Engine) unpinVersion(image string, version int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := imageKey{image, version}
	if e.pinned[k]--; e.pinned[k] <= 0 {
		delete(e.pinned, k)
	}
}

// startRemove marks a version for removal. It fails when a create clones
// from it now: a clone takes no source lock in Proxmox.
func (e *Engine) startRemove(image string, version int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := imageKey{image, version}
	if e.pinned[k] > 0 {
		return false
	}
	e.removing[k] = true
	return true
}

func (e *Engine) endRemove(image string, version int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.removing, imageKey{image, version})
}

// newestVersion returns the template of the newest ready version of a
// built Image that exists, pinned until the caller calls unpin (after the
// clone). A ready row whose template is gone becomes removed (D-198).
func (e *Engine) newestVersion(ctx context.Context, image string) (driver.ImageRef, int, func(), error) {
	versions, err := e.st.ImageVersions(ctx, image)
	if err != nil {
		return "", 0, nil, err
	}
	for _, v := range versions {
		if v.Status != store.ImageReady || !e.pinVersion(image, v.Version) {
			continue
		}
		unpin := func() { e.unpinVersion(image, v.Version) }
		ref, err := e.cfg.Driver.FindImage(ctx, image, v.Version, v.Digest)
		if errors.Is(err, driver.ErrNotFound) {
			unpin()
			_ = e.st.RemoveImageVersion(ctx, image, v.Version)
			continue
		}
		if err != nil {
			unpin()
			return "", 0, nil, err
		}
		return ref, v.Version, unpin, nil
	}
	return "", 0, nil, fmt.Errorf("the Image %s has no ready version; build the Image first", image)
}

// --- recover (S4) ---

// recoverBuilds fails the versions that a crash left in the state
// building, revokes the build agents and removes every build guest.
func (e *Engine) recoverBuilds(ctx context.Context) error {
	building, err := e.st.BuildingImageVersions(ctx)
	if err != nil {
		return err
	}
	live := map[string]bool{} // Images that a Run of this process builds
	for _, v := range building {
		e.mu.Lock()
		active := e.active[v.RunID]
		e.mu.Unlock()
		if active {
			live[v.Image] = true
			continue
		}
		if err := e.cfg.Agents.Revoke(ctx, buildLink(v.Image)); err != nil {
			return err
		}
		if err := e.st.FinishImageVersion(ctx, v.Image, v.Version, store.ImageFailed); err != nil {
			return err
		}
		e.imageVersionEvent(ctx, v.Image, v.Version, store.ImageFailed, v.RunID)
	}
	guests, err := e.cfg.Driver.ListBuilds(ctx)
	if err != nil {
		// The guests stay; the next build of each Image removes them.
		e.log.Error("recover: build guests not listed", "err", err)
		return nil
	}
	for _, g := range guests {
		if live[g.Image] || g.Template {
			continue
		}
		if err := e.removeBuildGuests(ctx, g.Image); err != nil {
			e.log.Error("recover: build guest not removed", "image", g.Image, "guest", g.ID, "err", err)
		}
	}
	return nil
}
