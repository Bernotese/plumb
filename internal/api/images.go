package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// POST /images/{name}/build: a Plan of origin build (D-199).
func (s *Server) buildImage(w http.ResponseWriter, r *http.Request, p *principal) {
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
	pl, err := s.cfg.Plans.ProposeBuild(r.Context(), r.PathValue("name"), store.TriggerManual, req.Reason, p.name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// --- Image status (ImageStatus) ---

type imageVersionView struct {
	Version      int        `json:"version"`
	Status       string     `json:"status"`
	Trigger      string     `json:"trigger"`
	RunID        int64      `json:"run_id"`
	Revision     int64      `json:"revision"`
	RecipeSHA256 string     `json:"recipe_sha256"`
	BaseSHA256   string     `json:"base_sha256"`
	DiskGB       int        `json:"disk_gb"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	RemovedAt    *time.Time `json:"removed_at"`
	Targets      []string   `json:"targets"`
}

type unusedImportView struct {
	Volume    string `json:"volume"`
	SizeBytes int64  `json:"size_bytes"`
	// Command is only for a volume name that Plumb made (T-064 review);
	// the web offers Copy only for this exact form.
	Command string `json:"command"`
}

type diskUseView struct {
	Storage       string `json:"storage"`
	TotalBytes    int64  `json:"total_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
	LimitPercent  int    `json:"limit_percent"`
}

type imageStatus struct {
	Versions      []imageVersionView `json:"versions"`
	Building      *int               `json:"building"`
	LastBuild     *imageVersionView  `json:"last_build"`
	NextBuildAt   *time.Time         `json:"next_build_at"`
	UnusedImports []unusedImportView `json:"unused_imports"`
	Disk          *diskUseView       `json:"disk,omitempty"`
	// LeftTemplates are converted build rests (T-061 N1); missing = none.
	LeftTemplates []leftTemplateView `json:"left_templates,omitempty"`
}

type leftTemplateView struct {
	Name    string `json:"name"`
	GuestID string `json:"guest_id"`
}

// imageStatus is the status of a built Image or of an lxc Image (T-085,
// D-233), or nil for a stock VM Image. The driver parts (unused files,
// disk) are left out when the driver cannot read them.
func (s *Server) imageStatus(ctx context.Context, d store.DocRow) (*imageStatus, error) {
	var doc struct {
		Spec struct {
			Type   string          `json:"type"`
			URL    string          `json:"url"`
			SHA256 string          `json:"sha256"`
			SHA512 string          `json:"sha512"`
			Build  json.RawMessage `json:"build"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(d.Body, &doc); err != nil {
		return nil, err
	}
	lxc := doc.Spec.Type == string(wire.KindLXC)
	built := len(doc.Spec.Build) != 0 && string(doc.Spec.Build) != "null"
	if !built && !lxc {
		return nil, nil
	}
	st := &imageStatus{Versions: []imageVersionView{}, UnusedImports: []unusedImportView{}}
	unusedFiles := func() {
		var unused []plan.UnusedImport
		var err error
		if lxc {
			unused, err = s.cfg.Plans.UnusedTemplateFiles(ctx, d.Name, doc.Spec.URL, doc.Spec.SHA512)
		} else {
			unused, err = s.cfg.Plans.UnusedImports(ctx, d.Name, doc.Spec.SHA256)
		}
		if err != nil {
			s.log.Warn("image status: unused files", "image", d.Name, "err", err)
			return
		}
		for _, u := range unused {
			st.UnusedImports = append(st.UnusedImports, unusedImportView{Volume: u.Volume, SizeBytes: u.SizeBytes, Command: u.Command})
		}
	}
	if !built { // a stock lxc Image: only its unused template files
		unusedFiles()
		return st, nil
	}
	versions, err := s.st.ImageVersions(ctx, d.Name)
	if err != nil {
		return nil, err
	}
	byVersion, err := s.targetsOfImage(ctx, d.Name)
	if err != nil {
		return nil, err
	}
	for _, v := range versions { // newest first
		vv := versionView(v)
		if t := byVersion[v.Version]; t != nil {
			vv.Targets = t
		}
		st.Versions = append(st.Versions, vv)
		if v.Status == store.ImageBuilding && st.Building == nil {
			n := v.Version
			st.Building = &n
		}
		if (v.Status == store.ImageReady || v.Status == store.ImageFailed) && st.LastBuild == nil {
			last := vv
			st.LastBuild = &last
		}
	}
	unusedFiles()
	if left, err := s.cfg.Plans.LeftTemplates(ctx, d.Name); err == nil {
		for _, l := range left {
			st.LeftTemplates = append(st.LeftTemplates, leftTemplateView{Name: l.Name, GuestID: l.GuestID})
		}
	} else {
		s.log.Warn("image status: build rests", "image", d.Name, "err", err)
	}
	if next, err := s.cfg.Plans.NextBuildAt(ctx, d.Name); err == nil {
		st.NextBuildAt = utc(next)
	} else {
		s.log.Warn("image status: next build", "image", d.Name, "err", err)
	}
	if u := s.cfg.Plans.DiskUse(ctx); u != nil {
		st.Disk = &diskUseView{Storage: u.Storage, TotalBytes: u.TotalBytes, UsedBytes: u.UsedBytes, ReservedBytes: u.ReservedBytes, LimitPercent: u.LimitPercent}
	}
	return st, nil
}

// targetsOfImage maps a version to the Targets of the head revision that
// were cloned from it: the version marker that the driver reads (D-200).
func (s *Server) targetsOfImage(ctx context.Context, image string) (map[int][]string, error) {
	_, docs, err := s.headDocs(ctx, state.KindTarget)
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	for _, d := range docs {
		var t struct {
			Spec struct {
				Image string `json:"image"`
			} `json:"spec"`
		}
		if json.Unmarshal(d.Body, &t) != nil || t.Spec.Image != image {
			continue
		}
		live, err := s.cfg.Plans.Live(ctx, d.Name)
		if err != nil {
			return nil, err
		}
		if live.ImageVersion > 0 {
			out[live.ImageVersion] = append(out[live.ImageVersion], d.Name)
		}
	}
	return out, nil
}

func versionView(v store.ImageVersion) imageVersionView {
	return imageVersionView{Version: v.Version, Status: v.Status, Trigger: v.Trigger, RunID: v.RunID, Revision: v.RevisionID,
		RecipeSHA256: v.RecipeSHA256, BaseSHA256: v.BaseSHA256, DiskGB: v.DiskGB, StartedAt: v.StartedAt.UTC(),
		FinishedAt: utc(v.FinishedAt), RemovedAt: utc(v.RemovedAt), Targets: []string{}}
}

// --- Plan.images and Run.images ---

type imageBuildPlanView struct {
	Version      int                `json:"version"`
	Trigger      string             `json:"trigger"`
	Upgrade      bool               `json:"upgrade"`
	RecipeSHA256 string             `json:"recipe_sha256"`
	Resources    []resourcePlanView `json:"resources"`
}

type imageRemovalView struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version *int   `json:"version"`
}

type imagePlanView struct {
	Image   string              `json:"image"`
	Build   *imageBuildPlanView `json:"build,omitempty"`
	Removes []imageRemovalView  `json:"removes"`
	Disk    *diskUseView        `json:"disk,omitempty"`
	// Notes are the disk notes of the build, for the person who approves
	// (T-061 review, T-066); missing = none.
	Notes []string `json:"notes,omitempty"`
}

func imagePlanViews(raw json.RawMessage) ([]imagePlanView, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var images []plan.ImagePlan
	if err := json.Unmarshal(raw, &images); err != nil {
		return nil, err
	}
	var out []imagePlanView
	for _, ip := range images {
		v := imagePlanView{Image: ip.Image, Removes: []imageRemovalView{}, Notes: ip.DiskNotes}
		if ip.Build != nil {
			b := &imageBuildPlanView{Version: ip.Build.Version, Trigger: ip.Build.Trigger, Upgrade: ip.Build.Upgrade,
				RecipeSHA256: ip.Build.RecipeSHA256, Resources: []resourcePlanView{}}
			for _, rc := range ip.Build.Resources {
				b.Resources = append(b.Resources, resourcePlanView{Resource: rc.Resource, Action: rc.Action, Risk: rc.Risk,
					Changes: changes(rc.Changes), Unverified: rc.Unverified, After: rc.After})
			}
			v.Build = b
		}
		for _, rm := range ip.Removes {
			rv := imageRemovalView{Kind: rm.Kind, Name: rm.Name}
			if rm.Version > 0 {
				n := rm.Version
				rv.Version = &n
			}
			v.Removes = append(v.Removes, rv)
		}
		if ip.Disk != nil {
			v.Disk = &diskUseView{Storage: ip.Disk.Storage, TotalBytes: ip.Disk.TotalBytes, UsedBytes: ip.Disk.UsedBytes,
				ReservedBytes: ip.Disk.ReservedBytes, LimitPercent: ip.Disk.LimitPercent}
		}
		out = append(out, v)
	}
	return out, nil
}

type imageRunView struct {
	Image   string     `json:"image"`
	Version int        `json:"version"`
	Trigger string     `json:"trigger"`
	Status  string     `json:"status"`
	Steps   []stepView `json:"steps"`
}

// imageLane returns the Image name of a step target "image:<name>".
func imageLane(target string) (string, bool) {
	return strings.CutPrefix(target, "image:")
}

// imageRunViews groups the image lanes of a Run. Version and trigger come
// from image_version, or from the Plan for a lane that ended before its
// version row exists. A lane with removals only shows the newest version.
func (s *Server) imageRunViews(ctx context.Context, run store.Run, lanes map[string]*targetRunView, order []string) ([]imageRunView, error) {
	if len(order) == 0 {
		return nil, nil
	}
	versions, err := s.st.ImageVersionsOfRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	byImage := map[string]store.ImageVersion{}
	for _, v := range versions {
		byImage[v.Image] = v
	}
	planned := map[string]plan.ImagePlan{}
	if p, err := s.st.GetPlan(ctx, run.PlanID); err == nil {
		var images []plan.ImagePlan
		if json.Unmarshal(p.Images, &images) == nil {
			for _, ip := range images {
				planned[ip.Image] = ip
			}
		}
	}
	var out []imageRunView
	for _, name := range order {
		lane := lanes[name]
		v := imageRunView{Image: name, Status: lane.Status, Steps: lane.Steps}
		if iv, ok := byImage[name]; ok {
			v.Version, v.Trigger = iv.Version, iv.Trigger
		} else if ip, ok := planned[name]; ok && ip.Build != nil {
			v.Version, v.Trigger = ip.Build.Version, ip.Build.Trigger
		} else {
			v.Version, v.Trigger = 1, store.TriggerChange
			if all, err := s.st.ImageVersions(ctx, name); err == nil && len(all) > 0 {
				v.Version, v.Trigger = all[0].Version, all[0].Trigger
			}
		}
		out = append(out, v)
	}
	return out, nil
}
