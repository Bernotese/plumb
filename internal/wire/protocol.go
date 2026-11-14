package wire

import "encoding/json"

// Paths of the agent protocol on the agent listener (port 8444).
const (
	PathEnroll = "/agent/v1/enroll" // POST EnrollRequest → EnrollResponse
	PathPoll   = "/agent/v1/poll"   // GET ?wait=<seconds> → 204 or Command
	PathReport = "/agent/v1/report" // POST Report → 204
	PathBinary = "/agent/v1/binary" // GET the agent binary
	// PathInstallScript serves the install script of this controller
	// (D-383, D-387): no auth, rate-limited like PathBinary.
	PathInstallScript = "/agent/v1/install.sh"
)

// The repair bundle (D-383, ARCHITECTURE §19): a ustar archive on the
// SSH stdin with exactly RepairMembers, at most MaxRepairBundle bytes.
// The forced command of the repair line extracts only these names.
const (
	MaxRepairBundle = 64 << 20
	// RepairTokenFile is the one-shot enrollment token (D-390): written
	// 0600 by the install script, it wins over the stored credential at
	// the agent's next start and is deleted after its use.
	RepairTokenFile = "/etc/plumb-agent/enroll.token"
)

// RepairForcedCommand is <FORCED> of the repair line (D-383): fixed text
// of the agent, with no double quote. It reads at most MaxRepairBundle
// bytes, extracts exactly RepairMembers into a 0700 dir, checks the
// binary and runs the bundled install.sh.
const RepairForcedCommand = `umask 077; d=/run/plumb-repair; rm -rf $d; mkdir -m 700 $d && head -c 67108864 | tar -x -f - -C $d --no-same-owner --no-same-permissions install.sh plumb-agent plumb-agent.sha256 ca.pem enroll.json && cd $d && sha256sum -c --status plumb-agent.sha256 && sh ./install.sh; r=$?; cd /; rm -rf $d; exit $r`

// RepairMembers are the members of the repair bundle, in archive order.
var RepairMembers = []string{"install.sh", "plumb-agent", "plumb-agent.sha256", "ca.pem", "enroll.json"}

// RepairEnroll is enroll.json of the repair bundle: the agent URL and a
// new one-time enrollment token.
type RepairEnroll struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

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
	// with it (D-207). The agent takes Build=true only at a first
	// enrollment from an empty state (no credential, never a watch); a
	// later enrollment with Build=true leaves the mark unset. A normal
	// enrollment clears the build mark. No enrollment ever clears the
	// watch mark: an agent that got a watch never seals (T-060 tests both).
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
	// CommandUpdate replaces the agent binary (D-258). Body: UpdateBody.
	// An agent from before slice 3 answers it with "unknown command type";
	// it does not list "update" in Capabilities.Commands, so the controller
	// never sends it there (CanUpdate).
	CommandUpdate CommandType = "update"
	// CommandGrow grows the root partition and file system of a VM after
	// the driver grew its disk (D-291, D-299). Body: GrowBody. An agent
	// that does not list "grow" in Capabilities.Commands never gets it: a
	// Plan that grows the disk of a VM with such an agent is refused by
	// the gate (GateCommandPrefix). The agent of this build lists it only
	// when T-117 adds the handler (AgentCommands).
	CommandGrow CommandType = "grow"
	// CommandUnbind ends the agent in a guest that Plumb keeps (D-388):
	// remove the repair lines, the unit, the binary and /etc/plumb-agent.
	// No body. The agent handler comes with T-193; an agent that does not
	// list it in Capabilities.Commands never gets it.
	CommandUnbind CommandType = "unbind"
)

// BuildLinkPrefix starts the link name of an image build guest:
// "build:<image name>" (D-203). A Target name cannot hold ':'.
const BuildLinkPrefix = "build:"

// Command is the answer to a poll. Body depends on Type:
// ObserveBody, ApplyBody, WatchBody, SealBody, UpdateBody or GrowBody
// (unbind has none).
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
	// Container: the build guest is an lxc container (D-253): seal skips
	// cloud-init, initramfs and trim, runs the upgrade itself (D-252),
	// empties /etc/machine-id, adds the SSH host key unit and keeps the
	// first-boot installer.
	Container bool `json:"container,omitempty"`
}

// UpdateBody asks the agent to replace its own binary (D-258). It holds no
// URL: the agent fetches PathBinary from the controller that it pinned at
// enrollment, over the same TLS link, and refuses a file whose SHA-256
// differs from SHA256.
//
// The agent then runs "<new binary> self-test" (exit 0 and nothing else),
// renames its own binary to "<path>.prev", renames the new one over it,
// writes the update marker, sends the result and exits for a systemd
// restart. The result has one Item AgentBinaryID: StatusApplied when the
// swap is done, StatusFailed with Output when a step before the swap
// failed (download, hash, self-test, disk), and the agent keeps running
// its old binary. StatusApplied does not pass the step: the controller
// passes it only when a Hello with Hello.BinarySHA256 == SHA256 arrives
// within UpdateHelloSeconds. Inside the guest, a crash loop or no
// successful Hello within UpdateRevertSeconds restores ".prev" (D-258).
type UpdateBody struct {
	PlanID int64 `json:"plan_id"`
	// SHA256 is the lowercase hex SHA-256 of the binary that PathBinary
	// serves now: the agent of this controller build.
	SHA256 string `json:"sha256"`
}

// Times of the agent update (D-258).
const (
	UpdateHelloSeconds  = 120 // the controller waits this long for the Hello of the new binary
	UpdateRevertSeconds = 300 // the agent restores .prev when it had no successful Hello by then
)

// AgentItemType is the type part of AgentBinaryID. Like SealItemType it is
// no resource type: no Block can name it, and link takes an Item of it only
// as the answer of an outstanding update command.
const AgentItemType = "agent"

// AgentBinaryID is the resource id of the "update agent" Plan step and of
// the Item in the result of an update command (D-257). Its one change is
// {attr: "version", from: <agent version>, to: <controller version>,
// risk: disruptive}.
const AgentBinaryID = AgentItemType + ":binary"

// AttrAgentVersion is the attribute of the change of AgentBinaryID.
const AttrAgentVersion = "version"

// GrowBody asks the agent to grow the root file system to the disk that
// the driver resized (D-299). It holds no device or path: the agent finds
// the device and partition of "/" itself and refuses anything else.
//
// The agent waits up to GrowWaitSeconds for the kernel to see a disk of
// at least MinBytes (it rescans the SCSI device), then grows the
// partition of "/" (growpart) and the file system (resize2fs or
// xfs_growfs). The result has one Item GrowItemID: StatusApplied when it
// grew, StatusUnchanged when the partition already fills the disk,
// StatusFailed with Output when "/" is not on the last partition of its
// disk, is on LVM, RAID or a file system other than ext4 or xfs, the tool
// is missing, or the disk stays smaller than MinBytes. A failed grow
// leaves the guest as it was: the bigger disk stays (it cannot shrink),
// and a later grow command can finish the job.
type GrowBody struct {
	PlanID   int64 `json:"plan_id"`
	MinBytes int64 `json:"min_bytes"` // the new disk size in bytes (GiB × 2^30)
}

// GrowWaitSeconds is how long the agent waits for the kernel to see the
// grown disk.
const GrowWaitSeconds = 30

// GrowItemType is the type part of GrowItemID. Like SealItemType it is no
// resource type: link takes an Item of it only as the answer of an
// outstanding grow command.
const GrowItemType = "grow"

// GrowItemID is the Item of the result of a grow command. In a Run the
// grow is part of the step guest:config (change disk); the controller
// copies the Output of a failed grow into that step.
const GrowItemID = GrowItemType + ":root"

// Safe apply (D-293, D-300). For a resource type in SafeApplyTypes the
// agent, inside an apply:
//
//  1. checks every value (ports, ranges, CIDRs) and builds the new rules;
//     a bad value fails the Item with nothing changed;
//  2. arms the rollback timer: a transient systemd timer
//     (systemd-run --on-active=120s, unit plumb-agent-firewall-rollback)
//     that loads the confirmed state (or, before the first confirm,
//     deletes the table), so a dead agent cannot keep unconfirmed rules;
//  3. writes the marker <state>/safe-apply.json (version 1: command id,
//     resource id, deadline);
//  4. applies the new state live, but keeps the confirmed state where the
//     guest loads it at boot (firewall: /etc/plumb-agent/firewall.nft, a
//     protected agent path, loaded by plumb-agent-firewall.service);
//  5. closes its idle connections and sends a Report of type
//     ReportConfirm for the resource over a new TCP and TLS connection;
//  6. on a 2xx answer within SafeApplySeconds of the change, makes the new
//     state the confirmed one, stops the rollback timer, removes the
//     marker, and reports the Item StatusApplied;
//  7. else puts the confirmed state back live, stops the timer, removes
//     the marker, and reports the Item StatusFailed with RolledBack.
//
// An agent that starts and finds the marker (it died in the window, or
// the guest rebooted) puts the confirmed state back first, stops the
// timer, and sends a result for the command id of the marker: the Item
// StatusFailed with RolledBack, the other resources of the apply
// StatusFailed with "the agent restarted during the apply". The
// controller still waits for that apply, so the Run step ends failed and
// rolled back, not at the apply timeout.
const SafeApplySeconds = 90

// SafeApplyTypes are the resource types with safe apply.
var SafeApplyTypes = []string{"firewall", "network"}

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

// SealItemType is the type part of a seal Item id. It is no resource type:
// link takes such an Item only in a result of a build link, as the answer
// of an outstanding seal command.
const SealItemType = "seal"

// SealItemID is the Item resource id of a seal step.
func SealItemID(step string) string { return SealItemType + ":" + step }

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
	// ReportConfirm is the round trip of a safe apply (D-300): Items has
	// one Item with the resource id and StatusRunning, CommandID the
	// apply. It must reach the controller over a new connection. The
	// controller answers 204 when the command is the open apply of this
	// agent and the resource is in it, else 409; it stores nothing. It is
	// no progress: the Run step stays running.
	ReportConfirm ReportType = "confirm"
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
	// AddrsUnknown is only for addrs, without a list: the agent cannot
	// read its addresses any more, so the list it sent before is stale.
	// The controller sets the list to unknown (D-212).
	AddrsUnknown bool `json:"addrs_unknown,omitempty"`
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
	// BinarySHA256 is the lowercase hex SHA-256 of the running agent
	// binary (/proc/self/exe), read once at start. Empty from an agent
	// before slice 3. The controller compares it with the SHA-256 of the
	// binary that it serves to tell whether an update is available, and
	// to pass an update step (D-258).
	BinarySHA256 string `json:"binary_sha256,omitempty"`
	// Capabilities lists what the agent can do (D-259). nil (an agent
	// before slice 3) means BaselineCapabilities.
	Capabilities *Capabilities `json:"capabilities,omitempty"`
	// Reverted is set when the agent restored ".prev" after an update
	// (crash loop or no successful Hello within UpdateRevertSeconds). The
	// agent sends it in every Hello until it gets the next update command.
	// The controller fails an open update step with it and shows it in the
	// Target status. nil: no revert since the last update command.
	Reverted *UpdateRevert `json:"reverted,omitempty"`
	// LiveWatch says for which resource types the agent sees ip and nft
	// hand changes at once by netlink (D-367). A false type is found only
	// by the full check. nil: an agent before slice 6 (full check only).
	LiveWatch *LiveWatch `json:"live_watch,omitempty"`
}

// LiveWatch is the live drift watch of an agent per resource type (D-367).
type LiveWatch struct {
	Network  bool `json:"network"`
	Firewall bool `json:"firewall"`
}

// UpdateRevert describes a failed update that the agent rolled back.
// It is guest text: before the controller stores or shows it, it keeps
// Reason only when it is RevertCrashLoop or RevertNoHello (else it drops
// the whole value), At only when it parses as RFC 3339 (else null), From
// and To only up to MaxRevertVersion bytes of printable ASCII (else cut
// and marked), and ToSHA256 only when it is 64 lowercase hex digits.
type UpdateRevert struct {
	From string `json:"from"` // version of the restored binary (now running)
	To   string `json:"to"`   // version that failed
	// ToSHA256 is the SHA-256 of the failed binary, so the controller can
	// tell it from a later build with the same version text.
	ToSHA256 string `json:"to_sha256"`
	Reason   string `json:"reason"` // "crash-loop" or "no-hello"
	At       string `json:"at"`     // RFC 3339, UTC, guest clock
}

// MaxRevertVersion is the longest From or To of an UpdateRevert that the
// controller keeps.
const MaxRevertVersion = 64

// The reasons of UpdateRevert.
const (
	RevertCrashLoop = "crash-loop"
	RevertNoHello   = "no-hello"
)

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
	// RolledBack: a safe apply (D-300) changed the resource, got no round
	// trip within SafeApplySeconds and put the confirmed state back. Only
	// with StatusFailed.
	RolledBack bool `json:"rolled_back,omitempty"`
}
