// Package repair is the SSH transport of the agent repair (D-377, D-382
// … D-385, ARCHITECTURE §19): the repair key of this controller, the
// readiness and host key pins from the agents' repair reports, and the
// SSH session that carries the bundle of plan.runRepair.
package repair

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// kindTarget is state.KindTarget (repair does not import state; a test
// keeps both equal).
const kindTarget = "Target"

// Config configures the Service.
type Config struct {
	Store   *store.Store
	Secrets *secret.Store
	Events  *event.Log
	Logger  *slog.Logger
	Now     func() time.Time
	// From are the controller addresses that guests see, IP literals only
	// (review R4 of #290). Empty: repair is off ("no-from").
	From []string
	// Neigh returns the MAC of the controller's neighbour entry of an
	// address (review S1 of #298); default: /proc/net/arp after a probe.
	Neigh func(ctx context.Context, ip net.IP) (string, error)
	// Rewatch sends the watch list with the current key set to every
	// online agent (review S2 of #298); nil: none (tests).
	Rewatch func(ctx context.Context)
	// Dial opens the TCP connection (tests); default net.Dialer.
	Dial func(ctx context.Context, addr string) (net.Conn, error)
	// ConnectTimeout bounds the TCP connect and the SSH handshake;
	// default 10 s (D-385).
	ConnectTimeout time.Duration
}

// Service is the SSH repair transport. It implements plan.RepairTransport.
type Service struct {
	cfg Config
	log *slog.Logger
}

// New checks the config. Every From entry must be an IP literal.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Secrets == nil {
		return nil, errors.New("repair: needs the store and the secret store")
	}
	for _, f := range cfg.From {
		if net.ParseIP(f) == nil {
			return nil, fmt.Errorf("repair: controller address %q is not an IP literal", f)
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Neigh == nil {
		cfg.Neigh = neighMAC
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.Dial == nil {
		d := &net.Dialer{}
		cfg.Dial = func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) }
	}
	return &Service{cfg: cfg, log: cfg.Logger}, nil
}

// FromAddrs resolves the agent URL host to IP literals for From: an IP
// stays; a name is resolved by lookup. Every result is checked with
// net.ParseIP, so a name never reaches a line.
func FromAddrs(ctx context.Context, host string, lookup func(ctx context.Context, host string) ([]net.IP, error)) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []string{ip.String()}, nil
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ip := range ips {
		if s := ip.String(); net.ParseIP(s) != nil && !ip.IsUnspecified() {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("repair: %s has no address", host)
	}
	sort.Strings(out)
	if len(out) > wire.MaxRepairFrom {
		out = out[:wire.MaxRepairFrom]
	}
	return out, nil
}

// keyName binds a sealed key to its id.
func keyName(id string) string { return "repair-key/" + id }

// newKey makes an ed25519 key and seals its private part.
func (s *Service) newKey() (store.RepairKeyRow, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return store.RepairKeyRow{}, err
	}
	seed := priv.Seed()
	defer clear(seed)
	defer clear(priv)
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return store.RepairKeyRow{}, err
	}
	var idb [6]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return store.RepairKeyRow{}, err
	}
	k := store.RepairKeyRow{ID: hex.EncodeToString(idb[:]),
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))),
		Fingerprint: ssh.FingerprintSHA256(sp), CreatedAt: s.cfg.Now().UTC()}
	if k.Nonce, k.Sealed, err = s.cfg.Secrets.SealBlob(secret.PurposeRepairKey, keyName(k.ID), seed); err != nil {
		return store.RepairKeyRow{}, err
	}
	return k, nil
}

// EnsureKey makes the first repair key when none exists (D-382).
func (s *Service) EnsureKey(ctx context.Context) error {
	k, err := s.newKey()
	if err != nil {
		return err
	}
	added, err := s.cfg.Store.AddRepairKey(ctx, k, true)
	if err == nil && added {
		s.log.Info("repair key made", "fingerprint", k.Fingerprint)
	}
	return err
}

// signer opens the private key of a key row. Only a Run calls it.
func (s *Service) signer(k store.RepairKeyRow) (ssh.Signer, error) {
	seed, err := s.cfg.Secrets.OpenBlob(secret.PurposeRepairKey, keyName(k.ID), k.Nonce, k.Sealed)
	if err != nil {
		return nil, fmt.Errorf("repair: the repair key cannot be opened: %w", err)
	}
	defer clear(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("repair: the repair key is damaged")
	}
	return ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(seed))
}

// Watch is the repair key set of every watch command (D-383): the
// current key and the previous one, and From. nil without a key or From.
func (s *Service) Watch(ctx context.Context) *wire.RepairWatch {
	if len(s.cfg.From) == 0 {
		return nil
	}
	keys, err := s.cfg.Store.RepairKeys(ctx)
	if err != nil || len(keys) == 0 {
		if err != nil {
			s.log.Error("read the repair keys", "err", err)
		}
		return nil
	}
	w := &wire.RepairWatch{From: append([]string{}, s.cfg.From...)}
	for _, k := range keys {
		w.Keys = append(w.Keys, wire.RepairKey{ID: k.ID, PublicKey: k.PublicKey})
	}
	if wire.CheckRepairWatch(*w) != nil {
		return nil
	}
	return w
}

// KeyView is RepairKeyView of the contract.
type KeyView struct {
	Fingerprint string       `json:"fingerprint"`
	CreatedAt   time.Time    `json:"created_at"`
	Previous    *PreviousKey `json:"previous"`
	// LeftBehind: the parked and forgotten guests (D-396), also without
	// a previous key (T-217, ROADMAP row 32). Previous.LeftBehind is the
	// same list, kept for one release.
	LeftBehind []string `json:"left_behind"`
}

// PreviousKey is RepairKeyView.previous.
type PreviousKey struct {
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
	Targets     []string  `json:"targets"`
	// LeftBehind are the guests of deleted adopted Targets whose agent was
	// not unbound (D-396): their repair line may still hold this key.
	LeftBehind []string `json:"left_behind"`
}

// View returns the public view of the repair keys; nil without a key.
func (s *Service) View(ctx context.Context) (*KeyView, error) {
	keys, err := s.cfg.Store.RepairKeys(ctx)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	left, err := s.leftBehind(ctx)
	if err != nil {
		return nil, err
	}
	v := &KeyView{Fingerprint: keys[0].Fingerprint, CreatedAt: keys[0].CreatedAt, LeftBehind: left}
	if len(keys) > 1 {
		targets, err := s.notConfirmed(ctx, keys[0].ID)
		if err != nil {
			return nil, err
		}
		v.Previous = &PreviousKey{Fingerprint: keys[1].Fingerprint, CreatedAt: keys[1].CreatedAt, Targets: targets, LeftBehind: left}
	}
	return v, nil
}

// notConfirmed lists the Targets whose last report does not hold the
// line of the key id.
func (s *Service) notConfirmed(ctx context.Context, id string) ([]string, error) {
	reports, err := s.cfg.Store.RepairReportTargets(ctx)
	if err != nil {
		return nil, err
	}
	// Only Targets that hold a guest now (review F7 of T-192): the report
	// of a deleted Target stays in the store, but it is no Target.
	binds, err := s.cfg.Store.Bindings(ctx, "Target")
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, b := range binds {
		bound[b.Name] = true
	}
	out := []string{}
	for t, raw := range reports {
		if !bound[t] {
			continue
		}
		r, ok := parseReport(raw)
		if !ok || !contains(r.Lines, id) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
}

// leftBehind lists the guests ("<target> (VMID <ref>)") whose agent is
// parked after an unconfirmed unbind (D-396), and the forgotten ones
// ("…, agent forgotten") that may still hold a line (review U2 of #309).
func (s *Service) leftBehind(ctx context.Context) ([]string, error) {
	rows, err := s.cfg.Store.LeftBehind(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, p := range rows {
		if p.Forgotten {
			out = append(out, fmt.Sprintf("%s (VMID %s, agent forgotten)", p.Target, p.GuestRef))
		} else {
			out = append(out, fmt.Sprintf("%s (VMID %s)", p.Target, p.GuestRef))
		}
	}
	return out, nil
}

// Rotate makes a new current key (the old one stays until revoked,
// review R2 of #290), or with revokeOld deletes the previous key.
func (s *Service) Rotate(ctx context.Context, revokeOld bool, actor string) (*KeyView, error) {
	if revokeOld {
		before, err := s.View(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := s.cfg.Store.DeletePreviousRepairKey(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, store.ErrNothingToRevoke
		}
		// A forgotten guest whose lines hold only revoked keys lets nobody
		// in any more (review U2 of #309).
		keys, err := s.cfg.Store.RepairKeys(ctx)
		if err != nil {
			return nil, err
		}
		var valid []string
		for _, k := range keys {
			valid = append(valid, k.ID)
		}
		if err := s.cfg.Store.DropForgotten(ctx, valid); err != nil {
			return nil, err
		}
		s.emit(ctx, "repair_key.rotated", actor, map[string]any{"revoke_old": true,
			"fingerprint": before.Fingerprint, "revoked": before.Previous.Fingerprint})
		s.rewatch(ctx)
		return s.View(ctx)
	}
	k, err := s.newKey()
	if err != nil {
		return nil, err
	}
	if _, err := s.cfg.Store.AddRepairKey(ctx, k, false); err != nil {
		return nil, err
	}
	s.emit(ctx, "repair_key.rotated", actor, map[string]any{"revoke_old": false, "fingerprint": k.Fingerprint})
	s.rewatch(ctx)
	return s.View(ctx)
}

// ViewJSON is View for the API (api does not import repair): null
// without a key.
func (s *Service) ViewJSON(ctx context.Context) (json.RawMessage, error) {
	v, err := s.View(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// RotateJSON is Rotate for the API.
func (s *Service) RotateJSON(ctx context.Context, revokeOld bool, actor string) (json.RawMessage, error) {
	v, err := s.Rotate(ctx, revokeOld, actor)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// rewatch pushes the new key set to the online agents now (review S2 of
// #298). A dead agent gets it with the watch at the end of its repair.
func (s *Service) rewatch(ctx context.Context) {
	if s.cfg.Rewatch != nil {
		s.cfg.Rewatch(ctx)
	}
}

func (s *Service) emit(ctx context.Context, typ, actor string, data any) {
	if s.cfg.Events == nil {
		return
	}
	if _, err := s.cfg.Events.Emit(ctx, typ, "repair_key", actor, data); err != nil {
		s.log.Error("event", "type", typ, "err", err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
