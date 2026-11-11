package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// HumanOnlyImageRecipe: the Plan makes or deletes a built Image, or changes
// its recipe, schedule or retention (D-201).
const HumanOnlyImageRecipe = "image-recipe"

// DiskLimitPercent is the limit that a Plan shows (D-203). The check
// itself is the disk gate of T-074, which part B of T-062 uses.
const DiskLimitPercent = 85

// ErrStockImage is wrapped when a build is asked for an Image without
// spec.build.
var ErrStockImage = errors.New("the Image has no build part; it is a stock image")

// ImagePlan is the image part of a Plan (Plan.images, D-199): a build of a
// new version, and the templates that the retention step removes.
type ImagePlan struct {
	Image   string          `json:"image"`
	Build   *ImageBuildPlan `json:"build,omitempty"`
	Removes []ImageRemoval  `json:"removes"`
	Disk    *DiskUse        `json:"disk,omitempty"`
}

// ImageBuildPlan is a planned build.
type ImageBuildPlan struct {
	// Version is the next free number when the Plan was made; the Run
	// takes the next free number at its start.
	Version      int    `json:"version"`
	Trigger      string `json:"trigger"` // store.Trigger*
	Upgrade      bool   `json:"upgrade"`
	RecipeSHA256 string `json:"recipe_sha256"`
	// Resources are what the build applies: all create and unverified.
	Resources []ResourceChange `json:"resources"`
}

// ImageRemoval is a template that the retention step removes (D-209).
type ImageRemoval struct {
	Kind    string `json:"kind"` // "version" or "base"
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
}

// DiskUse is the use of the disk storage when the Plan was made.
type DiskUse struct {
	Storage       string `json:"storage"`
	TotalBytes    int64  `json:"total_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
	LimitPercent  int    `json:"limit_percent"`
}

// VersionTemplate is the template name of a version (D-198).
func VersionTemplate(image string, version int) string {
	return fmt.Sprintf("plumb-img-%s-v%d", image, version)
}

// imageSlot is the busy key of an Image: one build or retention per Image
// at a time. A Target name cannot hold ':'.
func imageSlot(image string) string { return "image:" + image }

// imageFacts is what decides a build and the human-only rule.
type imageFacts struct {
	info state.ImageInfo
	hash string // recipe hash; "" for a stock Image
}

func factsOf(set *state.Set, name string) (imageFacts, bool, error) {
	info, ok := set.ImageInfo(name)
	if !ok {
		return imageFacts{}, false, nil
	}
	f := imageFacts{info: info}
	if info.Built {
		r, err := set.RenderImage(name)
		if err != nil {
			return f, true, err
		}
		f.hash = r.RecipeSHA256
	}
	return f, true, nil
}

// imagesHumanOnly reports whether a Plan from base to next needs a human
// approver for an Image (D-201): a built Image is made or deleted, turns
// built or stock, or changes its recipe, schedule or retention. The rule
// reads the content, for every origin; a Block change that reaches a
// built Image changes its recipe.
func imagesHumanOnly(base, next *state.Set) (bool, error) {
	for _, name := range union(base.Images(), next.Images()) {
		b, okb, err := factsOf(base, name)
		if err != nil {
			return false, err
		}
		n, okn, err := factsOf(next, name)
		if err != nil {
			return false, err
		}
		wasBuilt, isBuilt := okb && b.info.Built, okn && n.info.Built
		if !wasBuilt && !isBuilt {
			continue
		}
		if wasBuilt != isBuilt || b.hash != n.hash || b.info.Keep != n.info.Keep || string(b.info.Schedule) != string(n.info.Schedule) {
			return true, nil
		}
	}
	return false, nil
}

// planImages computes the image part of a Plan from base to next. A built
// Image whose recipe is new or changed gets a build (trigger change);
// force names one Image that gets a build with trigger forced (manual or
// schedule). Every Image that was or is built gets its removals. The
// notes become Plan warnings.
func (e *Engine) planImages(ctx context.Context, base, next *state.Set, force, forced string) ([]ImagePlan, []string, error) {
	var out []ImagePlan
	var notes []string
	for _, name := range union(base.Images(), next.Images()) {
		b, okb, err := factsOf(base, name)
		if err != nil {
			return nil, nil, err
		}
		n, okn, err := factsOf(next, name)
		if err != nil {
			return nil, nil, err
		}
		wasBuilt, isBuilt := okb && b.info.Built, okn && n.info.Built
		if !wasBuilt && !isBuilt {
			continue
		}
		ip := ImagePlan{Image: name, Removes: []ImageRemoval{}}
		trigger := ""
		switch {
		case name == force && isBuilt:
			trigger = forced
		case isBuilt && (!wasBuilt || b.hash != n.hash):
			trigger = store.TriggerChange
		}
		versions, err := e.st.ImageVersions(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		if trigger != "" {
			r, err := next.RenderImage(name)
			if err != nil {
				return nil, nil, err
			}
			nextNo := 1
			if len(versions) > 0 {
				nextNo = versions[0].Version + 1
			}
			bp := &ImageBuildPlan{Version: nextNo, Trigger: trigger, Upgrade: r.Recipe.Upgrade, RecipeSHA256: r.RecipeSHA256,
				Resources: []ResourceChange{}}
			// The build guest is new: nobody observes it, and it has no
			// state to lose (D-201).
			for _, res := range r.Resources {
				bp.Resources = append(bp.Resources, ResourceChange{Resource: res.ID, Action: wire.ActionCreate,
					Risk: wire.RiskReversible, Unverified: true, After: res.After})
			}
			ip.Build = bp
			disk, dnotes := e.diskUse(ctx, name)
			ip.Disk = disk
			notes = append(notes, dnotes...)
		}
		rm, rnotes := e.planRemovals(ctx, name, versions, n.info, isBuilt, ip.Build != nil)
		ip.Removes = append(ip.Removes, rm...)
		notes = append(notes, rnotes...)
		if ip.Build == nil && len(ip.Removes) == 0 {
			continue
		}
		out = append(out, ip)
	}
	return out, notes, nil
}

// planRemovals lists the templates that retention would remove (D-209):
// ready versions beyond keep (a planned build takes one place), and base
// templates with another checksum. An Image that is deleted or no longer
// built loses every version and base template. The Run decides again
// with what exists then; this is the preview.
func (e *Engine) planRemovals(ctx context.Context, name string, versions []store.ImageVersion, info state.ImageInfo, built, building bool) ([]ImageRemoval, []string) {
	var out []ImageRemoval
	keep := info.Keep
	if !built {
		keep = 0
	} else if building {
		keep-- // the new version is the newest ready one when retention runs
	}
	ready := 0
	for _, v := range versions { // newest first
		if v.Status != store.ImageReady {
			continue
		}
		ready++
		if ready > keep {
			out = append(out, ImageRemoval{Kind: "version", Name: VersionTemplate(name, v.Version), Version: v.Version})
		}
	}
	objs, err := e.cfg.Driver.ListImages(ctx, name)
	if errors.Is(err, driver.ErrNotImplemented) {
		return out, nil
	}
	if err != nil {
		return out, []string{fmt.Sprintf("Image %s: the templates could not be listed (%v); the removal list may be short.", name, err)}
	}
	for _, o := range objs {
		if o.Kind == driver.ImageBaseTemplate && (!built || o.Digest != info.SHA256) {
			out = append(out, ImageRemoval{Kind: "base", Name: o.Name})
		}
	}
	return out, nil
}

// diskUse reads the disk storage for the Plan of a build (D-203). The
// notes say what the person who approves must know: the use is unknown,
// or the thin-pool metadata use is unknown (T-061 review). The check
// before the build is the disk gate of T-074 (part B); ReservedBytes comes
// from it then.
func (e *Engine) diskUse(ctx context.Context, image string) (*DiskUse, []string) {
	ds, err := e.cfg.Driver.DiskSpace(ctx)
	if errors.Is(err, driver.ErrNotImplemented) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("Image %s: the use of the disk storage is unknown (%v); the build checks it again before it starts.", image, err)}
	}
	u := &DiskUse{Storage: ds.Storage, TotalBytes: ds.TotalBytes, UsedBytes: ds.UsedBytes, LimitPercent: DiskLimitPercent}
	var notes []string
	if !ds.MetaKnown {
		notes = append(notes, fmt.Sprintf("Image %s: thin-pool metadata use is unknown.", image))
	}
	return u, notes
}

// checkImageTargets refuses a new Target of a built Image when no version
// is ready, or when the same Plan builds that Image (D-200).
func (e *Engine) checkImageTargets(ctx context.Context, next *state.Set, plans []TargetPlan, images []ImagePlan) error {
	builds := map[string]bool{}
	for _, ip := range images {
		if ip.Build != nil {
			builds[ip.Image] = true
		}
	}
	for _, tp := range plans {
		if tp.Guest != "create" {
			continue
		}
		r, err := renderOrNil(next, tp.Target)
		if err != nil || r == nil {
			return err
		}
		info, ok := next.ImageInfo(r.Image)
		if !ok || !info.Built {
			continue
		}
		if builds[r.Image] {
			return &ProposalError{Target: tp.Target, Msg: fmt.Sprintf(
				"this Plan builds Image %q, and a Target cannot be made from it in the same Plan; approve the build first, then create the Target", r.Image)}
		}
		versions, err := e.st.ImageVersions(ctx, r.Image)
		if err != nil {
			return err
		}
		ready := false
		for _, v := range versions {
			ready = ready || v.Status == store.ImageReady
		}
		if !ready {
			return &ProposalError{Target: tp.Target, Msg: fmt.Sprintf("Image %q has no ready version; build the Image first", r.Image)}
		}
	}
	return nil
}

// ProposeBuild plans a build of one built Image with no document change
// (origin build, D-199): trigger manual (POST /images/{name}/build) or
// schedule (T-063).
func (e *Engine) ProposeBuild(ctx context.Context, image, trigger, reason, author string) (store.Plan, error) {
	if author == "" {
		return store.Plan{}, errors.New("plan: a build needs an author")
	}
	if trigger != store.TriggerManual && trigger != store.TriggerSchedule {
		return store.Plan{}, fmt.Errorf("plan: unknown build trigger %q", trigger)
	}
	baseRev, base, err := e.base(ctx)
	if err != nil {
		return store.Plan{}, err
	}
	info, ok := base.ImageInfo(image)
	if !ok {
		return store.Plan{}, fmt.Errorf("plan: Image %q: %w", image, store.ErrNotFound)
	}
	if !info.Built {
		return store.Plan{}, &ProposalError{Msg: fmt.Sprintf("Image %q has no build part; only a built Image can be built", image), Err: ErrStockImage}
	}
	if !e.cfg.ImageBuilds {
		return store.Plan{}, &ProposalError{Msg: textBuildsOff}
	}
	images, notes, err := e.planImages(ctx, base, base, image, trigger)
	if err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = "build " + image
	}
	proposal, _ := json.Marshal(Proposal{Notes: notes})
	raw, _ := json.Marshal(images)
	p, err := e.st.CreatePlan(ctx, store.Plan{BaseRevision: baseRev, Origin: OriginBuild, Proposal: proposal, Changes: json.RawMessage("[]"),
		Images: raw, Risk: string(wire.RiskReversible), Reason: reason, Author: author})
	if err != nil {
		return store.Plan{}, err
	}
	e.emit(ctx, "plan.created", fmt.Sprintf("plan:%d", p.ID), author,
		map[string]any{"origin": OriginBuild, "risk": wire.RiskReversible, "images": []string{image}})
	return p, nil
}

const textBuildsOff = "image builds are not available yet (T-062 part B)"

// recipeStale reports whether a recipe of the image part differs at set.
func recipeStale(set *state.Set, images []ImagePlan) (bool, error) {
	for _, ip := range images {
		if ip.Build == nil {
			continue
		}
		info, ok := set.ImageInfo(ip.Image)
		if !ok || !info.Built {
			return true, nil
		}
		r, err := set.RenderImage(ip.Image)
		if err != nil {
			return true, nil //nolint:nilerr // a recipe that no longer renders is a changed recipe
		}
		if r.RecipeSHA256 != ip.Build.RecipeSHA256 {
			return true, nil
		}
	}
	return false, nil
}

// imagePlans reads the image part of a stored Plan.
func imagePlans(p store.Plan) ([]ImagePlan, error) {
	if len(p.Images) == 0 {
		return nil, nil
	}
	var out []ImagePlan
	if err := json.Unmarshal(p.Images, &out); err != nil {
		return nil, fmt.Errorf("plan %d: images: %w", p.ID, err)
	}
	return out, nil
}

// reserveImages takes the busy slot of every Image of the Plan, or fails
// with store.ErrBuildRunning: one build or retention per Image at a time
// (D-203), also across a restart (a version in the state building).
func (e *Engine) reserveImages(ctx context.Context, images []ImagePlan) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	building, err := e.st.BuildingImageVersions(ctx)
	if err != nil {
		return nil, err
	}
	busy := map[string]bool{}
	for _, v := range building {
		busy[v.Image] = true
	}
	slots := make([]string, 0, len(images))
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ip := range images {
		if busy[ip.Image] || e.busy[imageSlot(ip.Image)] > 0 {
			return nil, fmt.Errorf("plan: Image %s: %w", ip.Image, store.ErrBuildRunning)
		}
		slots = append(slots, imageSlot(ip.Image))
	}
	for _, s := range slots {
		e.busy[s]++
	}
	return slots, nil
}

// approveBuild approves a Plan of origin build (D-199): no revision. The
// recipe at the head must be the planned one; a head change that leaves
// it alone is fine. The Run is pinned to that head.
func (e *Engine) approveBuild(ctx context.Context, p store.Plan, principal string, images []ImagePlan) (store.Run, error) {
	for attempt := 0; ; attempt++ {
		head, err := e.st.Head(ctx)
		if err != nil {
			return store.Run{}, err
		}
		set, err := e.load(ctx, head.ID)
		if err != nil {
			return store.Run{}, err
		}
		if stale, err := recipeStale(set, images); err != nil {
			return store.Run{}, err
		} else if stale {
			e.markStale(ctx, p.ID)
			return store.Run{}, fmt.Errorf("plan: plan %d: the recipe changed: %w", p.ID, ErrStale)
		}
		slots, err := e.reserveImages(ctx, images)
		if err != nil {
			return store.Run{}, err
		}
		run, err := e.st.ApproveBuildPlan(ctx, p.ID, principal, head.ID)
		if errors.Is(err, store.ErrHeadMoved) && attempt < 3 {
			e.release(slots)
			continue
		}
		if err != nil {
			e.release(slots)
			return store.Run{}, err
		}
		e.emit(ctx, "plan.approved", fmt.Sprintf("plan:%d", p.ID), principal, map[string]any{"run": run.ID, "pinned_revision": head.ID})
		e.start(p, run, set, nil, images, slots)
		return run, nil
	}
}

// markStale marks a pending Plan stale and tells OnPlanDone.
func (e *Engine) markStale(ctx context.Context, planID int64) {
	if ok, err := e.st.MarkPlanStale(ctx, planID); err == nil && ok {
		e.emit(ctx, "plan.stale", fmt.Sprintf("plan:%d", planID), "system", nil)
		if e.cfg.OnPlanDone != nil {
			e.cfg.OnPlanDone(ctx, planID, false)
		}
	}
}

// staleBuildPlans marks the pending build Plans whose recipe changed at the
// new head (D-199). A head change without a recipe change keeps them.
func (e *Engine) staleBuildPlans(ctx context.Context, set *state.Set) {
	plans, err := e.st.PendingPlans(ctx, OriginBuild)
	if err != nil {
		e.log.Error("pending build plans", "err", err)
		return
	}
	for _, p := range plans {
		images, err := imagePlans(p)
		if err != nil {
			e.log.Error("build plan", "plan", p.ID, "err", err)
			continue
		}
		if stale, _ := recipeStale(set, images); stale {
			e.markStale(ctx, p.ID)
		}
	}
}

// runImage is one image lane of a Run. Part B of T-062 builds here; until
// then a lane fails at its first step and changes nothing.
func (e *Engine) runImage(ctx context.Context, p store.Plan, run store.Run, set *state.Set, ip ImagePlan) bool {
	s := &stepper{e: e, run: run, target: imageSlot(ip.Image), seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	s.add(ctx, "image:base")
	s.set(ctx, "image:base", wire.StatusFailed, nil, textBuildsOff)
	return false
}

// UnusedImport is a downloaded base image that no template of the Image
// needs (D-209). Command is set only for a volume name that Plumb made, in
// the strict form of the T-064 review; else it is "".
type UnusedImport struct {
	Volume    string
	SizeBytes int64
	Command   string
}

// UnusedImports lists the import files of a built Image that its current
// checksum does not need. Plumb never deletes them; a person runs Command.
// A driver without the list gives none.
func (e *Engine) UnusedImports(ctx context.Context, image, sha256 string) ([]UnusedImport, error) {
	objs, err := e.cfg.Driver.ListImages(ctx, image)
	if errors.Is(err, driver.ErrNotImplemented) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []UnusedImport
	for _, o := range objs {
		if o.Kind != driver.ImageImportFile || (sha256 != "" && o.Digest == sha256) {
			continue
		}
		if sha256 != "" && len(sha256) >= 12 && strings.HasSuffix(o.Name, "-"+sha256[:12]+".qcow2") {
			continue
		}
		out = append(out, UnusedImport{Volume: o.Name, SizeBytes: o.SizeBytes, Command: FreeCommand(image, o.Name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Volume < out[j].Volume })
	return out, nil
}

// strict names (T-064 review): the storage id and an import file that
// Plumb made, plumb-<image>-<12 hex>.qcow2 (D-077). No shell character
// can be in them.
var (
	storageID  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	importFile = regexp.MustCompile(`^plumb-[a-z0-9][a-z0-9.-]*-[0-9a-f]{12}\.qcow2$`)
)

// FreeCommand returns the command that removes an import file on the
// Proxmox host, exactly "pvesm free <storage>:import/plumb-<image>-<12
// hex>.qcow2" with no quotes, or "" when the volume is not one that Plumb
// made for this Image.
func FreeCommand(image, volume string) string {
	storage, file, ok := strings.Cut(volume, ":import/")
	if !ok || !storageID.MatchString(storage) || !importFile.MatchString(file) {
		return ""
	}
	prefix := "plumb-" + image + "-"
	if !strings.HasPrefix(file, prefix) || len(file) != len(prefix)+12+len(".qcow2") {
		return ""
	}
	return "pvesm free " + storage + ":import/" + file
}

// DiskUse reads the disk storage of guests, for the Image status. It is
// nil when the driver cannot read it.
func (e *Engine) DiskUse(ctx context.Context) *DiskUse {
	ds, err := e.cfg.Driver.DiskSpace(ctx)
	if err != nil {
		return nil
	}
	return &DiskUse{Storage: ds.Storage, TotalBytes: ds.TotalBytes, UsedBytes: ds.UsedBytes, LimitPercent: DiskLimitPercent}
}
