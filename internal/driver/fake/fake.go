// Package fake is an in-memory Driver for tests and dev mode (D-029,
// D-039). It behaves like a hypervisor: operations run as async tasks
// with a latency, the config digest changes with every config change, and
// Diff is the shared rule of all drivers. Tests can inject failures and
// outside changes.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Op names a driver method, for failure injection.
type Op string

const (
	OpFetchImage Op = "FetchImage"
	OpCreate     Op = "Create"
	OpObserve    Op = "Observe"
	OpUpdate     Op = "Update"
	OpPower      Op = "Power"
	OpDelete     Op = "Delete"
)

type guest struct {
	spec  driver.GuestSpec
	power driver.Power
	// token is the active enrollment token; pending is the one that
	// becomes active at the next cold start (re-enrollment).
	token, pending string
	gen            int // bumped on every config change
	ip             string
}

// Driver is the fake driver. The zero value is not usable; call New.
type Driver struct {
	mu       sync.Mutex
	latency  time.Duration
	guests   map[driver.GuestID]*guest
	images   map[string]driver.ImageRef // name+sha → ref
	nextID   int
	failures map[Op][]error
	calls    map[Op]int
}

var _ driver.Driver = (*Driver)(nil)

// New returns an empty fake hypervisor. Guest ids start at 100, like
// Proxmox VMIDs.
func New() *Driver {
	return &Driver{
		guests:   map[driver.GuestID]*guest{},
		images:   map[string]driver.ImageRef{},
		nextID:   100,
		failures: map[Op][]error{},
		calls:    map[Op]int{},
	}
}

// SetLatency sets the duration of every task. Default 0.
func (d *Driver) SetLatency(l time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.latency = l
}

// Fail makes the next call of op return err. Several calls queue up.
func (d *Driver) Fail(op Op, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failures[op] = append(d.failures[op], err)
}

// Calls returns how often op was called.
func (d *Driver) Calls(op Op) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[op]
}

// Tamper changes a guest from outside Plumb, like a user in the Proxmox
// UI. The digest changes.
func (d *Driver) Tamper(id driver.GuestID, change func(*driver.GuestSpec)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	change(&g.spec)
	g.gen++
	return nil
}

// Bootstrap returns the active enrollment token of a guest, as an agent
// in the guest would read it. ok is false for a missing or stopped guest.
func (d *Driver) Bootstrap(id driver.GuestID) (driver.Bootstrap, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok || g.power != driver.PowerRunning || g.token == "" {
		return driver.Bootstrap{}, false
	}
	return driver.Bootstrap{Token: g.token}, true
}

// Guests returns the ids of all guests, sorted.
func (d *Driver) Guests() []driver.GuestID {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]driver.GuestID, 0, len(d.guests))
	for id := range d.guests {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// task runs fn as an async hypervisor task: it starts after the latency
// and the caller waits for it or for ctx. fn runs with the lock held. Its
// result comes back only through the channel, so a task that the caller
// gave up on cannot write into the caller's variables.
func task[T any](ctx context.Context, d *Driver, op Op, fn func() (T, error)) (T, error) {
	var zero T
	d.mu.Lock()
	d.calls[op]++
	if q := d.failures[op]; len(q) > 0 {
		err := q[0]
		d.failures[op] = q[1:]
		d.mu.Unlock()
		return zero, err
	}
	latency := d.latency
	d.mu.Unlock()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1) // buffered: an abandoned task does not leak
	go func() {
		if latency > 0 {
			time.Sleep(latency)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		v, err := fn()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		// Like Proxmox: the task may still finish in the background.
		return zero, ctx.Err()
	}
}

// do is task for methods without a result.
func (d *Driver) do(ctx context.Context, op Op, fn func() error) error {
	_, err := task(ctx, d, op, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FetchImage records the image. It refuses a bad checksum or a non-HTTPS
// URL. The same name and checksum give the same ref.
func (d *Driver) FetchImage(ctx context.Context, spec driver.ImageSpec) (driver.ImageRef, error) {
	return task(ctx, d, OpFetchImage, func() (driver.ImageRef, error) {
		if !sha256Hex.MatchString(spec.SHA256) {
			return "", errors.New("fake: image checksum must be 64 hex digits")
		}
		if !strings.HasPrefix(spec.URL, "https://") {
			return "", errors.New("fake: image URL must use https")
		}
		key := spec.Name + "@" + spec.SHA256
		if r, ok := d.images[key]; ok {
			return r, nil
		}
		ref := driver.ImageRef(fmt.Sprintf("tpl-%d", 9000+len(d.images)))
		d.images[key] = ref
		return ref, nil
	})
}

func (d *Driver) hasImage(ref driver.ImageRef) bool {
	for _, r := range d.images {
		if r == ref {
			return true
		}
	}
	return false
}

// Create makes and starts a guest with the token.
func (d *Driver) Create(ctx context.Context, spec driver.GuestSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	return task(ctx, d, OpCreate, func() (driver.GuestID, error) {
		if !d.hasImage(spec.Image) {
			return "", fmt.Errorf("fake: image %s: %w", spec.Image, driver.ErrNotFound)
		}
		if spec.Name == "" || spec.Cores < 1 || spec.MemoryMB < 1 || spec.DiskGB < 1 || spec.Bridge == "" {
			return "", errors.New("fake: incomplete guest spec")
		}
		for _, g := range d.guests {
			if g.spec.Name == spec.Name {
				return "", fmt.Errorf("fake: guest name %q is taken", spec.Name)
			}
		}
		id := driver.GuestID(strconv.Itoa(d.nextID))
		ip := strings.SplitN(spec.IP, "/", 2)[0]
		if spec.IP == "" || spec.IP == "dhcp" {
			ip = fmt.Sprintf("10.99.%d.%d", d.nextID/250, d.nextID%250+2)
		}
		d.nextID++
		spec.SSHKeys = append([]string(nil), spec.SSHKeys...)
		d.guests[id] = &guest{spec: spec, power: driver.PowerRunning, token: boot.Token, ip: ip}
		return id, nil
	})
}

// Observe returns the settings, the digest, the power state and the IPs.
func (d *Driver) Observe(ctx context.Context, id driver.GuestID) (driver.GuestState, error) {
	return task(ctx, d, OpObserve, func() (driver.GuestState, error) {
		g, ok := d.guests[id]
		if !ok {
			return driver.GuestState{}, driver.ErrNotFound
		}
		spec := g.spec
		spec.SSHKeys = append([]string(nil), g.spec.SSHKeys...)
		st := driver.GuestState{Spec: spec, Digest: digest(g), Power: g.power}
		if g.power == driver.PowerRunning {
			st.IPs = []string{g.ip}
		}
		return st, nil
	})
}

// digest changes whenever the config changes, like the Proxmox digest.
func digest(g *guest) string {
	b, _ := json.Marshal(struct {
		Spec    driver.GuestSpec
		Gen     int
		Pending bool
	}{g.spec, g.gen, g.pending != ""})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:20])
}

// Diff is the shared rule of all drivers.
func (d *Driver) Diff(desired driver.GuestSpec, observed driver.GuestState) ([]wire.Change, error) {
	return driver.DiffGuest(desired, observed)
}

// Update applies changes. A create-only attribute is refused. The new
// token in boot becomes active at the next cold start.
func (d *Driver) Update(ctx context.Context, id driver.GuestID, changes []wire.Change, boot *driver.Bootstrap) error {
	return d.do(ctx, OpUpdate, func() error {
		g, ok := d.guests[id]
		if !ok {
			return driver.ErrNotFound
		}
		next := g.spec
		for _, c := range changes {
			if driver.IsCreateOnly(c.Attr) {
				return &driver.CreateOnlyError{Attr: c.Attr}
			}
			var err error
			switch c.Attr {
			case driver.AttrCores:
				next.Cores, err = driver.IntValue(c.To)
			case driver.AttrMemory:
				next.MemoryMB, err = driver.IntValue(c.To)
			case driver.AttrBridge:
				s, ok := c.To.(string)
				if !ok || s == "" {
					err = fmt.Errorf("fake: bridge %v is not a name", c.To)
				}
				next.Bridge = s
			case driver.AttrEnrollToken:
				if !c.Sensitive || c.From != nil || c.To != nil {
					err = errors.New("fake: enroll_token change must be sensitive and carry no value")
				}
				if boot == nil {
					err = errors.New("fake: enroll_token change without Bootstrap")
				}
			default:
				err = fmt.Errorf("fake: unknown attribute %q", c.Attr)
			}
			if err != nil {
				return err
			}
		}
		g.spec = next
		if boot != nil {
			g.pending = boot.Token
		}
		g.gen++
		return nil
	})
}

// Power runs a power action. A pending token becomes active at a start
// from stopped or at a restart.
func (d *Driver) Power(ctx context.Context, id driver.GuestID, action driver.PowerAction) error {
	return d.do(ctx, OpPower, func() error {
		g, ok := d.guests[id]
		if !ok {
			return driver.ErrNotFound
		}
		switch action {
		case driver.PowerStop:
			g.power = driver.PowerStopped
		case driver.PowerStart, driver.PowerRestart:
			if action == driver.PowerRestart || g.power == driver.PowerStopped {
				if g.pending != "" {
					g.token, g.pending = g.pending, ""
					g.gen++
				}
			}
			g.power = driver.PowerRunning
		default:
			return fmt.Errorf("fake: unknown power action %q", action)
		}
		return nil
	})
}

// Delete removes a guest.
func (d *Driver) Delete(ctx context.Context, id driver.GuestID) error {
	return d.do(ctx, OpDelete, func() error {
		if _, ok := d.guests[id]; !ok {
			return driver.ErrNotFound
		}
		delete(d.guests, id)
		return nil
	})
}
