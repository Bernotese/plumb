package wire

// SSH repair line and readiness report (D-383, D-384, ARCHITECTURE §19).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// RepairLinePrefix starts the comment of every repair line. The agent
// removes every line with it that the controller did not list.
const RepairLinePrefix = "plumb-repair-"

// Limits of the repair messages.
const (
	MaxRepairKeys     = 2 // the current key and the previous one
	MaxRepairFrom     = 16
	MaxRepairHostKeys = 8
	MaxRepairLines    = 8
)

// RepairKey is one repair public key: id and "ssh-ed25519 <base64>".
type RepairKey struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

// RepairWatch is WatchBody.Repair.
type RepairWatch struct {
	Keys []RepairKey `json:"keys"`
	// From are the controller addresses, IP literals only (review R4 of
	// #290): sshd matches host names only with UseDNS.
	From []string `json:"from"`
}

// The sshd states of a repair report.
const (
	SSHDOk          = "ok"
	SSHDAbsent      = "absent"
	SSHDRootRefused = "root-refused"
)

// RepairSSHD is what `sshd -T` says.
type RepairSSHD struct {
	Status string `json:"status"`
	Port   int    `json:"port,omitempty"`
}

// RepairReport is Report.Repair: guest data with fixed limits.
type RepairReport struct {
	Lines    []string   `json:"lines"`     // key ids whose line the agent wrote
	SSHD     RepairSSHD `json:"sshd"`      // sshd -T
	HostKeys []string   `json:"host_keys"` // SHA256:… of /etc/ssh/ssh_host_*_key.pub
}

var (
	repairKeyID = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	hostKeyFP   = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

// ValidHostKeyFP reports whether s is an OpenSSH SHA256 fingerprint.
func ValidHostKeyFP(s string) bool { return hostKeyFP.MatchString(s) }

// CheckRepairWatch checks a repair key set before the agent writes a
// line from it: ids, ed25519 keys only, IP literals only in from.
func CheckRepairWatch(w RepairWatch) error {
	if len(w.Keys) > MaxRepairKeys || len(w.From) == 0 || len(w.From) > MaxRepairFrom {
		return errors.New("repair: wrong number of keys or addresses")
	}
	for _, k := range w.Keys {
		if !repairKeyID.MatchString(k.ID) {
			return errors.New("repair: bad key id")
		}
		typ, b64, ok := strings.Cut(k.PublicKey, " ")
		if !ok || typ != "ssh-ed25519" || len(b64) > 128 {
			return errors.New("repair: not an ssh-ed25519 key")
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) != 51 { // string "ssh-ed25519" + 32-byte key
			return errors.New("repair: bad key data")
		}
	}
	for _, f := range w.From {
		if net.ParseIP(f) == nil {
			return fmt.Errorf("repair: from entry %q is not an IP literal", f)
		}
	}
	return nil
}

// RepairLine is the authorized_keys line of one key (D-383).
func RepairLine(k RepairKey, from []string) string {
	return `restrict,from="` + strings.Join(from, ",") + `",command="` + RepairForcedCommand + `" ` +
		k.PublicKey + " " + RepairLinePrefix + k.ID
}

// CheckRepairReport applies the size and schema limits of D-384.
func CheckRepairReport(r *RepairReport) error {
	if r == nil {
		return errors.New("repair report without its body")
	}
	if len(r.Lines) > MaxRepairLines || len(r.HostKeys) > MaxRepairHostKeys {
		return errors.New("repair report: too many lines or host keys")
	}
	for _, l := range r.Lines {
		if !repairKeyID.MatchString(l) {
			return errors.New("repair report: bad line id")
		}
	}
	for _, h := range r.HostKeys {
		if !hostKeyFP.MatchString(h) {
			return errors.New("repair report: bad host key fingerprint")
		}
	}
	switch r.SSHD.Status {
	case SSHDOk, SSHDRootRefused:
		if r.SSHD.Port < 1 || r.SSHD.Port > 65535 {
			return errors.New("repair report: bad sshd port")
		}
	case SSHDAbsent:
		if r.SSHD.Port != 0 {
			return errors.New("repair report: port without sshd")
		}
	default:
		return errors.New("repair report: bad sshd status")
	}
	return nil
}
