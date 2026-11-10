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
}

// CommandType names what the controller asks the agent to do.
type CommandType string

const (
	CommandObserve CommandType = "observe" // observe and diff, change nothing
	CommandApply   CommandType = "apply"   // converge the resources
	CommandWatch   CommandType = "watch"   // replace the desired list and watch it for drift
)

// Command is the answer to a poll. Body depends on Type:
// ObserveBody, ApplyBody or WatchBody.
type Command struct {
	ID   string          `json:"id"`
	Type CommandType     `json:"type"`
	Body json.RawMessage `json:"body"`
}

// ObserveBody asks for the difference between these resources and the guest.
type ObserveBody struct {
	Resources []Resource `json:"resources"`
}

// ApplyBody asks the agent to converge the resources of an approved Plan.
// The agent observes again and refuses every change above MaxRisk.
type ApplyBody struct {
	PlanID    int64      `json:"plan_id"`
	Resources []Resource `json:"resources"`
	MaxRisk   Risk       `json:"max_risk"`
}

// WatchBody sets the full desired list of the target. The agent stores it
// and reports drift against it.
type WatchBody struct {
	Resources []Resource `json:"resources"`
}

// ReportType names the kind of a Report.
type ReportType string

const (
	ReportResult   ReportType = "result"   // final answer to a command
	ReportProgress ReportType = "progress" // step update while an apply runs
	ReportDrift    ReportType = "drift"    // a watched resource differs
	ReportHello    ReportType = "hello"    // sent after start and after a reconnect
)

// Report is every message from the agent after enrollment.
type Report struct {
	Type      ReportType `json:"type"`
	CommandID string     `json:"command_id,omitempty"` // for result and progress
	Items     []Item     `json:"items,omitempty"`
	Hello     *Hello     `json:"hello,omitempty"` // only for hello
	Error     string     `json:"error,omitempty"` // the command failed as a whole
}

// Hello describes the agent and its guest.
type Hello struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"` // for example "ubuntu 26.04"
}

// Status is the state of one resource in a Report.
type Status string

const (
	StatusUnchanged Status = "unchanged" // the guest equals the desired state
	StatusDiffers   Status = "differs"   // observe or drift: Changes lists the differences
	StatusRunning   Status = "running"   // progress: apply started
	StatusApplied   Status = "applied"   // apply made the Changes
	StatusFailed    Status = "failed"    // apply or observe failed, see Output
	StatusRefused   Status = "refused"   // a change is above max_risk; nothing was applied
	StatusSkipped   Status = "skipped"   // an earlier resource of the same Block failed
)

// Item is the report for one resource.
type Item struct {
	Resource string   `json:"resource"` // resource id
	Status   Status   `json:"status"`
	Changes  []Change `json:"changes,omitempty"`
	Output   string   `json:"output,omitempty"` // short, for humans; never a secret
}
