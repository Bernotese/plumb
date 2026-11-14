// Package agentinstall holds the agent install script of this controller
// and builds the repair bundle (D-383, D-387, ARCHITECTURE §19). The
// script and the member list are fixed by the Plumb release: nothing of
// an API request reaches the bundle.
package agentinstall

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	_ "embed"

	"github.com/Bernotese/plumb/internal/wire"
)

//go:embed install.sh
var script []byte

// Script returns the install script (GET wire.PathInstallScript and the
// bundle member install.sh).
func Script() []byte { return script }

// Bundle is the input of a repair bundle. Every field comes from the
// controller: its served agent binary, its CA, its agent URL and a new
// one-time token.
type Bundle struct {
	Agent []byte
	CA    []byte
	URL   string
	Token string
}

// mtime of every member: fixed, so the same inputs give the same bytes.
var mtime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// check refuses an input that the script would refuse, so a broken
// bundle never leaves the controller.
func (b Bundle) check() error {
	if len(b.Agent) == 0 {
		return errors.New("agentinstall: no agent binary in this build")
	}
	u, err := url.Parse(b.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil ||
		strings.ContainsAny(u.Host, "\"' \\") {
		return fmt.Errorf("agentinstall: the agent URL must be https://<host>:<port>")
	}
	if b.Token == "" || strings.Trim(b.Token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return errors.New("agentinstall: the token is not base64url")
	}
	if !strings.Contains(string(b.CA), "-----BEGIN CERTIFICATE-----") || strings.ContainsAny(string(b.CA), "\"\\") {
		return errors.New("agentinstall: the CA is not a PEM certificate")
	}
	return nil
}

// members returns the member bodies in the order of wire.RepairMembers.
func (b Bundle) members() ([][]byte, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b.Agent)
	enroll, err := json.Marshal(wire.RepairEnroll{URL: b.URL, Token: b.Token})
	if err != nil {
		return nil, err
	}
	// sha256sum -c format: "<hex>  <name>".
	shaLine := hex.EncodeToString(sum[:]) + "  plumb-agent\n"
	ca := strings.TrimRight(strings.ReplaceAll(string(b.CA), "\r\n", "\n"), "\n") + "\n"
	// The order of wire.RepairMembers.
	return [][]byte{script, b.Agent, []byte(shaLine), []byte(ca), append(enroll, '\n')}, nil
}

// WriteBundle writes the repair bundle: a ustar archive with exactly
// wire.RepairMembers, at most wire.MaxRepairBundle bytes.
func WriteBundle(w io.Writer, b Bundle) error {
	bodies, err := b.members()
	if err != nil {
		return err
	}
	size := int64(1024) // end marker
	for _, body := range bodies {
		size += 512 + (int64(len(body))+511)/512*512
	}
	if size > wire.MaxRepairBundle {
		return fmt.Errorf("agentinstall: the bundle is %d bytes, over the limit of %d", size, wire.MaxRepairBundle)
	}
	tw := tar.NewWriter(w)
	for i, name := range wire.RepairMembers {
		mode := int64(0o600)
		if name == "plumb-agent" || name == "install.sh" {
			mode = 0o700
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(bodies[i])),
			Uname: "root", Gname: "root", ModTime: mtime, Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := tw.Write(bodies[i]); err != nil {
			return err
		}
	}
	return tw.Close()
}
