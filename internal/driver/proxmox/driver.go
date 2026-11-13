package proxmox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Config configures the driver. The names come from the installer (T-013).
type Config struct {
	Client *Client
	Node   string // the one node (D-001)
	Pool   string // the pool of every Plumb guest and template (D-032)
	// DiskStorage holds the VM disks and the cloud-init drives.
	DiskStorage string
	// ImageStorage holds downloaded images (content "import") and LXC
	// templates (content "vztmpl", D-229).
	ImageStorage string
	// SnippetStorage and Snippet name the static vendor snippet that the
	// installer wrote (D-025). The driver never writes it.
	SnippetStorage string
	Snippet        string
	// VMIDMin and VMIDMax bound the ids of new guests and templates.
	VMIDMin, VMIDMax int
	// ShutdownTimeout is the clean shutdown time before a hard stop. Default 120 s.
	ShutdownTimeout time.Duration
	// Nameservers (1 to 3 IP addresses) and SearchDomain (one DNS name) go
	// to every new VM (D-187, PLUMB_PVE_NAMESERVER and _SEARCHDOMAIN).
	// Without them Proxmox copies the DNS settings of the host into the
	// cloud-init network data, and a DNS change on the host gives every
	// guest a new cloud-init instance. Create-only (D-185).
	Nameservers  []string
	SearchDomain string
	// Bridge is the only bridge of the install (PLUMB_PVE_BRIDGE): the
	// token has SDN.Use on it alone (D-081). Create and Update refuse any
	// other bridge before a Proxmox call (D-184).
	Bridge string
	// StartWait and StartPoll bound the retries of the start-up check on
	// /version (D-183). Default 60 s and 5 s.
	StartWait, StartPoll time.Duration
	// ImageLockWait and ImageLockPoll bound the wait of FetchImage for a
	// lock on a VM with the template name (D-188): another Target can
	// make or convert the same template. Default 2 min and 1 s.
	ImageLockWait, ImageLockPoll time.Duration
}

// Driver is the Proxmox VE driver.
type Driver struct {
	cfg Config
	c   *Client

	// imageLocks serialize FetchImage per template name (review S2): two
	// Targets with the same Image run in parallel, and one must not take
	// the other's VM for a half-made rest. One controller per host (D-001).
	imageMu    sync.Mutex
	imageLocks map[string]*sync.Mutex

	// reserved holds the VMIDs that nextID gave to a running Create or
	// FetchImage (T-055): /cluster/nextid gives the same free id to two
	// parallel calls until one of them made its VM.
	idMu     sync.Mutex
	reserved map[int]bool

	// templates: the start check saw the content "vztmpl" on the image
	// storage (D-229). Check sets it once, before serve uses the driver.
	templates bool

	// ct: container templates (ctfetch.go).
	ct ctState
}

// imageLock returns the lock of one template name.
func (d *Driver) imageLock(name string) *sync.Mutex {
	d.imageMu.Lock()
	defer d.imageMu.Unlock()
	if d.imageLocks == nil {
		d.imageLocks = map[string]*sync.Mutex{}
	}
	l := d.imageLocks[name]
	if l == nil {
		l = &sync.Mutex{}
		d.imageLocks[name] = l
	}
	return l
}

var _ driver.Driver = (*Driver)(nil)

// New returns the driver.
// checkDNS checks the DNS settings of new VMs (D-187).
func checkDNS(servers []string, domain string) error {
	if len(servers) < 1 || len(servers) > 3 {
		return fmt.Errorf("proxmox: 1 to 3 name servers are required (PLUMB_PVE_NAMESERVER), not %d", len(servers))
	}
	for _, s := range servers {
		if ip := net.ParseIP(s); ip == nil || strings.ContainsAny(s, ", =") {
			return fmt.Errorf("proxmox: name server %q is no IP address", s)
		}
	}
	if !dnsNameRE.MatchString(domain) || len(domain) > 253 {
		return fmt.Errorf("proxmox: search domain %q is no DNS name (PLUMB_PVE_SEARCHDOMAIN)", domain)
	}
	return nil
}

var dnsNameRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

func New(cfg Config) (*Driver, error) {
	switch {
	case cfg.Client == nil:
		return nil, errors.New("proxmox: a client is required")
	case cfg.Node == "" || cfg.Pool == "" || cfg.DiskStorage == "" || cfg.ImageStorage == "" ||
		cfg.SnippetStorage == "" || cfg.Snippet == "":
		return nil, errors.New("proxmox: node, pool, storages and snippet are required")
	case cfg.VMIDMin < 100 || cfg.VMIDMax < cfg.VMIDMin:
		return nil, errors.New("proxmox: a VMID range of at least 100 is required")
	}
	if err := checkDNS(cfg.Nameservers, cfg.SearchDomain); err != nil {
		return nil, err
	}
	if !reBridge.MatchString(cfg.Bridge) {
		return nil, fmt.Errorf("proxmox: the bridge of the install is required (PLUMB_PVE_BRIDGE), not %q", cfg.Bridge)
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	if cfg.ImageLockWait == 0 {
		cfg.ImageLockWait = 2 * time.Minute
	}
	if cfg.ImageLockPoll == 0 {
		cfg.ImageLockPoll = time.Second
	}
	return &Driver{cfg: cfg, c: cfg.Client}, nil
}

// ErrForeignGuest is returned for a VMID that is not in the pool or not
// in the VMID range. The driver touches only its own guests.
var ErrForeignGuest = errors.New("proxmox: the guest is not managed by Plumb")

func (d *Driver) node() string { return "/nodes/" + url.PathEscape(d.cfg.Node) }

func vmPath(d *Driver, id int) string { return d.node() + "/qemu/" + strconv.Itoa(id) }

// vmid parses a guest id or an image ref.
func vmid(s string) (int, error) {
	id, err := strconv.Atoi(s)
	if err != nil || id < 100 {
		return 0, fmt.Errorf("proxmox: %q is not a VMID", s)
	}
	return id, nil
}

// member is one guest of the pool. Proxmox builds the member fields from
// its cluster resource cache, which pvestatd refreshes every few seconds:
// right after a template conversion the list still says template 0 (test
// host, T-012), and right after a clone the new guest has no name yet
// (test host, T-073 A4). Membership itself is current (pool config). So
// own reads Name and Template from the guest config instead.
type member struct {
	VMID     int    `json:"vmid"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Template int    `json:"template"` // stale for a few seconds; see own
}

// Guest types of the pool members (Proxmox: qemu and lxc).
const (
	typeVM = "qemu"
	typeCT = "lxc"
)

// members lists the qemu guests in the pool.
func (d *Driver) members(ctx context.Context) ([]member, error) {
	all, err := d.guests(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(m member) bool { return m.Type != typeVM }), nil
}

// guests lists the VMs and containers in the pool. Proxmox takes the type
// of a member from its VM list, which is current; a member without a type
// counts as a VM, as before containers (T-079).
func (d *Driver) guests(ctx context.Context) ([]member, error) {
	var pools []struct {
		PoolID  string   `json:"poolid"`
		Members []member `json:"members"`
	}
	if err := d.c.Get(ctx, "/pools", url.Values{"poolid": {d.cfg.Pool}}, &pools); err != nil {
		return nil, err
	}
	var out []member
	for _, p := range pools {
		// The query asks for one pool, but the guard does not trust the
		// server's filter: only members of our pool count (review T-012 #1).
		if p.PoolID != d.cfg.Pool {
			continue
		}
		for _, m := range p.Members {
			switch m.Type {
			case "":
				m.Type = typeVM
				out = append(out, m)
			case typeVM, typeCT:
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// own checks that a VMID is in the range and in the pool, before the
// driver touches it. A missing guest is driver.ErrNotFound.
func (d *Driver) own(ctx context.Context, s string) (int, member, error) {
	id, err := vmid(s)
	if err != nil {
		return 0, member{}, err
	}
	if id < d.cfg.VMIDMin || id > d.cfg.VMIDMax {
		return 0, member{}, fmt.Errorf("%w: VMID %d is outside %d–%d", ErrForeignGuest, id, d.cfg.VMIDMin, d.cfg.VMIDMax)
	}
	ms, err := d.guests(ctx)
	if err != nil {
		return 0, member{}, err
	}
	for _, m := range ms {
		if m.VMID == id {
			// Name and template come from the config, never from the
			// list: for a fresh clone the list has no name yet (T-073).
			if m.Type == typeCT {
				cfg, err := d.ctConfig(ctx, id, true)
				if err != nil {
					return 0, member{}, err
				}
				m.Name, m.Template = cfg["hostname"], 0
				if cfg["template"] == "1" {
					m.Template = 1
				}
				return id, m, nil
			}
			cfg, err := d.config(ctx, id)
			if err != nil {
				return 0, member{}, err
			}
			m.Name = cfg["name"]
			m.Template = 0
			if cfg["template"] == "1" {
				m.Template = 1
			}
			return id, m, nil
		}
	}
	// Not in the pool: either gone, or someone else's. The token can only
	// see pool guests, so both look the same; it is not ours either way.
	return 0, member{}, fmt.Errorf("VMID %d is not in pool %s: %w", id, d.cfg.Pool, driver.ErrNotFound)
}

// ownVM is own for the methods that know only VMs: a container is
// refused before any change (T-038 §3).
func (d *Driver) ownVM(ctx context.Context, s string) (int, member, error) {
	id, m, err := d.own(ctx, s)
	if err == nil && m.Type != typeVM {
		return 0, member{}, fmt.Errorf("proxmox: VMID %d is a container, not a VM", id)
	}
	return id, m, err
}

// nextID returns a free VMID in the range.
// The caller must call releaseID when its VM exists or is gone.
func (d *Driver) nextID(ctx context.Context) (int, error) {
	d.idMu.Lock()
	defer d.idMu.Unlock()
	for id := d.cfg.VMIDMin; id <= d.cfg.VMIDMax; id++ {
		if d.reserved[id] {
			continue
		}
		var got string
		err := d.c.Get(ctx, "/cluster/nextid", url.Values{"vmid": {strconv.Itoa(id)}}, &got)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 400 && strings.Contains(ae.Error(), "already exists") {
			continue // taken
		}
		if err != nil {
			return 0, err
		}
		if d.reserved == nil {
			d.reserved = map[int]bool{}
		}
		d.reserved[id] = true
		return id, nil
	}
	return 0, fmt.Errorf("proxmox: no free VMID in %d–%d", d.cfg.VMIDMin, d.cfg.VMIDMax)
}

// releaseID ends the reservation of nextID.
func (d *Driver) releaseID(id int) {
	d.idMu.Lock()
	defer d.idMu.Unlock()
	delete(d.reserved, id)
}

func notFound(err error) error {
	var ae *APIError
	if errors.As(err, &ae) && ae.NotFound() {
		return fmt.Errorf("%w: %v", driver.ErrNotFound, err)
	}
	return err
}

// --- names and markers ---

var safeName = regexp.MustCompile(`[^a-z0-9.-]+`)

// templateName names the template of an image. The checksum is part of
// it, so a new checksum is a new template (D-077).
func templateName(spec driver.ImageSpec) string {
	n := safeName.ReplaceAllString(strings.ToLower(spec.Name), "-")
	return fmt.Sprintf("plumb-img-%s-%s", n, spec.SHA256[:12])
}

func importFile(spec driver.ImageSpec) string {
	n := safeName.ReplaceAllString(strings.ToLower(spec.Name), "-")
	return fmt.Sprintf("plumb-%s-%s.qcow2", n, spec.SHA256[:12])
}

// imageMarker is a line in the description of a guest that names the
// Image document it was made from (D-189). It is the create-only
// attribute image; a re-made template does not change it.
const imageMarker = "plumb-image: "

// hasChecksum reports whether a template description holds the full
// checksum line that FetchImage writes (D-188).
func hasChecksum(description, sum string) bool {
	for _, line := range strings.Split(description, "\n") {
		if strings.TrimSpace(line) == "sha256="+sum {
			return true
		}
	}
	return false
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// --- FetchImage ---

// FetchImage downloads the image into the image storage with the pinned
// SHA-256 (Proxmox checks it), creates a VM that imports it, adds a
// cloud-init drive and turns it into a template. It is idempotent: an
// existing template of the same name and checksum is reused, and an
// existing import file is not downloaded again. The engine calls it before
// every create (D-188), so a template that someone destroyed or moved out
// of the pool is made again.
func (d *Driver) FetchImage(ctx context.Context, spec driver.ImageSpec) (driver.ImageRef, error) {
	switch spec.Kind {
	case wire.KindLXC:
		return d.fetchCT(ctx, spec, false)
	case wire.KindVM:
	default:
		return "", fmt.Errorf("proxmox: unknown image type %q", spec.Kind)
	}
	if !sha256Hex.MatchString(spec.SHA256) || !strings.HasPrefix(spec.URL, "https://") {
		return "", errors.New("proxmox: an image needs an https URL and a SHA-256")
	}
	name := templateName(spec)
	lock := d.imageLock(name)
	lock.Lock()
	defer lock.Unlock()
	ref, halfMade, err := d.findTemplate(ctx, name, spec.SHA256)
	if err != nil || ref != "" {
		return ref, err
	}
	// A VM with the template's name that is no template is the rest of a
	// failed earlier try: remove it before the next try (review T-012 #6).
	// If that fails, stop: a second VM with the same name must not follow.
	for _, id := range halfMade {
		if err := d.removeHalfMade(ctx, id, name, false); err != nil {
			return "", halfMadeLeft(nil, id, err)
		}
	}

	file := importFile(spec)
	storage := d.node() + "/storage/" + url.PathEscape(d.cfg.ImageStorage)
	var content []struct {
		VolID string `json:"volid"`
	}
	if err := d.c.Get(ctx, storage+"/content", url.Values{"content": {"import"}}, &content); err != nil {
		return "", err
	}
	volid := d.cfg.ImageStorage + ":import/" + file
	have := slices.ContainsFunc(content, func(c struct {
		VolID string `json:"volid"`
	}) bool {
		return c.VolID == volid
	})
	if !have {
		if err := d.c.Task(ctx, d.cfg.Node, "POST", storage+"/download-url", url.Values{
			"content": {"import"}, "filename": {file}, "url": {spec.URL},
			"checksum": {spec.SHA256}, "checksum-algorithm": {"sha256"}, "verify-certificates": {"1"},
		}); err != nil {
			return "", fmt.Errorf("proxmox: download %s: %w", spec.URL, err)
		}
	}

	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	defer d.releaseID(id)
	if err := d.c.Task(ctx, d.cfg.Node, "POST", d.node()+"/qemu", url.Values{
		"vmid": {strconv.Itoa(id)}, "name": {name}, "pool": {d.cfg.Pool},
		"ostype": {"l26"}, "memory": {"1024"}, "cores": {"1"}, "cpu": {"x86-64-v2-AES"},
		"scsihw": {"virtio-scsi-single"}, "scsi0": {d.cfg.DiskStorage + ":0,import-from=" + volid},
		"ide2": {d.cfg.DiskStorage + ":cloudinit"}, "boot": {"order=scsi0"},
		"serial0": {"socket"}, "vga": {"serial0"},
		"description": {"Plumb image " + spec.Name + "\nsha256=" + spec.SHA256},
	}); err != nil {
		return "", halfMadeLeft(fmt.Errorf("proxmox: create template VM: %w", err), id, d.removeHalfMade(ctx, id, name, false))
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/template", nil); err != nil {
		return "", halfMadeLeft(fmt.Errorf("proxmox: convert to template: %w", err), id, d.removeHalfMade(ctx, id, name, true))
	}
	return driver.ImageRef(strconv.Itoa(id)), nil
}

// findTemplate looks for a VM with the template name in the pool and the
// VMID range. It returns the ref of a template with the full checksum, or
// the VMIDs of half-made VMs of that name and checksum. The template flag comes from
// the guest config, not from the list (see member): a fresh template must
// never look half-made. A VM with a lock is busy (a create, clone or
// conversion runs; review S2): it is never half-made, and findTemplate
// waits up to ImageLockWait for the lock to go (D-188). A template with
// the name but another checksum, or a VM with the name and without the
// checksum line, is an error that removes nothing.
func (d *Driver) findTemplate(ctx context.Context, name, sum string) (driver.ImageRef, []int, error) {
	deadline := time.Now().Add(d.cfg.ImageLockWait)
	for {
		ms, err := d.members(ctx)
		if err != nil {
			return "", nil, err
		}
		var halfMade []int
		busy, busyLock := 0, ""
		for _, m := range ms {
			if m.Name != name || m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
				continue
			}
			cfg, err := d.config(ctx, m.VMID)
			if err != nil {
				return "", nil, err
			}
			switch {
			case cfg["lock"] != "":
				busy, busyLock = m.VMID, cfg["lock"]
			case cfg["template"] == "1" && !hasChecksum(cfg["description"], sum):
				return "", nil, fmt.Errorf("proxmox: template VM %d has the name %s but not the checksum sha256=%s in its description; Plumb does not use or remove it: check it and remove it by hand (qm destroy %d)", m.VMID, name, sum, m.VMID)
			case cfg["template"] == "1":
				return driver.ImageRef(strconv.Itoa(m.VMID)), nil, nil
			case !hasChecksum(cfg["description"], sum):
				// A name match alone proves nothing (review S1 of PR #92):
				// FetchImage writes the checksum line at create, so a VM
				// without it is not a half-made image of ours.
				return "", nil, fmt.Errorf("proxmox: VM %d has the name %s but is no template and has not the checksum sha256=%s in its description; Plumb does not use or remove it: check it and remove it by hand (qm destroy %d)", m.VMID, name, sum, m.VMID)
			default:
				halfMade = append(halfMade, m.VMID)
			}
		}
		if busy == 0 {
			return "", halfMade, nil
		}
		if !time.Now().Before(deadline) {
			return "", nil, fmt.Errorf("proxmox: image VM %d is busy (lock %s); try again later", busy, busyLock)
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(d.cfg.ImageLockPoll):
		}
	}
}

// --- Create ---

// sshKeysParam encodes the keys the way the sshkeys option needs them:
// newline-separated and URL-encoded with %20 for spaces (to verify on
// host), then form-encoded by the client.
func sshKeysParam(keys []string) string {
	return strings.ReplaceAll(url.QueryEscape(strings.Join(keys, "\n")), "+", "%20")
}

func ipConfig(s driver.GuestSpec) string {
	if s.IP == "" || s.IP == "dhcp" {
		return "ip=dhcp"
	}
	v := "ip=" + s.IP
	if s.Gateway != "" {
		v += ",gw=" + s.Gateway
	}
	return v
}

// smbios builds the smbios1 option: it keeps every field of the old value
// (the uuid above all) and sets the serial, base64-encoded (D-078).
func smbios(old, serial string) string {
	fields := parseProps(old)
	for k, v := range fields {
		if fields["base64"] != "1" && k != "uuid" && k != "base64" {
			fields[k] = b64(v)
		}
	}
	fields["serial"] = b64(serial)
	fields["base64"] = "1"
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k + "=" + fields[k])
	}
	return b.String()
}

// Create clones the template into the pool, sets the settings, the vendor
// snippet and the token as SMBIOS serial, grows the disk and starts the
// guest. The driver writes no file on the host. A failure after the clone
// removes the half-made guest.
func (d *Driver) Create(ctx context.Context, spec driver.GuestSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	if boot.Token == "" {
		return "", errors.New("proxmox: a new guest needs an enrollment token")
	}
	switch spec.Kind {
	case wire.KindLXC:
		return d.createCT(ctx, spec, boot)
	case wire.KindVM:
	default:
		return "", fmt.Errorf("proxmox: unknown guest type %q", spec.Kind)
	}
	if err := checkSpec(spec); err != nil {
		return "", err
	}
	if err := d.checkBridge(spec.Bridge); err != nil {
		return "", err
	}
	tpl, m, err := d.ownVM(ctx, string(spec.Image))
	if err != nil {
		return "", fmt.Errorf("proxmox: image %s: %w", spec.Image, err)
	}
	if m.Template != 1 {
		return "", fmt.Errorf("proxmox: VMID %d is not a template", tpl)
	}
	if err := d.checkDisk(ctx, tpl, spec); err != nil {
		return "", err
	}
	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	defer d.releaseID(id)
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, tpl)+"/clone", url.Values{
		"newid": {strconv.Itoa(id)}, "name": {spec.Name}, "pool": {d.cfg.Pool},
		"full": {"1"}, "storage": {d.cfg.DiskStorage},
	}); err != nil {
		// A clone that failed or was stopped after Proxmox made the VM
		// leaves a half-made guest (review N2).
		return "", halfMadeLeft(fmt.Errorf("proxmox: clone: %w", err), id, d.removeHalfMade(ctx, id, spec.Name, false))
	}
	gid := driver.GuestID(strconv.Itoa(id))
	if err := d.configure(ctx, id, spec, boot.Token); err != nil {
		// The clone worked: the VM at id is the one this call made.
		return "", scrub(halfMadeLeft(err, id, d.removeHalfMade(ctx, id, spec.Name, true)), boot.Token)
	}
	return gid, nil
}

// checkDisk refuses a disk that is smaller than the template's disk
// before the clone: Proxmox cannot shrink a disk, and the resize would
// fail only after the clone (T-073). A size it cannot read is no error;
// the resize still refuses it, and the half-made guest is removed.
func (d *Driver) checkDisk(ctx context.Context, tpl int, spec driver.GuestSpec) error {
	cfg, err := d.config(ctx, tpl)
	if err != nil {
		return err
	}
	have := sizeMB(parseProps(cfg["scsi0"])["size"])
	if have <= 0 || spec.DiskGB*1024 >= have {
		return nil
	}
	min := (have + 1023) / 1024
	return fmt.Errorf("proxmox: disk %d GB is smaller than the %d GB disk of image %s (template VMID %d); a disk cannot shrink: set disk to %d or more",
		spec.DiskGB, min, spec.ImageName, tpl, min)
}

// removeHalfMade deletes a guest that a failed Create or FetchImage left
// behind, with its own deadline. Like every change, it first checks that
// the VMID is ours: in the range, in the pool, with the name this call gave
// it and no template. A failed create can mean that someone else took the
// id after nextID; that guest is never touched (review N1), and that is no
// error. It waits until Proxmox drops the lock of a stopped create or clone
// (a DELETE during the lock fails), checks the task results and proves that
// the guest is gone (review S1). A non-nil error means the guest is left.
// made says that this call's create or clone worked, so the guest at id
// must be its own: another name there is then an error (the guest is left
// and named), not a silent skip (T-073).
func (d *Driver) removeHalfMade(ctx context.Context, id int, name string, made bool) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	_, m, err := d.own(cleanup, strconv.Itoa(id))
	switch {
	case errors.Is(err, driver.ErrNotFound), errors.Is(err, ErrForeignGuest):
		return nil // nothing of ours to remove
	case err != nil:
		return err
	case m.Name != name || m.Template == 1 || m.Type != typeVM:
		if made {
			return fmt.Errorf("VMID %d has the name %q (template %d), not %q: not proven ours, not removed", id, m.Name, m.Template, name)
		}
		return nil // not the guest this call made
	}
	if err := d.waitUnlocked(cleanup, id); err != nil {
		return err
	}
	if st, err := d.status(cleanup, id); err != nil {
		return err
	} else if st == "running" {
		if err := d.c.Task(cleanup, d.cfg.Node, "POST", vmPath(d, id)+"/status/stop", nil); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	if err := d.c.Task(cleanup, d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if _, _, err := d.own(cleanup, strconv.Itoa(id)); !errors.Is(err, driver.ErrNotFound) {
		if err == nil {
			return errors.New("the guest is still there after the delete")
		}
		return err
	}
	return nil
}

// waitUnlocked waits until the guest config has no lock.
func (d *Driver) waitUnlocked(ctx context.Context, id int) error {
	for {
		cfg, err := d.config(ctx, id)
		if err != nil {
			return err
		}
		if cfg["lock"] == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still locked (%s): %w", cfg["lock"], ctx.Err())
		case <-time.After(d.c.poll):
		}
	}
}

// halfMadeLeft adds a failed cleanup to the error of Create or FetchImage,
// so the Run and the Issue name the VMID that is left (review S1).
func halfMadeLeft(err error, id int, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	return errors.Join(err, fmt.Errorf("proxmox: the half-made VM %d is left: %w; remove it with qm destroy %d", id, cleanupErr, id))
}

// The driver is the last step before the host: values that go into a
// Proxmox property string must not carry "," or "=" (review T-012 #5).
var (
	reBridge = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
	reIPv4   = regexp.MustCompile(`^(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])){3}$`)
	reUser   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// checkBridge refuses a bridge other than the one of the install (D-184).
func (d *Driver) checkBridge(b string) error {
	if b != d.cfg.Bridge {
		return fmt.Errorf("proxmox: bridge %q is not the bridge of this install (%s); the token may use only %s (D-184)", b, d.cfg.Bridge, d.cfg.Bridge)
	}
	return nil
}

func checkSpec(s driver.GuestSpec) error {
	if !reBridge.MatchString(s.Bridge) {
		return fmt.Errorf("proxmox: bad bridge %q", s.Bridge)
	}
	if s.IP != "" && s.IP != "dhcp" {
		addr, bits, ok := strings.Cut(s.IP, "/")
		n, err := strconv.Atoi(bits)
		if !ok || !reIPv4.MatchString(addr) || err != nil || n < 0 || n > 32 {
			return fmt.Errorf("proxmox: bad ip %q", s.IP)
		}
	}
	if s.Gateway != "" && !reIPv4.MatchString(s.Gateway) {
		return fmt.Errorf("proxmox: bad gateway %q", s.Gateway)
	}
	if s.User != "" && !reUser.MatchString(s.User) {
		return fmt.Errorf("proxmox: bad user %q", s.User)
	}
	if s.ImageVersion < 0 {
		return fmt.Errorf("proxmox: bad image version %d", s.ImageVersion)
	}
	if s.Cores < 1 || s.MemoryMB < 16 || s.DiskGB < 1 {
		return errors.New("proxmox: cores, memory and disk must be positive")
	}
	// The Image name goes into the description marker (D-189): one line.
	if strings.TrimSpace(s.ImageName) != s.ImageName || s.ImageName == "" || strings.ContainsAny(s.ImageName, "\r\n") {
		return fmt.Errorf("proxmox: bad image name %q", s.ImageName)
	}
	return nil
}

func (d *Driver) configure(ctx context.Context, id int, spec driver.GuestSpec, token string) error {
	return d.configureWith(ctx, id, spec, token, nil)
}

// configureWith is configure with a last change of the parameters (the
// build guest of CreateBuild has its own description, onboot and ciupgrade).
func (d *Driver) configureWith(ctx context.Context, id int, spec driver.GuestSpec, token string, tweak func(url.Values)) error {
	cfg, err := d.config(ctx, id)
	if err != nil {
		return err
	}
	params := url.Values{
		"cores":       {strconv.Itoa(spec.Cores)},
		"memory":      {strconv.Itoa(spec.MemoryMB)},
		"net0":        {"virtio,bridge=" + spec.Bridge},
		"ipconfig0":   {ipConfig(spec)},
		"ciuser":      {spec.User},
		"cicustom":    {"vendor=" + d.cfg.SnippetStorage + ":snippets/" + d.cfg.Snippet},
		"smbios1":     {smbios(cfg["smbios1"], token)},
		"description": {imageMarker + spec.ImageName + "\nManaged by Plumb. Do not change it here."},
		"onboot":      {"1"},
		// No package upgrade at first boot outside a Plan (D-186); Proxmox
		// sets package_upgrade: true without it. Create-only (D-185).
		"ciupgrade": {"0"},
		// DNS from the controller settings, never from the host (D-187).
		"nameserver":   {strings.Join(d.cfg.Nameservers, " ")},
		"searchdomain": {d.cfg.SearchDomain},
	}
	if len(spec.SSHKeys) > 0 {
		params.Set("sshkeys", sshKeysParam(spec.SSHKeys))
	}
	if tags, err := tagsParam(spec.Tags); err != nil {
		return err
	} else if tags != "" {
		params.Set("tags", tags)
	}
	if spec.ImageVersion > 0 {
		// Information only (D-200): Observe reads it, Diff ignores it.
		params.Set("description", fmt.Sprintf("%s%s\n%s%d\nManaged by Plumb. Do not change it here.",
			imageMarker, spec.ImageName, versionMarker, spec.ImageVersion))
	}
	if tweak != nil {
		tweak(params)
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/config", params); err != nil {
		return fmt.Errorf("proxmox: configure: %w", err)
	}
	if err := d.c.Task(ctx, d.cfg.Node, "PUT", vmPath(d, id)+"/resize", url.Values{
		"disk": {"scsi0"}, "size": {strconv.Itoa(spec.DiskGB) + "G"},
	}); err != nil {
		return fmt.Errorf("proxmox: resize disk: %w", err)
	}
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/status/start", nil); err != nil {
		return fmt.Errorf("proxmox: start: %w", err)
	}
	return nil
}

// DefaultShutdownTimeout is the clean shutdown time of a restart before
// Proxmox powers the guest off hard (forceStop). The Plan text of a
// restart names it ("hard power-off after 2 minutes", D-194), so a test
// pins it.
const DefaultShutdownTimeout = 120 * time.Second

// --- Observe ---

// config reads the current config of a guest as a flat map.
func (d *Driver) config(ctx context.Context, id int) (map[string]string, error) {
	return d.readConfig(ctx, id, true)
}

// readConfig reads one view of the config: current=1 gives the values
// that the running guest uses; without it Proxmox gives the values after
// the next stop and start, the pending ones applied (T-015 run 1).
func (d *Driver) readConfig(ctx context.Context, id int, current bool) (map[string]string, error) {
	return d.readPath(ctx, vmPath(d, id), current)
}

// readPath reads one config view of a VM or a container at path.
func (d *Driver) readPath(ctx context.Context, path string, current bool) (map[string]string, error) {
	var q url.Values
	if current {
		q = url.Values{"current": {"1"}}
	}
	var raw map[string]any
	if err := d.c.Get(ctx, path+"/config", q, &raw); err != nil {
		return nil, notFound(err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case []any:
			// The raw lxc.* lines of a container: [[key, value], …].
			var lines []string
			for _, kv := range v {
				if p, ok := kv.([]any); ok && len(p) == 2 {
					lines = append(lines, fmt.Sprintf("%v: %v", p[0], p[1]))
				} else {
					lines = append(lines, fmt.Sprint(kv))
				}
			}
			out[k] = strings.Join(lines, "\n")
		case string:
			out[k] = v
		case float64:
			out[k] = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			out[k] = fmt.Sprint(v)
		}
	}
	return out, nil
}

// parseProps splits a Proxmox property string "a=1,b=2" (or "1,b=2"; the
// first key-less value goes under "").
func parseProps(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			out[""] = part
			continue
		}
		out[k] = v
	}
	return out
}

// sizeGB parses "20G", "512M" or "1T".
func sizeGB(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0
	}
	switch s[len(s)-1] {
	case 'T':
		return int(n * 1024)
	case 'G':
		return int(n)
	case 'M':
		return int(n / 1024)
	}
	return 0
}

// sizeMB reads a Proxmox disk size ("10G", "3584M", "1T", "512K") in MiB,
// rounded up. 0 means unknown.
func sizeMB(s string) int {
	if len(s) < 2 {
		return 0
	}
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil || n < 0 {
		return 0
	}
	var mb float64
	switch s[len(s)-1] {
	case 'T':
		mb = n * 1024 * 1024
	case 'G':
		mb = n * 1024
	case 'M':
		mb = n
	case 'K':
		mb = n / 1024
	default:
		return 0
	}
	return int(math.Ceil(mb))
}

// specOf reads the managed settings from a config map.
func specOf(name string, cfg map[string]string) driver.GuestSpec {
	s := driver.GuestSpec{Name: name, Kind: wire.KindVM}
	s.Cores, _ = strconv.Atoi(cfg["cores"])
	if s.Cores == 0 {
		s.Cores = 1 // the Proxmox default
	}
	mem := parseProps(cfg["memory"]) // "2048" or "current=2048" (to verify on host)
	if v, ok := mem["current"]; ok {
		s.MemoryMB, _ = strconv.Atoi(v)
	} else {
		s.MemoryMB, _ = strconv.Atoi(mem[""])
	}
	net := parseProps(cfg["net0"])
	s.Bridge = net["bridge"]
	ip := parseProps(cfg["ipconfig0"])
	s.IP, s.Gateway = ip["ip"], ip["gw"]
	s.User = cfg["ciuser"]
	// PathUnescape: a raw "+" stays "+" (review T-012 #11).
	if keys, err := url.PathUnescape(cfg["sshkeys"]); err == nil && keys != "" {
		for _, k := range strings.Split(strings.TrimSpace(keys), "\n") {
			if k = strings.TrimSpace(k); k != "" {
				s.SSHKeys = append(s.SSHKeys, k)
			}
		}
	}
	s.DiskGB = sizeGB(parseProps(cfg["scsi0"])["size"])
	s.Tags = parseTags(cfg["tags"])
	for _, line := range strings.Split(cfg["description"], "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), imageMarker); ok {
			s.ImageName = name
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), versionMarker); ok {
			s.ImageVersion, _ = strconv.Atoi(v)
		}
	}
	return s
}

// Observe reads the settings, the config digest, the power state and the
// addresses. Without the QEMU guest agent (D-011), Proxmox knows only a
// static address; a DHCP guest has no IPs here (D-079).
func (d *Driver) Observe(ctx context.Context, gid driver.GuestID) (driver.GuestState, error) {
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return driver.GuestState{}, err
	}
	if m.Type == typeCT {
		return d.observeCT(ctx, id)
	}
	cfg, next, err := d.views(ctx, id)
	if err != nil {
		return driver.GuestState{}, err
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, vmPath(d, id)+"/status/current", nil, &status); err != nil {
		return driver.GuestState{}, notFound(err)
	}
	name := cfg["name"]
	if name == "" {
		name = m.Name
	}
	st := driver.GuestState{Spec: specOf(name, cfg), Digest: cfg["digest"], Power: driver.PowerStopped}
	if p := specOf(name, next); !reflect.DeepEqual(p, st.Spec) {
		st.Pending = &p
	}
	st.TokenPending = parseProps(cfg["smbios1"])["serial"] != parseProps(next["smbios1"])["serial"]
	if status.Status == "running" {
		st.Power = driver.PowerRunning
	}
	if ip := st.Spec.IP; ip != "" && ip != "dhcp" {
		st.IPs = []string{strings.SplitN(ip, "/", 2)[0]}
	}
	return st, nil
}

// viewTries is how often views reads both views before it gives up.
const viewTries = 3

// views reads the running config and the config after the next stop and
// start (D-190). Proxmox gives both answers the same digest in every
// state (host check 1, T-056), so equal digests mean one state of the
// config file; a write between the two reads makes the driver read again.
func (d *Driver) views(ctx context.Context, id int) (running, next map[string]string, err error) {
	for range viewTries {
		if running, err = d.readConfig(ctx, id, true); err != nil {
			return nil, nil, err
		}
		if next, err = d.readConfig(ctx, id, false); err != nil {
			return nil, nil, err
		}
		if running["digest"] == "" || next["digest"] == "" {
			// Two missing digests are not one state; without a digest a
			// later write cannot be checked either (D-078).
			return nil, nil, fmt.Errorf("proxmox: VMID %d: the config answer has no digest", id)
		}
		if running["digest"] == next["digest"] {
			return running, next, nil
		}
	}
	return nil, nil, fmt.Errorf("proxmox: VMID %d: the config changed during each of %d reads; try again later", id, viewTries)
}

// Diff is the shared rule of all drivers (D-063). Proxmox can hot-plug
// cores and memory only with hotplug and NUMA settings that Plumb does not
// set, so both stay disruptive.
func (d *Driver) Diff(desired driver.GuestSpec, observed driver.GuestState) ([]wire.Change, error) {
	return driver.DiffGuest(desired, observed)
}

// --- Update, Power, Delete ---

// Update applies changes of cores, memory, bridge and tags, and for
// re-enrollment the new token as SMBIOS serial. A create-only attribute is
// refused. The serial takes effect at the next cold start (Power restart).
// A disk change grows the boot disk scsi0 after the config write (resize,
// D-299); a smaller value is refused before any call.
func (d *Driver) Update(ctx context.Context, gid driver.GuestID, changes []wire.Change, boot *driver.Bootstrap) error {
	// The bridge rule comes first, before any Proxmox call (D-184).
	for _, c := range changes {
		if c.Attr == driver.AttrBridge {
			b, _ := c.To.(string)
			if err := d.checkBridge(b); err != nil {
				return err
			}
		}
	}
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	if m.Type == typeCT {
		return d.updateCT(ctx, id, changes, boot)
	}
	cfg, err := d.config(ctx, id)
	if err != nil {
		return err
	}
	params := url.Values{"digest": {cfg["digest"]}} // refuse when the config changed meanwhile
	grow := 0
	for _, c := range changes {
		if driver.IsCreateOnly(c.Attr) {
			return &driver.CreateOnlyError{Attr: c.Attr}
		}
		switch c.Attr {
		case driver.AttrTags:
			if err := setTags(params, c.To); err != nil {
				return err
			}
		case driver.AttrDisk:
			n, err := growTo(c.To, sizeGB(parseProps(cfg["scsi0"])["size"]))
			if err != nil {
				return err
			}
			grow = n
		case driver.AttrCores:
			n, err := driver.IntValue(c.To)
			if err != nil || n < 1 {
				return fmt.Errorf("proxmox: cores %v", c.To)
			}
			params.Set("cores", strconv.Itoa(n))
		case driver.AttrMemory:
			n, err := driver.IntValue(c.To)
			if err != nil || n < 16 {
				return fmt.Errorf("proxmox: memory %v", c.To)
			}
			params.Set("memory", strconv.Itoa(n))
		case driver.AttrBridge:
			b, ok := c.To.(string)
			if !ok || b == "" || strings.ContainsAny(b, ",=") {
				return fmt.Errorf("proxmox: bridge %v", c.To)
			}
			params.Set("net0", replaceProp(cfg["net0"], "bridge", b))
		case driver.AttrEnrollToken:
			if !c.Sensitive || c.From != nil || c.To != nil || boot == nil || boot.Token == "" {
				return errors.New("proxmox: enroll_token needs a Bootstrap and carries no value")
			}
			params.Set("smbios1", smbios(cfg["smbios1"], boot.Token))
		default:
			return fmt.Errorf("proxmox: unknown attribute %q", c.Attr)
		}
	}
	if len(params) > 1 {
		if err := checkUpdateParams(cfg, params); err != nil {
			return err
		}
		token := ""
		if boot != nil {
			token = boot.Token
		}
		if err := scrub(d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/config", params), token); err != nil {
			return tagRefusal(err, params)
		}
	}
	if grow > 0 {
		return d.resize(ctx, vmPath(d, id), "scsi0", grow)
	}
	return nil
}

// resize grows one disk to size GiB (VM.Config.Disk and
// Datastore.AllocateSpace, D-081). Proxmox refuses a smaller size itself;
// the callers check before.
func (d *Driver) resize(ctx context.Context, path, disk string, size int) error {
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "PUT", path+"/resize", url.Values{
		"disk": {disk}, "size": {strconv.Itoa(size) + "G"},
	}); err != nil {
		return fmt.Errorf("proxmox: grow disk %s to %d GiB: %w", disk, size, err)
	}
	return nil
}

// tagRefusal explains a 403 of a config write with tags (D-299): the
// datacenter option user-tag-access (other than free) or a registered tag
// can refuse a tag that the token may otherwise write (VM.Config.Options).
func tagRefusal(err error, params url.Values) error {
	var ae *APIError
	_, tags := params["tags"]
	if !tags && params.Get("delete") != "tags" || !errors.As(err, &ae) || ae.Status != 403 {
		return err
	}
	return fmt.Errorf("proxmox: the tags were refused: the datacenter option user-tag-access (other than free) or a registered tag can refuse a tag write, "+
		"and Plumb gets no new right for it (D-081). Allow the tags in Datacenter → Options → Tag Style or User Tag Access, or remove them from the Target: %w", err)
}

// growTo checks a new disk size against the size of the guest: grow only.
// It returns 0 when the size is already right.
func growTo(v any, have int) (int, error) {
	n, err := driver.IntValue(v)
	switch {
	case err != nil || n < 1:
		return 0, fmt.Errorf("proxmox: disk %v", v)
	case n < have:
		return 0, &driver.ShrinkError{Have: have, Want: n}
	case n == have:
		return 0, nil
	}
	return n, nil
}

// reTag is a tag that Plumb writes (D-299; the Target schema has the
// same pattern). Proxmox separates tags by ";".
var reTag = regexp.MustCompile(`^[a-z0-9_][a-z0-9_+.-]{0,63}$`)

// parseTags reads the tags option ("a;b", Proxmox also takes "," and
// spaces) in the form of driver.NormTags.
func parseTags(v string) []string {
	return driver.NormTags(strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }))
}

// tagsParam is the tags option of a set: "" for none.
func tagsParam(tags []string) (string, error) {
	tags = driver.NormTags(tags)
	if len(tags) > 32 {
		return "", fmt.Errorf("proxmox: %d tags; at most 32", len(tags))
	}
	for _, t := range tags {
		if !reTag.MatchString(t) {
			return "", fmt.Errorf("proxmox: tag %q is not valid", t)
		}
	}
	return strings.Join(tags, ";"), nil
}

// setTags sets the tags of a change value (a string list) in params: the
// option, or a delete of it for no tags.
func setTags(params url.Values, v any) error {
	var tags []string
	switch l := v.(type) {
	case nil:
	case []string:
		tags = l
	case []any:
		for _, t := range l {
			s, ok := t.(string)
			if !ok {
				return fmt.Errorf("proxmox: tags %v", v)
			}
			tags = append(tags, s)
		}
	default:
		return fmt.Errorf("proxmox: tags %v", v)
	}
	p, err := tagsParam(tags)
	if err != nil {
		return err
	}
	if p == "" {
		params.Set("delete", "tags")
	} else {
		params.Set("tags", p)
	}
	return nil
}

// updateKeys are the only config keys that Update may write (D-185).
// Proxmox makes the cloud-init instance-id from the user and network data;
// a change of any cloud-init input (ciuser, sshkeys, ipconfigN, nameserver,
// searchdomain, citype, ciupgrade, cicustom, name, the MAC of net0) starts a
// new cloud-init instance at the next cold start: first boot again, and new
// SSH host keys without ssh_deletekeys (test host, T-012 item 19).
// tags (D-299) is no cloud-init input; "delete" may only name it.
var updateKeys = map[string]bool{"digest": true, "cores": true, "memory": true, "net0": true, "smbios1": true, "tags": true, "delete": true}

// checkUpdateParams is the last guard before Update writes: only
// updateKeys; a new net0 keeps the MAC and every other property except
// the bridge; a new smbios1 keeps the uuid (review N1).
func checkUpdateParams(cfg map[string]string, params url.Values) error {
	for k := range params {
		if !updateKeys[k] {
			return fmt.Errorf("proxmox: Update must not write %q (cloud-init inputs are create-only, D-185)", k)
		}
	}
	if del, ok := params["delete"]; ok && (len(del) != 1 || del[0] != "tags") {
		return fmt.Errorf("proxmox: Update may delete only the tags, not %q", del)
	}
	if net0, ok := params["net0"]; ok {
		mac := macOf(cfg["net0"])
		if mac == "" || len(net0) != 1 || macOf(net0[0]) != mac || !sameExcept(cfg["net0"], net0[0], "bridge") {
			return errors.New("proxmox: a net0 change must keep the MAC address and every property but the bridge (a new MAC is a new cloud-init instance, D-185)")
		}
	}
	if sm, ok := params["smbios1"]; ok {
		uuid := parseProps(cfg["smbios1"])["uuid"]
		if uuid == "" || len(sm) != 1 || parseProps(sm[0])["uuid"] != uuid {
			return errors.New("proxmox: an smbios1 change must keep the uuid")
		}
	}
	return nil
}

// sameExcept reports whether two property lists are equal except for one
// key (values compared without case, for the MAC).
func sameExcept(a, b, key string) bool {
	pa, pb := parseProps(a), parseProps(b)
	delete(pa, key)
	delete(pb, key)
	if len(pa) != len(pb) {
		return false
	}
	for k, v := range pa {
		if w, ok := pb[k]; !ok || !strings.EqualFold(v, w) {
			return false
		}
	}
	return true
}

// macOf returns the MAC of a net0 value ("virtio=BC:24:…,bridge=…"), or "".
func macOf(net0 string) string {
	for _, p := range strings.Split(net0, ",") {
		if _, v, ok := strings.Cut(p, "="); ok && macRE.MatchString(v) {
			return strings.ToUpper(v)
		}
	}
	return ""
}

var macRE = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$`)

// replaceProp sets one key of a property string and keeps the rest (for
// net0: the model and the MAC address stay).
func replaceProp(s, key, value string) string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		if k, _, ok := strings.Cut(p, "="); ok && k == key {
			parts[i] = key + "=" + value
			return strings.Join(parts, ",")
		}
	}
	if s == "" {
		return key + "=" + value
	}
	return s + "," + key + "=" + value
}

func (d *Driver) status(ctx context.Context, id int) (string, error) {
	var st struct {
		Status string `json:"status"`
	}
	err := d.c.Get(ctx, vmPath(d, id)+"/status/current", nil, &st)
	return st.Status, notFound(err)
}

// Power runs a power action. Restart is a shutdown and a start, not a
// reboot: only a cold start gives the guest a new SMBIOS serial (D-078).
func (d *Driver) Power(ctx context.Context, gid driver.GuestID, action driver.PowerAction) error {
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	path := vmPath(d, id)
	if m.Type == typeCT {
		path = ctPath(d, id)
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, path+"/status/current", nil, &st); err != nil {
		return notFound(err)
	}
	// A container takes its pending values and a new env token at the
	// same cold start (host C4): shutdown and start, as a VM.
	stop := func() error {
		if st.Status != "running" {
			return nil
		}
		return d.c.Task(ctx, d.cfg.Node, "POST", path+"/status/shutdown", url.Values{
			"timeout": {strconv.Itoa(int(d.cfg.ShutdownTimeout.Seconds()))}, "forceStop": {"1"},
		})
	}
	start := func() error { return d.c.Task(ctx, d.cfg.Node, "POST", path+"/status/start", nil) }
	switch action {
	case driver.PowerStart:
		if st.Status == "running" {
			return nil
		}
		return start()
	case driver.PowerStop:
		return stop()
	case driver.PowerRestart:
		if err := stop(); err != nil {
			return err
		}
		return start()
	}
	return fmt.Errorf("proxmox: unknown power action %q", action)
}

// Delete stops the guest and removes it with its disks.
func (d *Driver) Delete(ctx context.Context, gid driver.GuestID) error {
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	if m.Template == 1 {
		return fmt.Errorf("proxmox: VMID %d is a template; images are not deleted by Delete", id)
	}
	if m.Type == typeCT {
		return d.deleteCT(ctx, id)
	}
	st, err := d.status(ctx, id)
	if err != nil {
		return err
	}
	if st == "running" {
		if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/status/stop", nil); err != nil {
			return err
		}
	}
	return d.c.Task(ctx, d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}})
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
