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
	s.DiskGB = sizeGB(parseProps(cfg["rootfs"])["size"])
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

// ctDescription is the description of a new container: the image marker
// (D-189), the version of a built Image (D-200) and the SSH keys.
func ctDescription(spec driver.GuestSpec) string {
	var b strings.Builder
	b.WriteString(imageMarker + spec.ImageName + "\n")
	if spec.ImageVersion > 0 {
		fmt.Fprintf(&b, "%s%d\n", versionMarker, spec.ImageVersion)
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

func checkCTSpec(s driver.GuestSpec) error {
	if err := checkSpec(s); err != nil {
		return err
	}
	if s.User != "" {
		return errors.New("proxmox: a container has no bootstrap user (D-226)")
	}
	if !dnsNameRE.MatchString(s.Name) || strings.Contains(s.Name, ".") || len(s.Name) > 63 {
		return fmt.Errorf("proxmox: %q is no host name for a container", s.Name)
	}
	for _, k := range s.SSHKeys {
		if k == "" || strings.ContainsAny(k, "\r\n") {
			return errors.New("proxmox: an SSH key must be one line")
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
	if err := checkCTSpec(spec); err != nil {
		return "", err
	}
	if err := d.checkBridge(spec.Bridge); err != nil {
		return "", err
	}
	if err := d.checkTemplateRef(spec.Image); err != nil {
		return "", err
	}
	desc := ctDescription(spec)
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
		"onboot":       {"1"},
		"description":  {desc},
		// The form encoding sends "=" as %3D, as Proxmox needs (host C2).
		"env": {tokenPrefix + boot.Token},
	}
	if len(spec.SSHKeys) > 0 {
		params.Set("ssh-public-keys", strings.Join(spec.SSHKeys, "\n"))
	}
	if err := d.c.Task(ctx, d.cfg.Node, "POST", d.node()+"/lxc", params); err != nil {
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
	st := driver.GuestState{Spec: specOfCT(cfg), Digest: cfg["digest"], Power: driver.PowerStopped}
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
		a.ImageVersion == b.ImageVersion && strings.Join(a.SSHKeys, "\n") == strings.Join(b.SSHKeys, "\n")
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
var ctUpdateKeys = map[string]bool{"digest": true, "cores": true, "memory": true, "net0": true, "env": true}

// updateCT changes cores, memory and the bridge (live, host C5) and for a
// re-enrollment sets the new token in env; the caller restarts.
func (d *Driver) updateCT(ctx context.Context, id int, changes []wire.Change, boot *driver.Bootstrap) error {
	cfg, err := d.ctConfig(ctx, id, true)
	if err != nil {
		return err
	}
	params := url.Values{"digest": {cfg["digest"]}}
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
			if !ok || !reBridge.MatchString(b) {
				return fmt.Errorf("proxmox: bridge %v", c.To)
			}
			params.Set("net0", replaceProp(cfg["net0"], "bridge", b))
		case driver.AttrEnrollToken:
			if !c.Sensitive || c.From != nil || c.To != nil || boot == nil || boot.Token == "" {
				return errors.New("proxmox: enroll_token needs a Bootstrap and carries no value")
			}
			params.Set("env", tokenPrefix+boot.Token)
		default:
			return fmt.Errorf("proxmox: unknown attribute %q", c.Attr)
		}
	}
	if len(params) == 1 {
		return nil
	}
	for k := range params {
		if !ctUpdateKeys[k] {
			return fmt.Errorf("proxmox: Update must not write %q to a container", k)
		}
	}
	if net0, ok := params["net0"]; ok && (len(net0) != 1 || !sameExcept(cfg["net0"], net0[0], "bridge")) {
		return errors.New("proxmox: a net0 change must keep every property but the bridge")
	}
	token := ""
	if boot != nil {
		token = boot.Token
	}
	return scrub(d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/config", params), token)
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
