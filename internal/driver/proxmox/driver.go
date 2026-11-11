package proxmox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	// ImageStorage holds downloaded images (content type "import").
	ImageStorage string
	// SnippetStorage and Snippet name the static vendor snippet that the
	// installer wrote (D-025). The driver never writes it.
	SnippetStorage string
	Snippet        string
	// VMIDMin and VMIDMax bound the ids of new guests and templates.
	VMIDMin, VMIDMax int
	// ShutdownTimeout is the clean shutdown time before a hard stop. Default 120 s.
	ShutdownTimeout time.Duration
}

// Driver is the Proxmox VE driver.
type Driver struct {
	cfg Config
	c   *Client
}

var _ driver.Driver = (*Driver)(nil)

// New returns the driver.
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
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 120 * time.Second
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
// host, T-012). Membership itself is current (pool config). So the driver
// never trusts Template of the list; own and isTemplate read the guest
// config instead.
type member struct {
	VMID     int    `json:"vmid"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Template int    `json:"template"` // stale for a few seconds; see own
}

// members lists the qemu guests in the pool.
func (d *Driver) members(ctx context.Context) ([]member, error) {
	var pools []struct {
		PoolID  string   `json:"poolid"`
		Members []member `json:"members"`
	}
	if err := d.c.Get(ctx, "/pools", url.Values{"poolid": {d.cfg.Pool}, "type": {"qemu"}}, &pools); err != nil {
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
			if m.Type == "qemu" || m.Type == "" {
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
	ms, err := d.members(ctx)
	if err != nil {
		return 0, member{}, err
	}
	for _, m := range ms {
		if m.VMID == id {
			t, err := d.isTemplate(ctx, id)
			if err != nil {
				return 0, member{}, err
			}
			m.Template = 0
			if t {
				m.Template = 1
			}
			return id, m, nil
		}
	}
	// Not in the pool: either gone, or someone else's. The token can only
	// see pool guests, so both look the same; it is not ours either way.
	return 0, member{}, fmt.Errorf("VMID %d is not in pool %s: %w", id, d.cfg.Pool, driver.ErrNotFound)
}

// nextID returns a free VMID in the range.
func (d *Driver) nextID(ctx context.Context) (int, error) {
	for id := d.cfg.VMIDMin; id <= d.cfg.VMIDMax; id++ {
		var got string
		err := d.c.Get(ctx, "/cluster/nextid", url.Values{"vmid": {strconv.Itoa(id)}}, &got)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 400 && strings.Contains(ae.Error(), "already exists") {
			continue // taken
		}
		if err != nil {
			return 0, err
		}
		return id, nil
	}
	return 0, fmt.Errorf("proxmox: no free VMID in %d–%d", d.cfg.VMIDMin, d.cfg.VMIDMax)
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
// template it was cloned from. Proxmox keeps no such link (D-077).
const imageMarker = "plumb-image: "

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// --- FetchImage ---

// FetchImage downloads the image into the image storage with the pinned
// SHA-256 (Proxmox checks it), creates a VM that imports it, adds a
// cloud-init drive and turns it into a template. It is idempotent: an
// existing template of the same name and checksum is reused.
func (d *Driver) FetchImage(ctx context.Context, spec driver.ImageSpec) (driver.ImageRef, error) {
	if spec.Kind != wire.KindVM {
		return "", fmt.Errorf("proxmox: only VM images in this version, not %q", spec.Kind)
	}
	if !sha256Hex.MatchString(spec.SHA256) || !strings.HasPrefix(spec.URL, "https://") {
		return "", errors.New("proxmox: an image needs an https URL and a SHA-256")
	}
	name := templateName(spec)
	ms, err := d.members(ctx)
	if err != nil {
		return "", err
	}
	// The template flag comes from the guest config, not from the list
	// (see member): a fresh template must never look half-made.
	var halfMade []int
	for _, m := range ms {
		if m.Name != name || m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
			continue
		}
		t, err := d.isTemplate(ctx, m.VMID)
		if err != nil {
			return "", err
		}
		if t {
			return driver.ImageRef(strconv.Itoa(m.VMID)), nil
		}
		halfMade = append(halfMade, m.VMID)
	}
	// A VM with the template's name that is no template is the rest of a
	// failed earlier try: remove it before the next try (review T-012 #6).
	for _, id := range halfMade {
		d.removeHalfMade(ctx, id, name)
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
	if err := d.c.Task(ctx, d.cfg.Node, "POST", d.node()+"/qemu", url.Values{
		"vmid": {strconv.Itoa(id)}, "name": {name}, "pool": {d.cfg.Pool},
		"ostype": {"l26"}, "memory": {"1024"}, "cores": {"1"}, "cpu": {"x86-64-v2-AES"},
		"scsihw": {"virtio-scsi-single"}, "scsi0": {d.cfg.DiskStorage + ":0,import-from=" + volid},
		"ide2": {d.cfg.DiskStorage + ":cloudinit"}, "boot": {"order=scsi0"},
		"serial0": {"socket"}, "vga": {"serial0"},
		"description": {"Plumb image " + spec.Name + "\nsha256=" + spec.SHA256},
	}); err != nil {
		d.removeHalfMade(ctx, id, name)
		return "", fmt.Errorf("proxmox: create template VM: %w", err)
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/template", nil); err != nil {
		d.removeHalfMade(ctx, id, name)
		return "", fmt.Errorf("proxmox: convert to template: %w", err)
	}
	return driver.ImageRef(strconv.Itoa(id)), nil
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
	if spec.Kind != wire.KindVM {
		return "", fmt.Errorf("proxmox: only VMs in this version, not %q", spec.Kind)
	}
	if boot.Token == "" {
		return "", errors.New("proxmox: a new guest needs an enrollment token")
	}
	if err := checkSpec(spec); err != nil {
		return "", err
	}
	tpl, m, err := d.own(ctx, string(spec.Image))
	if err != nil {
		return "", fmt.Errorf("proxmox: image %s: %w", spec.Image, err)
	}
	if m.Template != 1 {
		return "", fmt.Errorf("proxmox: VMID %d is not a template", tpl)
	}
	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, tpl)+"/clone", url.Values{
		"newid": {strconv.Itoa(id)}, "name": {spec.Name}, "pool": {d.cfg.Pool},
		"full": {"1"}, "storage": {d.cfg.DiskStorage},
	}); err != nil {
		// A clone that failed or was stopped after Proxmox made the VM
		// leaves a half-made guest (review N2).
		d.removeHalfMade(ctx, id, spec.Name)
		return "", fmt.Errorf("proxmox: clone: %w", err)
	}
	gid := driver.GuestID(strconv.Itoa(id))
	if err := d.configure(ctx, id, tpl, spec, boot.Token); err != nil {
		d.removeHalfMade(ctx, id, spec.Name)
		return "", scrub(err, boot.Token)
	}
	return gid, nil
}

// removeHalfMade deletes a guest that a failed Create or FetchImage left
// behind. It is best effort, with its own deadline. Like every change, it
// first checks that the VMID is ours: in the range, in the pool, with the
// name this call gave it and no template. A failed create can mean that
// someone else took the id after nextID; that guest is never touched
// (review N1).
func (d *Driver) removeHalfMade(ctx context.Context, id int, name string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	_, m, err := d.own(cleanup, strconv.Itoa(id))
	if err != nil || m.Name != name || m.Template == 1 {
		return
	}
	if st, err := d.status(cleanup, id); err == nil && st == "running" {
		_ = d.c.Task(cleanup, d.cfg.Node, "POST", vmPath(d, id)+"/status/stop", nil)
	}
	_ = d.c.Task(cleanup, d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}})
}

// The driver is the last step before the host: values that go into a
// Proxmox property string must not carry "," or "=" (review T-012 #5).
var (
	reBridge = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
	reIPv4   = regexp.MustCompile(`^(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])){3}$`)
	reUser   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

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
	if s.Cores < 1 || s.MemoryMB < 16 || s.DiskGB < 1 {
		return errors.New("proxmox: cores, memory and disk must be positive")
	}
	return nil
}

func (d *Driver) configure(ctx context.Context, id, tpl int, spec driver.GuestSpec, token string) error {
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
		"description": {imageMarker + strconv.Itoa(tpl) + "\nManaged by Plumb. Do not change it here."},
		"onboot":      {"1"},
	}
	if len(spec.SSHKeys) > 0 {
		params.Set("sshkeys", sshKeysParam(spec.SSHKeys))
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

// --- Observe ---

// config reads the current config of a guest as a flat map.
// isTemplate reads the template flag from the guest config (current).
func (d *Driver) isTemplate(ctx context.Context, id int) (bool, error) {
	cfg, err := d.config(ctx, id)
	if err != nil {
		return false, err
	}
	return cfg["template"] == "1", nil
}

func (d *Driver) config(ctx context.Context, id int) (map[string]string, error) {
	var raw map[string]any
	if err := d.c.Get(ctx, vmPath(d, id)+"/config", url.Values{"current": {"1"}}, &raw); err != nil {
		return nil, notFound(err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
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
	for _, line := range strings.Split(cfg["description"], "\n") {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(line), imageMarker); ok {
			s.Image = driver.ImageRef(ref)
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
	cfg, err := d.config(ctx, id)
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
	if status.Status == "running" {
		st.Power = driver.PowerRunning
	}
	if ip := st.Spec.IP; ip != "" && ip != "dhcp" {
		st.IPs = []string{strings.SplitN(ip, "/", 2)[0]}
	}
	return st, nil
}

// Diff is the shared rule of all drivers (D-063). Proxmox can hot-plug
// cores and memory only with hotplug and NUMA settings that Plumb does not
// set, so both stay disruptive.
func (d *Driver) Diff(desired driver.GuestSpec, observed driver.GuestState) ([]wire.Change, error) {
	return driver.DiffGuest(desired, observed)
}

// --- Update, Power, Delete ---

// Update applies changes of cores, memory and bridge, and for
// re-enrollment the new token as SMBIOS serial. A create-only attribute is
// refused. The serial takes effect at the next cold start (Power restart).
func (d *Driver) Update(ctx context.Context, gid driver.GuestID, changes []wire.Change, boot *driver.Bootstrap) error {
	id, _, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	cfg, err := d.config(ctx, id)
	if err != nil {
		return err
	}
	params := url.Values{"digest": {cfg["digest"]}} // refuse when the config changed meanwhile
	for _, c := range changes {
		if driver.IsCreateOnly(c.Attr) {
			return &driver.CreateOnlyError{Attr: c.Attr}
		}
		switch c.Attr {
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
	if len(params) == 1 {
		return nil
	}
	token := ""
	if boot != nil {
		token = boot.Token
	}
	return scrub(d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/config", params), token)
}

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
	id, _, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	st, err := d.status(ctx, id)
	if err != nil {
		return err
	}
	stop := func() error {
		if st != "running" {
			return nil
		}
		return d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/status/shutdown", url.Values{
			"timeout": {strconv.Itoa(int(d.cfg.ShutdownTimeout.Seconds()))}, "forceStop": {"1"},
		})
	}
	start := func() error { return d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/status/start", nil) }
	switch action {
	case driver.PowerStart:
		if st == "running" {
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
