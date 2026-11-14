package proxmox

// LXC containers (T-038 §3, T-079). A container is an unprivileged
// Proxmox CT with nesting=1 (D-227), made from a repacked template file
// on the image storage (D-229, D-230). Its enrollment token is the env
// entry PLUMB_ENROLL_TOKEN (D-231). The token-facing facts are from the
// test host (docs/tasks/T-038.log.md, T-079.log.md).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/proxmox/ctpack"
	"github.com/Bernotese/plumb/internal/wire"
)

func ctPath(d *Driver, id int) string { return d.node() + "/lxc/" + strconv.Itoa(id) }

// ctSwapMB is the swap of every container (T-038 §3).
const ctSwapMB = 512

// sshKeyMarker is a description line per SSH key of a container (D-249).
// Proxmox writes ssh-public-keys into the container at create and keeps no
// copy, so Observe reads the create-only attribute ssh_keys from here (as
// the image marker, D-189). Public keys only. A person who edits or removes
// a line makes ssh_keys differ: a guest:config Issue, and a Plan says
// "create a new target"; the driver never re-creates. state.lxcKeyLine is
// the same text.
const sshKeyMarker = "plumb-ssh-key: "

// maxDescription is the limit of a container description in PVE 9.2
// (PVE/LXC/Config.pm: maxLength 8192).
const maxDescription = 8192

// tokenPrefix starts the env entry of the enrollment token (D-231).
const tokenPrefix = ctpack.TokenEnv + "="

// ctConfig reads one view of a container config (see readConfig):
// current=1 gives the values of the running container, without it the
// values after the next stop and start.
func (d *Driver) ctConfig(ctx context.Context, id int, current bool) (map[string]string, error) {
	return d.readPath(ctx, ctPath(d, id), current)
}

// ctViews is views for a container: both reads must show one digest.
func (d *Driver) ctViews(ctx context.Context, id int) (running, next map[string]string, err error) {
	for range viewTries {
		if running, err = d.ctConfig(ctx, id, true); err != nil {
			return nil, nil, err
		}
		if next, err = d.ctConfig(ctx, id, false); err != nil {
			return nil, nil, err
		}
		if running["digest"] == "" || next["digest"] == "" {
			return nil, nil, fmt.Errorf("proxmox: CT %d: the config answer has no digest", id)
		}
		if running["digest"] == next["digest"] {
			return running, next, nil
		}
	}
	return nil, nil, fmt.Errorf("proxmox: CT %d: the config changed during each of %d reads; try again later", id, viewTries)
}

// envToken returns the token entry of an env value ("" without one). It
// is a secret: never put it into an error.
func envToken(env string) string {
	for _, e := range strings.Split(env, "\x00") {
		if v, ok := strings.CutPrefix(e, tokenPrefix); ok {
			return v
		}
	}
	return ""
}

// specOfCT reads the managed settings of a container config.
func specOfCT(cfg map[string]string) driver.GuestSpec {
	s := driver.GuestSpec{Name: cfg["hostname"], Kind: wire.KindLXC}
	s.Cores, _ = strconv.Atoi(cfg["cores"])
	s.MemoryMB, _ = strconv.Atoi(cfg["memory"])
	net := parseProps(cfg["net0"])
	s.Bridge = net["bridge"]
	s.IP, s.Gateway = net["ip"], net["gw"]
	s.NICs = nicsOf(cfg, true) // T-219
	s.DiskGB = sizeGB(parseProps(cfg["rootfs"])["size"])
	s.Tags = parseTags(cfg["tags"])
	s.Features = sortedProps(cfg["features"])
	for k, v := range cfg {
		switch {
		case reMountKey.MatchString(k):
			s.Mounts = append(s.Mounts, k+"="+v)
		case reDeviceKey.MatchString(k):
			s.Devices = append(s.Devices, k+"="+v)
		case k == "lxc" && v != "":
			s.RawLXC = strings.Split(v, "\n")
		}
	}
	slices.Sort(s.Mounts)
	slices.Sort(s.Devices)
	for _, line := range strings.Split(cfg["description"], "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, imageMarker); ok {
			s.ImageName = v
		}
		if v, ok := strings.CutPrefix(line, versionMarker); ok {
			s.ImageVersion, _ = strconv.Atoi(v)
		}
		if v, ok := strings.CutPrefix(line, sshKeyMarker); ok && v != "" {
			s.SSHKeys = append(s.SSHKeys, v)
		}
	}
	return s
}

var (
	reMountKey  = regexp.MustCompile(`^mp[0-9]+$`)
	reDeviceKey = regexp.MustCompile(`^dev[0-9]+$`)
)

// sortedProps puts the keys of a property string in order, so
// "keyctl=1,nesting=1" and "nesting=1,keyctl=1" compare equal.
func sortedProps(v string) string {
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ",")
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// templateMarker is a description line that names the template file of a
// container: Proxmox keeps no ostemplate in the config, and the Image
// status must tell a file in use (T-085).
const templateMarker = "plumb-template: "

// ctDescription is the description of a new container: the image marker
// (D-189), the version of a built Image (D-200) and the SSH keys.
func ctDescription(spec driver.GuestSpec) string {
	var b strings.Builder
	b.WriteString(imageMarker + spec.ImageName + "\n")
	if spec.ImageVersion > 0 {
		fmt.Fprintf(&b, "%s%d\n", versionMarker, spec.ImageVersion)
	}
	if spec.Image != "" {
		b.WriteString(templateMarker + string(spec.Image) + "\n")
	}
	for _, k := range spec.SSHKeys {
		b.WriteString(sshKeyMarker + k + "\n")
	}
	b.WriteString("Managed by Plumb. Do not change it here.")
	return b.String()
}

// reTemplateFile is the volid of a repacked template on the image storage
// (ctpack.Name). The storage name is checked against the config.
var reTemplateFile = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.-]*):vztmpl/(plumb-ct-[a-z0-9-]+-[0-9a-f]{12}\.tar\.gz)$`)

func (d *Driver) checkTemplateRef(ref driver.ImageRef) error {
	m := reTemplateFile.FindStringSubmatch(string(ref))
	if m == nil || m[1] != d.cfg.ImageStorage {
		return fmt.Errorf("proxmox: %q is no Plumb container template on %s", ref, d.cfg.ImageStorage)
	}
	return nil
}

func checkCTSpec(s driver.GuestSpec) error { return checkCTSpecName(s, false) }

// checkCTSpecName is checkCTSpec; dotted allows a name with dots (the
// build container of an Image whose name holds a dot).
func checkCTSpecName(s driver.GuestSpec, dotted bool) error {
	if err := checkSpec(s); err != nil {
		return err
	}
	if s.User != "" {
		return errors.New("proxmox: a container has no bootstrap user (D-226)")
	}
	if !dnsNameRE.MatchString(s.Name) || (!dotted && strings.Contains(s.Name, ".")) || len(s.Name) > 63 {
		return fmt.Errorf("proxmox: %q is no host name for a container", s.Name)
	}
	for _, k := range s.SSHKeys {
		// One description line that comes back byte-equal (D-249,
		// review S1 of #162).
		if k == "" || strings.TrimSpace(k) != k || strings.IndexFunc(k, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("proxmox: an SSH key must be one line without white space at its ends or control characters")
		}
	}
	return nil
}

// ctNet0 is the net0 value of a new container: Proxmox names the device
// eth0 and writes the address into the container (host D1).
func ctNet0(s driver.GuestSpec) string {
	v := "name=eth0,bridge=" + s.Bridge
	if s.IP == "" || s.IP == "dhcp" {
		return v + ",ip=dhcp"
	}
	v += ",ip=" + s.IP
	if s.Gateway != "" {
		v += ",gw=" + s.Gateway
	}
	return v
}

// createCT makes an unprivileged container from a template file and
// starts it. A failure after Proxmox made the container removes it.
func (d *Driver) createCT(ctx context.Context, spec driver.GuestSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	if _, err := vmid(string(spec.Image)); err == nil {
		return d.cloneCT(ctx, spec, boot) // a version of a built Image (T-082)
	}
	if err := checkCTSpec(spec); err != nil {
		return "", err
	}
	return d.createFromFile(ctx, spec, boot, ctDescription(spec), "1")
}

// createFromFile makes a container from a template file with the given
// description and onboot, and starts it (a Target or a build container).
func (d *Driver) createFromFile(ctx context.Context, spec driver.GuestSpec, boot driver.Bootstrap, desc, onboot string) (driver.GuestID, error) {
	if err := d.checkBridge(spec.Bridge); err != nil {
		return "", err
	}
	if err := d.checkNICs(spec.NICs, true); err != nil { // T-219
		return "", err
	}
	if err := d.checkTemplateRef(spec.Image); err != nil {
		return "", err
	}
	if len(desc) > maxDescription {
		return "", fmt.Errorf("proxmox: the description with the SSH keys has %d bytes; Proxmox allows %d (D-249)", len(desc), maxDescription)
	}
	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	defer d.releaseID(id)
	params := url.Values{
		"vmid":         {strconv.Itoa(id)},
		"ostemplate":   {string(spec.Image)},
		"pool":         {d.cfg.Pool},
		"hostname":     {spec.Name},
		"unprivileged": {"1"},
		"features":     {"nesting=1"}, // systemd needs it (host D4, D-227)
		"rootfs":       {d.cfg.DiskStorage + ":" + strconv.Itoa(spec.DiskGB)},
		"net0":         {ctNet0(spec)},
		"nameserver":   {strings.Join(d.cfg.Nameservers, " ")},
		"searchdomain": {d.cfg.SearchDomain},
		"cores":        {strconv.Itoa(spec.Cores)},
		"memory":       {strconv.Itoa(spec.MemoryMB)},
		"swap":         {strconv.Itoa(ctSwapMB)},
		"onboot":       {onboot},
		"description":  {desc},
		// The form encoding sends "=" as %3D, as Proxmox needs (host C2).
		"env": {tokenPrefix + boot.Token},
	}
	addNICParams(params, spec.NICs, true) // T-219
	if len(spec.SSHKeys) > 0 {
		params.Set("ssh-public-keys", strings.Join(spec.SSHKeys, "\n"))
	}
	if tags, err := tagsParam(spec.Tags); err != nil {
		return "", err
	} else if tags != "" {
		params.Set("tags", tags)
	}
	err = d.c.Task(ctx, d.cfg.Node, "POST", d.node()+"/lxc", params)
	if err != nil && archiveError(err) && ctx.Err() == nil {
		// A reused file may be damaged (review S10): remove the half-made
		// container, make the file once more, and try once again.
		if cerr := d.removeHalfMadeCT(ctx, id, spec.Name, false); cerr != nil {
			return "", scrub(halfMadeLeftCT(fmt.Errorf("proxmox: create container: %w", err), id, cerr), boot.Token)
		}
		d.ct.mu.Lock()
		ispec, known := d.ct.specs[spec.Image]
		d.ct.mu.Unlock()
		if known {
			if _, ferr := d.fetchCT(ctx, ispec, true); ferr != nil {
				return "", scrub(fmt.Errorf("proxmox: create container: %w; making the template again failed: %v", err, ferr), boot.Token)
			}
			err = d.c.Task(ctx, d.cfg.Node, "POST", d.node()+"/lxc", params)
		}
	}
	if err != nil {
		err = fmt.Errorf("proxmox: create container: %w", err)
		return "", scrub(halfMadeLeftCT(err, id, d.removeHalfMadeCT(ctx, id, spec.Name, false)), boot.Token)
	}
	if err := d.c.Task(ctx, d.cfg.Node, "POST", ctPath(d, id)+"/status/start", nil); err != nil {
		err = fmt.Errorf("proxmox: start: %w", err)
		return "", scrub(halfMadeLeftCT(err, id, d.removeHalfMadeCT(ctx, id, spec.Name, true)), boot.Token)
	}
	return driver.GuestID(strconv.Itoa(id)), nil
}

// halfMadeLeftCT is halfMadeLeft with the container command.
func halfMadeLeftCT(err error, id int, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	return errors.Join(err, fmt.Errorf("proxmox: the half-made container %d is left: %w; remove it with pct destroy %d", id, cleanupErr, id))
}

// removeHalfMadeCT is removeHalfMade for a container: only a container of
// ours with the name of this call is removed.
func (d *Driver) removeHalfMadeCT(ctx context.Context, id int, name string, made bool) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_, m, err := d.own(cleanup, strconv.Itoa(id))
	switch {
	case errors.Is(err, driver.ErrNotFound), errors.Is(err, ErrForeignGuest):
		// Not in our pool: nothing of ours. A create that failed before
		// Proxmox added the CT to the pool is destroyed by Proxmox itself
		// (the create worker removes it on error; review N2 of #162).
		return nil
	case err != nil:
		return err
	case m.Type != typeCT || m.Name != name || m.Template == 1:
		if made {
			return fmt.Errorf("VMID %d is %s %q (template %d), not the container %q: not proven ours, not removed", id, m.Type, m.Name, m.Template, name)
		}
		return nil
	}
	if err := d.ctUnlocked(cleanup, id); err != nil {
		return err
	}
	if err := d.deleteCT(cleanup, id); err != nil {
		return err
	}
	if _, _, err := d.own(cleanup, strconv.Itoa(id)); !errors.Is(err, driver.ErrNotFound) {
		if err == nil {
			return errors.New("the container is still there after the delete")
		}
		return err
	}
	return nil
}

func (d *Driver) ctUnlocked(ctx context.Context, id int) error {
	for {
		cfg, err := d.ctConfig(ctx, id, true)
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

// observeCT reads a container. TokenInConfig is the token in the view of
// the next start: a pending delete counts as removed (host C4, D-246).
func (d *Driver) observeCT(ctx context.Context, id int) (driver.GuestState, error) {
	cfg, next, err := d.ctViews(ctx, id)
	if err != nil {
		return driver.GuestState{}, err
	}
	if cfg["unprivileged"] != "1" {
		return driver.GuestState{}, fmt.Errorf("proxmox: CT %d is privileged; Plumb manages only unprivileged containers (D-227)", id)
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, ctPath(d, id)+"/status/current", nil, &status); err != nil {
		return driver.GuestState{}, notFound(err)
	}
	st := driver.GuestState{Spec: specOfCT(cfg), Digest: cfg["digest"], Power: driver.PowerStopped, MAC: net0MAC(cfg["net0"])}
	if p := specOfCT(next); !equalCTSpec(p, st.Spec) {
		st.Pending = &p
	}
	now, later := envToken(cfg["env"]), envToken(next["env"])
	st.TokenInConfig = later != ""
	st.TokenPending = later != "" && later != now
	if status.Status == "running" {
		st.Power = driver.PowerRunning
		st.IPs = d.ctAddresses(ctx, id)
	}
	if ip := st.Spec.IP; len(st.IPs) == 0 && ip != "" && ip != "dhcp" {
		st.IPs = []string{strings.SplitN(ip, "/", 2)[0]}
	}
	return st, nil
}

func equalCTSpec(a, b driver.GuestSpec) bool {
	return a.Name == b.Name && a.Cores == b.Cores && a.MemoryMB == b.MemoryMB && a.Bridge == b.Bridge &&
		a.IP == b.IP && a.Gateway == b.Gateway && a.DiskGB == b.DiskGB && a.ImageName == b.ImageName &&
		a.ImageVersion == b.ImageVersion && slices.Equal(a.SSHKeys, b.SSHKeys) && a.Features == b.Features &&
		slices.Equal(a.Mounts, b.Mounts) && slices.Equal(a.Devices, b.Devices) && slices.Equal(a.RawLXC, b.RawLXC) &&
		slices.Equal(a.Tags, b.Tags) && slices.Equal(a.NICs, b.NICs)
}

// ctAddresses reads the IPv4 addresses of eth0 that Proxmox sees from the
// host side; it needs no agent (host C7). A failed read is no error: the
// addresses are information.
func (d *Driver) ctAddresses(ctx context.Context, id int) []string {
	var ifs []struct {
		Name string `json:"name"`
		Addr []struct {
			Type string `json:"ip-address-type"`
			IP   string `json:"ip-address"`
		} `json:"ip-addresses"`
	}
	if err := d.c.Get(ctx, ctPath(d, id)+"/interfaces", nil, &ifs); err != nil {
		return nil
	}
	var out []string
	for _, i := range ifs {
		if i.Name != "eth0" {
			continue
		}
		for _, a := range i.Addr {
			if a.Type == "inet" && reIPv4.MatchString(a.IP) {
				out = append(out, a.IP)
			}
		}
	}
	return out
}

// ctUpdateKeys are the only keys that updateCT writes.
var ctUpdateKeys = map[string]bool{"digest": true, "cores": true, "memory": true, "net0": true, "env": true, "features": true,
	"tags": true, "delete": true, // tags: D-299; delete only of the tags and more NICs
	"net1": true, "net2": true, "net3": true} // T-219

// updateCT changes cores, memory, the bridge (live, host C5) and the
// tags, and for a re-enrollment sets the new token in env; the caller
// restarts. A disk change grows rootfs after the config write; Proxmox
// grows the file system of the container with it (D-299).
func (d *Driver) updateCT(ctx context.Context, id int, changes []wire.Change, boot *driver.Bootstrap) error {
	cfg, err := d.ctConfig(ctx, id, true)
	if err != nil {
		return err
	}
	params := url.Values{"digest": {cfg["digest"]}}
	grow := 0
	var nicDels []string
	for _, c := range changes {
		if driver.IsCreateOnly(c.Attr) {
			return &driver.CreateOnlyError{Attr: c.Attr}
		}
		if driver.NICIndex(c.Attr) > 0 { // T-219
			if err := d.nicChange(params, &nicDels, c, true); err != nil {
				return err
			}
			continue
		}
		switch c.Attr {
		case driver.AttrTags:
			if err := setTags(params, c.To); err != nil {
				return err
			}
		case driver.AttrDisk:
			n, err := growTo(c.To, sizeGB(parseProps(cfg["rootfs"])["size"]))
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
			if !ok || !reBridge.MatchString(b) {
				return fmt.Errorf("proxmox: bridge %v", c.To)
			}
			params.Set("net0", replaceProp(cfg["net0"], "bridge", b))
		case driver.AttrEnrollToken:
			if !c.Sensitive || c.From != nil || c.To != nil || boot == nil || boot.Token == "" {
				return errors.New("proxmox: enroll_token needs a Bootstrap and carries no value")
			}
			params.Set("env", tokenPrefix+boot.Token)
		case driver.AttrFeatures:
			// Only the features of a Plumb container (D-227); the token
			// may write nesting alone (host H10). On a running container
			// it waits for the next start (host D4).
			if v, _ := c.To.(string); v != driver.CTFeatures {
				return fmt.Errorf("proxmox: features %v: Plumb sets only %s", c.To, driver.CTFeatures)
			}
			params.Set("features", driver.CTFeatures)
		case driver.AttrMounts, driver.AttrDevices, driver.AttrRawLXC:
			return &driver.OutsideError{Attr: c.Attr}
		default:
			return fmt.Errorf("proxmox: unknown attribute %q", c.Attr)
		}
	}
	mergeDeletes(params, nicDels)
	if len(params) > 1 {
		for k := range params {
			if !ctUpdateKeys[k] {
				return fmt.Errorf("proxmox: Update must not write %q to a container", k)
			}
		}
		if err := checkUpdateNICs(params); err != nil { // the tags and more NICs only (T-219)
			return err
		}
		if net0, ok := params["net0"]; ok && (len(net0) != 1 || !sameExcept(cfg["net0"], net0[0], "bridge")) {
			return errors.New("proxmox: a net0 change must keep every property but the bridge")
		}
		token := ""
		if boot != nil {
			token = boot.Token
		}
		if err := scrub(d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/config", params), token); err != nil {
			return tagRefusal(err, params)
		}
		if err := checkNICsApplied(ctx, params, func(ctx context.Context, current bool) (map[string]string, error) {
			return d.ctConfig(ctx, id, current)
		}); err != nil {
			return err
		}
	}
	if grow > 0 {
		return d.resize(ctx, ctPath(d, id), "rootfs", grow)
	}
	return nil
}

// ClearBootstrap removes the enrollment token from the env of a container
// (D-231). On a running container Proxmox keeps the delete pending until
// the next start (host C4); the next view shows no token at once. Without
// a token it changes nothing. A VM has nothing to clear.
func (d *Driver) ClearBootstrap(ctx context.Context, gid driver.GuestID) error {
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	if m.Type != typeCT {
		return fmt.Errorf("proxmox: VMID %d is a VM; a VM has no token to clear", id)
	}
	next, err := d.ctConfig(ctx, id, false)
	if err != nil {
		return err
	}
	if envToken(next["env"]) == "" {
		return nil
	}
	return d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/config", url.Values{"delete": {"env"}, "digest": {next["digest"]}})
}

// deleteCT stops a container and removes it with its volumes. The
// parameters go into the query: a DELETE body gives 501 (host C6).
func (d *Driver) deleteCT(ctx context.Context, id int) error {
	var st struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, ctPath(d, id)+"/status/current", nil, &st); err != nil {
		return notFound(err)
	}
	if st.Status == "running" {
		if err := d.c.Task(ctx, d.cfg.Node, "POST", ctPath(d, id)+"/status/stop", nil); err != nil {
			return err
		}
	}
	return d.c.Task(ctx, d.cfg.Node, "DELETE", ctPath(d, id), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}})
}
