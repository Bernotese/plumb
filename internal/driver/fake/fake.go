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
	// spec is what the running guest uses; next is what it uses after the
	// next stop and start (D-190). Both are equal on a stopped guest.
	spec, next driver.GuestSpec
	power      driver.Power
	// token is the active enrollment token; pending is the one that
	// becomes active at the next cold start (re-enrollment).
	token, pending string
	gen            int // bumped on every config change
	ip             string
	frozen         string // a digest that a cold start kept (ColdRestart)
	boots          int    // cold starts, Create included
}

// Driver is the fake driver. The zero value is not usable; call New.
type Driver struct {
	mu       sync.Mutex
	latency  time.Duration
	guests   map[driver.GuestID]*guest
	images   map[string]driver.ImageRef // name+sha → ref
	nextID   int
	nextTpl  int // refs are never used twice, also after DestroyTemplate
	failures map[Op][]error
	calls    map[Op]int
	hooks    map[Op][]func()
	// hotplug: a running guest takes cores and memory live. Default off,
	// like Proxmox without hotplug and NUMA settings.
	hotplug bool
	// bridgeStuck: a running guest does not take a bridge change live (a
	// failed hot-unplug).
	bridgeStuck bool
	// keepPending: a write of the running value on a running guest keeps
	// the pending value (a Proxmox that needs revert=<key>, review B1).
	keepPending bool
	// restartFault fails a restart half way: "stop" (the guest still
	// runs) or "start" (the guest is stopped). One shot.
	restartFault    string
	restartFaultErr error
}

var _ driver.Driver = (*Driver)(nil)

// New returns an empty fake hypervisor. Guest ids start at 100, like
// Proxmox VMIDs.
func New() *Driver {
	return &Driver{
		guests:   map[driver.GuestID]*guest{},
		images:   map[string]driver.ImageRef{},
		nextID:   100,
		nextTpl:  9000,
		failures: map[Op][]error{},
		calls:    map[Op]int{},
		hooks:    map[Op][]func(){},
	}
}

// Hook runs fn once, right before the next call of op takes the driver
// lock: for example a guest that someone starts outside Plumb between
// two steps of a Run.
func (d *Driver) Hook(op Op, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hooks[op] = append(d.hooks[op], fn)
}

// SetHotplug makes a running guest take cores and memory live.
func (d *Driver) SetHotplug(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hotplug = on
}

// SetBridgeStuck makes a running guest keep a bridge change pending.
func (d *Driver) SetBridgeStuck(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bridgeStuck = on
}

// SetKeepPending makes a write of the running value keep the pending one.
func (d *Driver) SetKeepPending(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keepPending = on
}

// FailRestart makes the next restart fail half way: at "stop" the guest
// still runs; at "start" it is stopped.
func (d *Driver) FailRestart(stage string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restartFault, d.restartFaultErr = stage, err
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
// UI, in the running view and in the view after the next cold start. The
// digest changes.
func (d *Driver) Tamper(id driver.GuestID, change func(*driver.GuestSpec)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	change(&g.spec)
	change(&g.next)
	g.gen++
	g.frozen = ""
	return nil
}

// TamperPending changes only the view after the next cold start, like
// qm set --memory on a running VM (T-015 run 1). The digest changes.
func (d *Driver) TamperPending(id driver.GuestID, change func(*driver.GuestSpec)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	change(&g.next)
	g.gen++
	g.frozen = ""
	return nil
}

// StartOutside starts a stopped guest outside Plumb; the pending values
// become active. A running guest stays as it is.
func (d *Driver) StartOutside(id driver.GuestID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	if g.power == driver.PowerStopped {
		g.coldStart()
	}
	return nil
}

// StopOutside stops a guest outside Plumb. Pending values stay pending.
func (d *Driver) StopOutside(id driver.GuestID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	g.power = driver.PowerStopped
	return nil
}

// ColdRestart stops and starts a guest outside Plumb. With keepDigest the
// digest does not move, so a poll cannot rely on it (review S8).
func (d *Driver) ColdRestart(id driver.GuestID, keepDigest bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	g, ok := d.guests[id]
	if !ok {
		return driver.ErrNotFound
	}
	old := digest(g)
	g.coldStart()
	if keepDigest {
		g.frozen = old
	}
	return nil
}

// coldStart applies the pending values and the pending token and runs
// the guest.
func (g *guest) coldStart() {
	changed := !equalSpec(g.spec, g.next) || g.pending != ""
	g.spec = g.next
	g.spec.SSHKeys = append([]string(nil), g.next.SSHKeys...)
	if g.pending != "" {
		g.token, g.pending = g.pending, ""
	}
	if changed {
		g.gen++
		g.frozen = ""
	}
	g.power = driver.PowerRunning
	g.boots++
}

// Boots returns how often a guest had a cold start (Create counts), so a
// test agent can say hello after each boot, like the real agent.
func (d *Driver) Boots(id driver.GuestID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if g, ok := d.guests[id]; ok {
		return g.boots
	}
	return 0
}

func equalSpec(a, b driver.GuestSpec) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
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
	if hs := d.hooks[op]; len(hs) > 0 {
		d.hooks[op] = nil
		d.mu.Unlock()
		for _, h := range hs {
			h()
		}
		d.mu.Lock()
	}
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
		ref := driver.ImageRef(fmt.Sprintf("tpl-%d", d.nextTpl))
		d.nextTpl++
		d.images[key] = ref
		return ref, nil
	})
}

// DestroyTemplate removes an image as if someone destroyed the template on
// the host (T-055). Guests of the image stay. The next FetchImage of the
// same spec makes it again with a new ref. It is a test helper, not the
// guarded driver method RemoveImage.
func (d *Driver) DestroyTemplate(ref driver.ImageRef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, r := range d.images {
		if r == ref {
			delete(d.images, k)
		}
	}
}

// Images returns the number of images.
func (d *Driver) Images() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.images)
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
		if spec.Name == "" || spec.ImageName == "" || spec.Cores < 1 || spec.MemoryMB < 1 || spec.DiskGB < 1 || spec.Bridge == "" {
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
		spec.Image = "" // like Proxmox: the guest keeps the Image name, not the ref (D-189)
		next := spec
		next.SSHKeys = append([]string(nil), spec.SSHKeys...)
		d.guests[id] = &guest{spec: spec, next: next, power: driver.PowerRunning, token: boot.Token, ip: ip, boots: 1}
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
		st := driver.GuestState{Spec: spec, Digest: digest(g), Power: g.power, TokenPending: g.pending != ""}
		if !equalSpec(g.spec, g.next) {
			next := g.next
			next.SSHKeys = append([]string(nil), g.next.SSHKeys...)
			st.Pending = &next
		}
		if g.power == driver.PowerRunning {
			st.IPs = []string{g.ip}
		}
		return st, nil
	})
}

// digest changes whenever the config changes, like the Proxmox digest.
func digest(g *guest) string {
	if g.frozen != "" {
		return g.frozen
	}
	b, _ := json.Marshal(struct {
		Spec, Next driver.GuestSpec
		Gen        int
		Pending    bool
	}{g.spec, g.next, g.gen, g.pending != ""})
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
		// A running guest takes cores and memory only at the next cold
		// start (unless hotplug), and the bridge live (unless stuck).
		running := g.power == driver.PowerRunning
		run, next := g.spec, g.next
		for _, c := range changes {
			if driver.IsCreateOnly(c.Attr) {
				return &driver.CreateOnlyError{Attr: c.Attr}
			}
			var err error
			switch c.Attr {
			case driver.AttrCores:
				var v int
				v, err = driver.IntValue(c.To)
				if !(running && d.keepPending && v == run.Cores) {
					next.Cores = v
				}
				if !running || d.hotplug {
					run.Cores = v
				}
			case driver.AttrMemory:
				var v int
				v, err = driver.IntValue(c.To)
				if !(running && d.keepPending && v == run.MemoryMB) {
					next.MemoryMB = v
				}
				if !running || d.hotplug {
					run.MemoryMB = v
				}
			case driver.AttrBridge:
				s, ok := c.To.(string)
				if !ok || s == "" {
					err = fmt.Errorf("fake: bridge %v is not a name", c.To)
				}
				if !(running && d.keepPending && s == run.Bridge) {
					next.Bridge = s
				}
				if !running || !d.bridgeStuck {
					run.Bridge = s
				}
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
		g.spec, g.next = run, next
		if boot != nil {
			g.pending = boot.Token
		}
		g.gen++
		g.frozen = ""
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
		case driver.PowerStart:
			if g.power == driver.PowerStopped { // a start of a running guest does nothing
				g.coldStart()
			}
		case driver.PowerRestart:
			if d.restartFault != "" {
				stage, err := d.restartFault, d.restartFaultErr
				d.restartFault, d.restartFaultErr = "", nil
				if stage == "start" {
					g.power = driver.PowerStopped
				}
				return err
			}
			g.coldStart()
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

// --- Image builds: stubs until T-061 (contract T-058) ---

// CreateBuild is not implemented yet (T-061).
func (d *Driver) CreateBuild(ctx context.Context, spec driver.BuildSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	return "", driver.ErrNotImplemented
}

// MakeImage is not implemented yet (T-061).
func (d *Driver) MakeImage(ctx context.Context, id driver.GuestID, img driver.BuiltImage) (driver.ImageRef, error) {
	return "", driver.ErrNotImplemented
}

// FindImage is not implemented yet (T-061).
func (d *Driver) FindImage(ctx context.Context, image string, version int, digest string) (driver.ImageRef, error) {
	return "", driver.ErrNotImplemented
}

// ListImages is not implemented yet (T-061).
func (d *Driver) ListImages(ctx context.Context, image string) ([]driver.ImageObject, error) {
	return nil, driver.ErrNotImplemented
}

// RemoveImage is not implemented yet (T-061).
func (d *Driver) RemoveImage(ctx context.Context, obj driver.ImageObject) error {
	return driver.ErrNotImplemented
}

// DiskSpace is not implemented yet (T-061).
func (d *Driver) DiskSpace(ctx context.Context) (driver.DiskSpace, error) {
	return driver.DiskSpace{}, driver.ErrNotImplemented
}
