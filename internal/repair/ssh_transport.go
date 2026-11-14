package repair

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
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

// dialAddrs returns the addresses that a Run may dial (review S1 of
// #298): the static address of the Target document, or, for a DHCP
// Target, a reported IPv4 address whose neighbour entry on the controller
// has the MAC of the guest's NIC in Proxmox. A guest can so make the
// controller dial only itself; a LAN host, a routed address or an address
// without that MAC is never dialled.
func (s *Service) dialAddrs(ctx context.Context, static string, reported []string, guestMAC string) []string {
	if ip := net.ParseIP(static); ip != nil {
		return []string{ip.String()}
	}
	if guestMAC == "" {
		return nil
	}
	var out []string
	tried := 0
	for _, r := range reported {
		ip, _, err := net.ParseCIDR(r)
		if err != nil {
			ip = net.ParseIP(r)
		}
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			continue
		}
		if tried++; tried > maxDialAddrs {
			break
		}
		mac, err := s.cfg.Neigh(ctx, ip)
		if err != nil || !strings.EqualFold(mac, guestMAC) {
			continue
		}
		out = append(out, ip.String())
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
	addrs := s.dialAddrs(ctx, rs.StaticIP, rs.Reported, rs.GuestMAC)
	if len(addrs) == 0 {
		return 0, false, errors.New("repair: no safe address: the Target has no static address, and no reported address belongs to the MAC of the guest's NIC in Proxmox")
	}
	shared, err := s.cfg.Store.HostKeyOwners(ctx, append(append([]string{}, pins...), rs.AcceptHostKey), rs.Target)
	if err != nil {
		return 0, false, err
	}
	var lastErr error
	for _, a := range addrs {
		exit, sent, err := s.session(ctx, net.JoinHostPort(a, strconv.Itoa(*st.Port)), signer, pins, shared, rs, guest)
		if err == nil || sent || errors.Is(err, ErrHostKey) {
			return exit, sent, err
		}
		lastErr = err
	}
	return 0, false, lastErr
}

// session runs one SSH session on addr.
func (s *Service) session(ctx context.Context, addr string, signer ssh.Signer, pins []string, shared map[string]string, rs plan.RepairSend, guest string) (int, bool, error) {
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
			if other, ok := shared[fp]; ok {
				// The same host key is pinned for another Target: this may
				// be that guest (review S3 of #298).
				hostKeyErr = fmt.Errorf("%w: the host key %s is pinned for Target %s too", ErrHostKey, fp, other)
				return hostKeyErr
			}
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
	// stdout and stderr copy in two goroutines: one locked Write, never
	// a ReaderFrom of the caller's writer (a bytes.Buffer loses data).
	lw := &lockedWriter{w: out}
	sess.Stdout, sess.Stderr = lw, lw
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

// CheckAcceptHostKey implements plan.HostKeyChecker (review S3 of #298).
func (s *Service) CheckAcceptHostKey(ctx context.Context, target, fp string) error {
	owners, err := s.cfg.Store.HostKeyOwners(ctx, []string{fp}, target)
	if err != nil {
		return err
	}
	if other, ok := owners[fp]; ok {
		return fmt.Errorf("the host key %s is pinned for Target %s; a repair does not accept it (host-key-shared)", fp, other)
	}
	return nil
}

// lockedWriter serializes writes and hides any ReadFrom of w.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
