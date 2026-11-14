package repair

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// maxDialAddrs bounds the reported addresses that a Run tries (review R5
// of #290).
const maxDialAddrs = 8

// ErrHostKey: the guest showed a host key that is not pinned. Nothing was
// sent (no SSH TOFU, D-384).
var ErrHostKey = errors.New("repair: the guest's host key is not pinned; nothing was sent")

// dialAddrs returns the addresses that a Run may dial: the static address
// of the Target, or the reported addresses that lie inside one of the
// controller's own networks (on-link). A routed address is never dialled.
func (s *Service) dialAddrs(static string, reported []string) []string {
	if ip := net.ParseIP(static); ip != nil {
		return []string{ip.String()}
	}
	var out []string
	for _, r := range reported {
		ip, _, err := net.ParseCIDR(r)
		if err != nil {
			ip = net.ParseIP(r)
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			continue
		}
		for _, n := range s.cfg.OwnNets {
			if n.Contains(ip) {
				out = append(out, ip.String())
				break
			}
		}
		if len(out) == maxDialAddrs {
			break
		}
	}
	return out
}

// Send implements plan.RepairTransport (D-385): root, public key auth
// with the current repair key, the host key only from the pins (or the
// one that the approved Plan accepts, which it then pins), no PTY, no
// forwarding, no env, no subsystem. The bundle goes on stdin.
func (s *Service) Send(ctx context.Context, rs plan.RepairSend) (int, bool, error) {
	guest, err := s.guestOf(ctx, rs.Target)
	if err != nil || guest == "" {
		return 0, false, fmt.Errorf("repair: %s has no guest", rs.Target)
	}
	st, err := s.Status(ctx, rs.Target)
	if err != nil {
		return 0, false, err
	}
	if st == nil || st.Port == nil {
		return 0, false, errors.New("repair: no sshd port from the agent")
	}
	keys, err := s.cfg.Store.RepairKeys(ctx)
	if err != nil || len(keys) == 0 {
		return 0, false, errors.New("repair: no repair key")
	}
	signer, err := s.signer(keys[0])
	if err != nil {
		return 0, false, err
	}
	pins := st.HostKeys
	if rs.AcceptHostKey != "" && !wire.ValidHostKeyFP(rs.AcceptHostKey) {
		return 0, false, errors.New("repair: bad accepted fingerprint")
	}
	addrs := s.dialAddrs(rs.StaticIP, rs.Reported)
	if len(addrs) == 0 {
		return 0, false, errors.New("repair: no address to dial: no static address and no reported address on a controller network")
	}
	var lastErr error
	for _, a := range addrs {
		exit, sent, err := s.session(ctx, net.JoinHostPort(a, strconv.Itoa(*st.Port)), signer, pins, rs, guest)
		if err == nil || sent || errors.Is(err, ErrHostKey) {
			return exit, sent, err
		}
		lastErr = err
	}
	return 0, false, lastErr
}

// session runs one SSH session on addr.
func (s *Service) session(ctx context.Context, addr string, signer ssh.Signer, pins []string, rs plan.RepairSend, guest string) (int, bool, error) {
	dctx, cancel := context.WithTimeout(ctx, s.cfg.ConnectTimeout)
	conn, err := s.cfg.Dial(dctx, addr)
	cancel()
	if err != nil {
		return 0, false, fmt.Errorf("repair: connect %s: %w", addr, err)
	}
	defer conn.Close()
	// The handshake (with the banner and the version line of the guest) is
	// bounded in time; the library bounds the sizes.
	conn.SetDeadline(time.Now().Add(s.cfg.ConnectTimeout))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var hostKeyErr error
	var accepted atomic.Bool
	cfg := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			if contains(pins, fp) {
				return nil
			}
			if rs.AcceptHostKey != "" && fp == rs.AcceptHostKey {
				accepted.Store(true)
				return nil
			}
			hostKeyErr = ErrHostKey
			return ErrHostKey
		},
		BannerCallback: func(string) error { return nil }, // guest text: never shown or logged
		Timeout:        s.cfg.ConnectTimeout,
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		if hostKeyErr != nil {
			return 0, false, hostKeyErr
		}
		return 0, false, fmt.Errorf("repair: ssh handshake with %s: %w", addr, err)
	}
	conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()
	if accepted.Load() {
		// The person accepted this key in the approved Plan (D-384).
		if err := s.cfg.Store.AddHostKeyPin(ctx, rs.Target, guest, rs.AcceptHostKey, "repair-plan", s.cfg.Now().UTC()); err != nil {
			return 0, false, err
		}
	}
	sess, err := client.NewSession()
	if err != nil {
		return 0, false, fmt.Errorf("repair: ssh session: %w", err)
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return 0, false, err
	}
	out := rs.Output
	if out == nil {
		out = io.Discard
	}
	sess.Stdout, sess.Stderr = out, out
	// The forced command of the line runs whatever is asked here.
	if err := sess.Start("plumb-repair"); err != nil {
		return 0, false, fmt.Errorf("repair: start: %w", err)
	}
	werr := rs.Bundle(stdin)
	stdin.Close()
	err = sess.Wait()
	var ee *ssh.ExitError
	switch {
	case errors.As(err, &ee):
		return ee.ExitStatus(), true, nil
	case err != nil:
		return 0, true, fmt.Errorf("repair: session: %w", err)
	case werr != nil:
		return 0, true, fmt.Errorf("repair: bundle: %s", strings.TrimPrefix(werr.Error(), "agentinstall: "))
	}
	return 0, true, nil
}
