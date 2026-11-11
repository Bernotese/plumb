package plan

// Scheduled image builds (T-063, D-201, D-210): a simple schedule (day,
// week or month plus a time in UTC), planned and approved by "system".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// PrincipalSystem approves scheduled builds, only inside the engine: no
// API session or token has this name (D-101, D-201).
const PrincipalSystem = "system"

// Schedule is spec.schedule of a built Image (image.json).
type Schedule struct {
	Every   string `json:"every"` // day | week | month
	At      string `json:"at"`    // HH:MM, UTC
	Weekday string `json:"weekday,omitempty"`
	Day     int    `json:"day,omitempty"` // 1 … 28
}

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

func parseSchedule(raw json.RawMessage) (Schedule, error) {
	var s Schedule
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	if _, err := time.Parse("15:04", s.At); err != nil {
		return s, fmt.Errorf("schedule: bad time %q", s.At)
	}
	switch s.Every {
	case "day":
	case "week":
		if _, ok := weekdays[s.Weekday]; !ok {
			return s, fmt.Errorf("schedule: bad weekday %q", s.Weekday)
		}
	case "month":
		if s.Day < 1 || s.Day > 28 {
			return s, fmt.Errorf("schedule: bad day %d", s.Day)
		}
	default:
		return s, fmt.Errorf("schedule: bad every %q", s.Every)
	}
	return s, nil
}

// LastSlot is the newest slot at or before now (UTC).
func (s Schedule) LastSlot(now time.Time) time.Time {
	now = now.UTC()
	at, _ := time.Parse("15:04", s.At)
	h, m := at.Hour(), at.Minute()
	switch s.Every {
	case "week":
		d := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
		back := (int(d.Weekday()) - int(weekdays[s.Weekday]) + 7) % 7
		d = d.AddDate(0, 0, -back)
		if d.After(now) {
			d = d.AddDate(0, 0, -7)
		}
		return d
	case "month":
		d := time.Date(now.Year(), now.Month(), s.Day, h, m, 0, 0, time.UTC)
		if d.After(now) {
			d = d.AddDate(0, -1, 0) // day ≤ 28: no month overflow
		}
		return d
	default: // day
		d := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
		if d.After(now) {
			d = d.AddDate(0, 0, -1)
		}
		return d
	}
}

// NextSlot is the first slot after now (UTC).
func (s Schedule) NextSlot(now time.Time) time.Time {
	last := s.LastSlot(now)
	switch s.Every {
	case "week":
		return last.AddDate(0, 0, 7)
	case "month":
		return last.AddDate(0, 1, 0)
	default:
		return last.AddDate(0, 0, 1)
	}
}

// NextBuildAt returns the next slot of a built Image, or the zero time
// when it has no schedule (ImageStatus.next_build_at).
func (e *Engine) NextBuildAt(ctx context.Context, image string) (time.Time, error) {
	_, set, err := e.base(ctx)
	if err != nil {
		return time.Time{}, err
	}
	info, ok := set.ImageInfo(image)
	if !ok || !info.Built || len(info.Schedule) == 0 {
		return time.Time{}, nil
	}
	s, err := parseSchedule(info.Schedule)
	if err != nil {
		return time.Time{}, err
	}
	return s.NextSlot(e.cfg.Now()), nil
}

// RunSchedules checks the schedules every interval (one minute in serve)
// until ctx ends.
func (e *Engine) RunSchedules(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := e.CheckSchedules(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("image schedules", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckSchedules runs one round (D-210). Per built Image with a schedule,
// in name order: a slot is due when it is past and newer than last_slot.
// Only the newest past slot counts, so after a long stop one build runs,
// not one per missed slot. A schedule with no last_slot (new) starts with
// the next slot. A due slot is used up before its Plan is made: a failed
// or stale build is not tried again before the next slot, and a slot
// that meets a running build of the Image is skipped. Scheduled builds run
// one after the other: while one runs, the next due slot waits.
func (e *Engine) CheckSchedules(ctx context.Context) error {
	head, err := e.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	set, err := e.load(ctx, head.ID)
	if err != nil {
		return err
	}
	now := e.cfg.Now().UTC()
	var errs []error
	for _, name := range set.BuiltImages() {
		info, _ := set.ImageInfo(name)
		if len(info.Schedule) == 0 {
			continue
		}
		s, err := parseSchedule(info.Schedule)
		if err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", name, err))
			continue
		}
		slot := s.LastSlot(now)
		last, err := e.st.ImageScheduleSlot(ctx, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if last.IsZero() { // a new schedule starts with the next slot
			errs = append(errs, e.st.SetImageScheduleSlot(ctx, name, now))
			continue
		}
		if !slot.After(last) {
			continue
		}
		if e.scheduledRunning() {
			return errors.Join(errs...) // one after the other: wait for the next round
		}
		// The slot is used up now, whatever happens next.
		if err := e.st.SetImageScheduleSlot(ctx, name, slot); err != nil {
			errs = append(errs, err)
			continue
		}
		if busy, err := e.imageBusy(ctx, name); err != nil || busy {
			if err == nil {
				e.log.Info("scheduled build skipped: a build of the Image runs", "image", name, "slot", slot)
			}
			errs = append(errs, err)
			continue
		}
		if err := e.scheduledBuild(ctx, name, slot); err != nil {
			e.log.Error("scheduled build", "image", name, "slot", slot, "err", err)
		}
	}
	return errors.Join(errs...)
}

// imageBusy reports whether a build or retention of the Image runs.
func (e *Engine) imageBusy(ctx context.Context, image string) (bool, error) {
	e.mu.Lock()
	busy := e.busy[imageSlot(image)] > 0
	e.mu.Unlock()
	if busy {
		return true, nil
	}
	building, err := e.st.BuildingImageVersions(ctx)
	if err != nil {
		return false, err
	}
	for _, v := range building {
		if v.Image == image {
			return true, nil
		}
	}
	return false, nil
}

// scheduledBuild plans the build of a due slot as system and approves it
// with the system rule.
func (e *Engine) scheduledBuild(ctx context.Context, image string, slot time.Time) error {
	p, err := e.ProposeBuild(ctx, image, store.TriggerSchedule,
		fmt.Sprintf("scheduled build (slot %s)", slot.Format("2006-01-02 15:04 UTC")), PrincipalSystem)
	if err != nil {
		return err
	}
	// Marked before the approval, by Plan: a fast Run may end before
	// Approve returns.
	e.mu.Lock()
	if e.schedPlans == nil {
		e.schedPlans = map[int64]bool{}
	}
	e.schedPlans[p.ID] = true
	e.mu.Unlock()
	if _, err := e.Approve(ctx, p.ID, PrincipalSystem, SystemCheck); err != nil {
		e.scheduledDone(p.ID)
		// No pending scheduled Plan may wait for a later approval by a
		// token or a person (review S1 of #138): it is stale at once.
		e.markStale(context.WithoutCancel(ctx), p.ID)
		return err
	}
	return nil
}

// scheduledRunning reports whether a scheduled build runs now.
func (e *Engine) scheduledRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.schedPlans) > 0
}

// scheduledDone ends a scheduled build (its Run ended, or it never
// started).
func (e *Engine) scheduledDone(planID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.schedPlans, planID)
}

// ErrNotSystemPlan is returned when the system rule refuses a Plan.
var ErrNotSystemPlan = errors.New("plan: system approves only a scheduled build with no document change and no human-only reason")

// SystemCheck is the approval rule of system (D-201): author system,
// origin build, every image part a build with trigger schedule, no
// document change, no Target, no human-only reason. The engine uses it; no API route can (the API
// gives no principal the name system).
func SystemCheck(p store.Plan) error {
	if p.Author != PrincipalSystem || p.Origin != OriginBuild || len(p.HumanOnly) > 0 {
		return ErrNotSystemPlan
	}
	var prop Proposal
	if err := json.Unmarshal(p.Proposal, &prop); err != nil || len(prop.Put) > 0 || len(prop.Delete) > 0 ||
		len(prop.Targets) > 0 || len(prop.Reenroll) > 0 {
		return ErrNotSystemPlan
	}
	var targets []TargetPlan
	if err := json.Unmarshal(p.Changes, &targets); err != nil || len(targets) > 0 {
		return ErrNotSystemPlan
	}
	images, err := imagePlans(p)
	if err != nil || len(images) == 0 {
		return ErrNotSystemPlan
	}
	for _, ip := range images {
		if ip.Build == nil || ip.Build.Trigger != store.TriggerSchedule {
			return ErrNotSystemPlan
		}
	}
	return nil
}

// resetSchedules starts a new or changed schedule with its next slot
// (D-210): after a commit, an Image whose schedule is new or changed gets
// last_slot = now.
func (e *Engine) resetSchedules(ctx context.Context, base, next *state.Set) {
	now := e.cfg.Now().UTC()
	for _, name := range next.BuiltImages() {
		n, _ := next.ImageInfo(name)
		if len(n.Schedule) == 0 {
			continue
		}
		if b, ok := base.ImageInfo(name); ok && b.Built && string(b.Schedule) == string(n.Schedule) {
			continue
		}
		if err := e.st.SetImageScheduleSlot(ctx, name, now); err != nil {
			e.log.Error("reset schedule", "image", name, "err", err)
		}
	}
}
