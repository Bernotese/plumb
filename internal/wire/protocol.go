package wire

import "encoding/json"

// Paths of the agent protocol on the agent listener (port 8444).
const (
	PathEnroll = "/agent/v1/enroll" // POST EnrollRequest → EnrollResponse
	PathPoll   = "/agent/v1/poll"   // GET ?wait=<seconds> → 204 or Command
	PathReport = "/agent/v1/report" // POST Report → 204
	PathBinary = "/agent/v1/binary" // GET the agent binary
)

// After enrollment the agent sends its credential on every call as
// "Authorization: Bearer <credential>". A 401 means: enroll again (D-038).

// Limits that both sides enforce.
const (
	MaxReportBytes = 1 << 20 // largest accepted Report body
	MaxPollWait    = 30      // longest long poll, in seconds
	OnlineSeconds  = 90      // a target is online when it polled within this time
)

// EnrollRequest is sent once by a new agent.
type EnrollRequest struct {
	Token    string `json:"token"`    // one-time enrollment token
	Hostname string `json:"hostname"` // informational
	Version  string `json:"version"`  // agent version
}

// EnrollResponse gives the agent its identity. The credential is shown
// only here; the controller stores a hash.
type EnrollResponse struct {
	AgentID    string `json:"agent_id"`
	Credential string `json:"credential"`
	// Build is true only for the agent of an image build guest: its token
	// was made by Driver.CreateBuild (D-203). The agent stores the mark in
	// the same atomic write as the credential, and runs CommandSeal only
	// with it (D-207). A normal enrollment clears it.
	Build bool `json:"build,omitempty"`
}

// CommandType names what the controller asks the agent to do.
type CommandType string

const (
	CommandObserve CommandType = "observe" // observe and diff, change nothing
	CommandApply   CommandType = "apply"   // converge the resources
	CommandWatch   CommandType = "watch"   // replace the desired list and watch it for drift
	// CommandSeal makes an image build guest clean for a template (D-205).
	// Only for a link name with the prefix BuildLinkPrefix, and only an
	// agent with the build mark that never got a watch runs it (D-207).
	CommandSeal CommandType = "seal"
)

// BuildLinkPrefix starts the link name of an image build guest:
// "build:<image name>" (D-203). A Target name cannot hold ':'.
const BuildLinkPrefix = "build:"

// Command is the answer to a poll. Body depends on Type:
// ObserveBody, ApplyBody, WatchBody or SealBody.
type Command struct {
	ID   string          `json:"id"`
	Type CommandType     `json:"type"`
	Body json.RawMessage `json:"body"`
}

// ObserveBody asks for the difference between these resources and the guest.
type ObserveBody struct {
	Resources []Resource `json:"resources"`
}

// ApplyBody asks the agent to apply the approved Plan on one target.
// The agent observes again. It changes a resource only as far as Planned
// allows (D-075), and never above MaxRisk.
type ApplyBody struct {
	PlanID    int64      `json:"plan_id"`   // Plan, Run and Issue ids are integers everywhere
	Resources []Resource `json:"resources"` // the full desired list of the target
	Planned   []Planned  `json:"planned"`   // what the approved Plan shows for this target
	MaxRisk   Risk       `json:"max_risk"`
}

// Planned is the approved change of one resource.
type Planned struct {
	Resource string   `json:"resource"`
	Changes  []Change `json:"changes,omitempty"`
	// Unverified: nobody observed the resource (new target, D-066).
	// Only MaxRisk limits it.
	Unverified bool `json:"unverified,omitempty"`
}

// WatchBody sets the full desired list of the target. The agent stores it
// and reports drift against it.
type WatchBody struct {
	Resources []Resource `json:"resources"`
}

// SealBody starts seal. It holds no path, command or user name: what seal
// does is fixed in the agent (D-205).
type SealBody struct {
	PlanID int64 `json:"plan_id"`
	// Upgraded: the build guest ran the package upgrade (D-204), so seal
	// checks that nothing is left to upgrade.
	Upgraded bool `json:"upgraded"`
}

// The result of seal has one Item per step, in this order, with the
// resource id "seal:<step>" (SealItemID). Status is applied, skipped (the
// step does not apply to this guest, for example no dracut) or failed;
// a failed step ends seal, and the later steps are skipped. An agent that
// may not seal answers one Item SealItemID(SealStepGuard) with
// StatusRefused and changes nothing (D-207). After the controller took the
// result, the agent removes its own files, runs sync and powers the guest
// off; that part has no Item (D-207, review B1).
const (
	SealStepGuard     = "guard"      // build mark set and no watch ever seen
	SealStepInitramfs = "initramfs"  // D-192: dracut without network modules; skipped without dracut
	SealStepUpgrade   = "upgrade"    // nothing left to upgrade; skipped when not Upgraded
	SealStepLogStop   = "log-stop"   // stop rsyslog, journalctl --relinquish-var
	SealStepCloudInit = "cloud-init" // remove cloud-init.disabled, cloud-init clean
	SealStepHostKeys  = "host-keys"  // remove /etc/ssh/ssh_host_* (D-185)
	SealStepBuildUser = "build-user" // userdel -r -f plumb-build, its sudoers file
	SealStepSeed      = "seed"       // systemd random seed, credential.secret
	SealStepLogs      = "logs"       // journal, /var/log, caches, /tmp, history; no symlink follow
	SealStepTrim      = "trim"       // fstrim
)

// SealSteps is the fixed order of the seal steps.
var SealSteps = []string{
	SealStepGuard, SealStepInitramfs, SealStepUpgrade, SealStepLogStop, SealStepCloudInit,
	SealStepHostKeys, SealStepBuildUser, SealStepSeed, SealStepLogs, SealStepTrim,
}

// SealItemID is the Item resource id of a seal step.
func SealItemID(step string) string { return "seal:" + step }

// BodyResources returns the resources of an observe, apply or watch body
// (value or pointer), or nil.
func BodyResources(body any) []Resource {
	switch b := body.(type) {
	case ObserveBody:
		return b.Resources
	case *ObserveBody:
		return b.Resources
	case ApplyBody:
		return b.Resources
	case *ApplyBody:
		return b.Resources
	case WatchBody:
		return b.Resources
	case *WatchBody:
		return b.Resources
	}
	return nil
}

// ReportType names the kind of a Report.
type ReportType string

const (
	ReportResult   ReportType = "result"   // final answer to a command
	ReportProgress ReportType = "progress" // step update while an apply runs
	ReportDrift    ReportType = "drift"    // a watched resource differs
	ReportHello    ReportType = "hello"    // sent after start and after a reconnect
	// ReportAddrs carries a changed address list (D-212). It is not a
	// second Hello: the controller counts Hellos to see a restarted agent.
	ReportAddrs ReportType = "addrs"
)

// Report is every message from the agent after enrollment.
type Report struct {
	Type      ReportType `json:"type"`
	CommandID string     `json:"command_id,omitempty"` // for result and progress
	Items     []Item     `json:"items,omitempty"`
	Hello     *Hello     `json:"hello,omitempty"` // only for hello
	Error     string     `json:"error,omitempty"` // the command failed as a whole
	// Addrs and AddrsTruncated are only for addrs. A missing list means
	// an empty list: the guest has no address to report.
	Addrs          []string `json:"addrs,omitempty"`
	AddrsTruncated bool     `json:"addrs_truncated,omitempty"`
}

// Hello describes the agent and its guest.
type Hello struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"` // for example "ubuntu 26.04"
	// Addrs is the address list of the guest (D-212, CheckAddrs). A new
	// agent always sends it, also empty ([]). A missing field or null
	// (nil) means "this agent does not report addresses" (an old agent,
	// D-217): the controller sets the stored list to unknown.
	Addrs []string `json:"addrs"`
	// AddrsTruncated: the guest has more than MaxAddrs addresses; Addrs
	// holds the first MaxAddrs in sort order.
	AddrsTruncated bool `json:"addrs_truncated,omitempty"`
}

// Status is the state of one resource in a Report.
type Status string

// The states of a run step in the database and in the API are the same
// words: pending, running, unchanged, applied, failed, refused, skipped.
const (
	StatusPending   Status = "pending"   // controller only: the step has not started
	StatusUnchanged Status = "unchanged" // the guest equals the desired state
	StatusDiffers   Status = "differs"   // observe or drift: Changes lists the differences
	StatusRunning   Status = "running"   // progress: apply started
	StatusApplied   Status = "applied"   // apply made the Changes
	StatusFailed    Status = "failed"    // apply or observe failed, see Output
	StatusRefused   Status = "refused"   // the difference is not in the approved Plan, or a change is above max_risk; nothing of this resource was applied
	StatusSkipped   Status = "skipped"   // not applied: an earlier resource of the same Block use, or a resource that it comes after (Resource.After), did not end unchanged or applied; or the apply ended first. Output names the cause (D-170)
)

// Item is the report for one resource.
type Item struct {
	Resource string   `json:"resource"` // resource id
	Status   Status   `json:"status"`
	Changes  []Change `json:"changes,omitempty"`
	Output   string   `json:"output,omitempty"` // short, for humans; never a secret
}
