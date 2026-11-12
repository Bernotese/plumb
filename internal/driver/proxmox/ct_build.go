package proxmox

// Image builds of lxc Images (T-082, D-234, D-251 … D-253): the build
// container is made from the repacked template file, sealed by its agent,
// cleared of its env token and turned into a container template; a Target
// of a version is a full clone with its own env token.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
)

// guestConfig reads the current config of a pool guest of either type. A
// container's hostname is its name, so the shared build and image code
// can read cfg["name"] for both.
func (d *Driver) guestConfig(ctx context.Context, m member) (map[string]string, error) {
	if m.Type != typeCT {
		return d.config(ctx, m.VMID)
	}
	cfg, err := d.ctConfig(ctx, m.VMID, true)
	if err != nil {
		return nil, notFound(err)
	}
	cfg["name"] = cfg["hostname"]
	return cfg, nil
}

// guestPath is the API path of a guest of the member's type.
func guestPath(d *Driver, m member) string {
	if m.Type == typeCT {
		return ctPath(d, m.VMID)
	}
	return vmPath(d, m.VMID)
}

// ctEnvLeft reports where a container still has an env value: the running
// view, the view after the next start, or a pending row (review S8). It
// never returns the value.
func (d *Driver) ctEnvLeft(ctx context.Context, id int) (string, error) {
	run, next, err := d.ctViews(ctx, id)
	if err != nil {
		return "", err
	}
	var pending []struct {
		Key string `json:"key"`
	}
	if err := d.c.Get(ctx, ctPath(d, id)+"/pending", nil, &pending); err != nil {
		return "", notFound(err)
	}
	var where []string
	if run["env"] != "" {
		where = append(where, "config")
	}
	if next["env"] != "" {
		where = append(where, "config after the next start")
	}
	for _, p := range pending {
		if p.Key == "env" && run["env"] == "" {
			where = append(where, "pending")
		}
	}
	return strings.Join(where, ", "), nil
}

// createBuildCT makes the build container of one version from the
// repacked template file: build name and marker, onboot=0, no SSH keys,
// the env token. It starts it.
func (d *Driver) createBuildCT(ctx context.Context, spec driver.BuildSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	name := buildName(spec.Image, spec.Version)
	gs := driver.GuestSpec{Name: name, ImageName: spec.Image, Image: spec.Base, Cores: spec.Cores, MemoryMB: spec.MemoryMB,
		DiskGB: spec.DiskGB, Bridge: spec.Bridge, IP: spec.IP, Gateway: spec.Gateway}
	if err := checkCTSpecName(gs, true); err != nil {
		return "", err
	}
	gs2, err := d.guests(ctx)
	if err != nil {
		return "", err
	}
	for _, m := range gs2 {
		if m.Type == typeCT {
			if cfg, err := d.ctConfig(ctx, m.VMID, true); err == nil && cfg["hostname"] == name {
				return "", fmt.Errorf("proxmox: CT %d already has the name %s; remove the old build container first", m.VMID, name)
			}
		}
	}
	return d.createFromFile(ctx, gs, boot, buildLine(spec.Image, spec.Version)+"\nPlumb image build. Do not change it here.", "0")
}

// makeImageCT turns the sealed, stopped build container into the version
// template (D-234): the guard of MakeImage, the env token removed and
// proven gone from both views and the pending list (review S8), one
// config write with the digest, the conversion.
func (d *Driver) makeImageCT(ctx context.Context, id int, m member, img driver.BuiltImage) (driver.ImageRef, error) {
	ref := driver.ImageRef(strconv.Itoa(id))
	bname := buildName(img.Image, img.Version)
	cfg, err := d.ctConfig(ctx, id, true)
	if err != nil {
		return "", err
	}
	if m.Template == 1 || cfg["template"] == "1" {
		switch {
		case cfg["hostname"] == versionName(img.Image, img.Version) && isVersionOf(cfg["description"], img.Image, img.Version, img.Digest):
			return ref, nil
		case cfg["hostname"] == bname && hasLine(cfg["description"], buildLine(img.Image, img.Version)) && cfg["lock"] == "":
			if err := d.nameVersionCT(ctx, id, img); err != nil {
				return "", err
			}
			return ref, nil
		}
		return "", fmt.Errorf("proxmox: CT %d is already a template", id)
	}
	switch {
	case cfg["hostname"] != bname:
		return "", fmt.Errorf("proxmox: CT %d has the name %q, not the build name %s", id, cfg["hostname"], bname)
	case !hasLine(cfg["description"], buildLine(img.Image, img.Version)):
		return "", fmt.Errorf("proxmox: CT %d has no build marker %q", id, buildLine(img.Image, img.Version))
	case cfg["lock"] != "":
		return "", fmt.Errorf("proxmox: CT %d is locked (%s)", id, cfg["lock"])
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := d.c.Get(ctx, ctPath(d, id)+"/status/current", nil, &st); err != nil {
		return "", notFound(err)
	}
	if st.Status != "stopped" {
		return "", fmt.Errorf("proxmox: build container %d is %s, not stopped", id, st.Status)
	}
	if err := d.ClearBootstrap(ctx, driver.GuestID(ref)); err != nil {
		return "", fmt.Errorf("proxmox: remove the token of build container %d: %w", id, err)
	}
	if err := d.nameVersionCT(ctx, id, img); err != nil {
		return "", err
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", ctPath(d, id)+"/template", nil); err != nil {
		return d.afterFailedConversionCT(ctx, id, img, fmt.Errorf("proxmox: convert to template: %w", err))
	}
	if err := d.waitTemplate(ctx, id); err != nil {
		return d.afterFailedConversionCT(ctx, id, img, err)
	}
	return ref, nil
}

// waitTemplate waits until a container config says template: 1. Proxmox
// answers the template call with null but converts in a worker that it
// does not name (host, T-082 H-B1: the read right after still shows the
// vm- disk and no template key).
func (d *Driver) waitTemplate(ctx context.Context, id int) error {
	wait, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	for {
		cfg, err := d.ctConfig(wait, id, true)
		if err != nil {
			return err
		}
		if cfg["template"] == "1" {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("proxmox: CT %d is no template %s after the template call: %w", id, cleanupTimeout, wait.Err())
		case <-time.After(d.c.poll):
		}
	}
}

// nameVersionCT proves that no env is left (S8) and writes the version
// name and description in one write with the digest; DNS and onboot of
// the build go.
func (d *Driver) nameVersionCT(ctx context.Context, id int, img driver.BuiltImage) error {
	if where, err := d.ctEnvLeft(ctx, id); err != nil {
		return err
	} else if where != "" {
		return fmt.Errorf("proxmox: CT %d still has an env value (%s); no template is made from it (D-234)", id, where)
	}
	cfg, err := d.ctConfig(ctx, id, true)
	if err != nil {
		return err
	}
	params := url.Values{"digest": {cfg["digest"]}, "hostname": {versionName(img.Image, img.Version)}, "description": {versionDescription(img)}}
	var del []string
	for _, k := range []string{"nameserver", "searchdomain", "onboot"} {
		if _, ok := cfg[k]; ok {
			del = append(del, k)
		}
	}
	if len(del) > 0 {
		params.Set("delete", strings.Join(del, ","))
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/config", params); err != nil {
		return fmt.Errorf("proxmox: name CT %d %s (its config may have changed after the check): %w", id, versionName(img.Image, img.Version), err)
	}
	return nil
}

// afterFailedConversionCT is afterFailedConversion for a container: a
// template now is a success; else the build name and marker go back, so
// the cleanup of build guests finds it.
func (d *Driver) afterFailedConversionCT(ctx context.Context, id int, img driver.BuiltImage, convErr error) (driver.ImageRef, error) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	left := func(err error) (driver.ImageRef, error) {
		return "", errors.Join(convErr, fmt.Errorf("proxmox: CT %d may keep the template name %s; check it and remove it by hand (pct destroy %d): %w",
			id, versionName(img.Image, img.Version), id, err))
	}
	if err := d.ctUnlocked(cleanup, id); err != nil {
		return left(err)
	}
	cfg, err := d.ctConfig(cleanup, id, true)
	if err != nil {
		return left(err)
	}
	if cfg["template"] == "1" {
		return driver.ImageRef(strconv.Itoa(id)), nil
	}
	if err := d.c.MaybeTask(cleanup, d.cfg.Node, "PUT", ctPath(d, id)+"/config", url.Values{"digest": {cfg["digest"]},
		"hostname": {buildName(img.Image, img.Version)}, "description": {buildLine(img.Image, img.Version) + "\nPlumb image build. Do not change it here."}}); err != nil {
		return left(err)
	}
	return "", convErr
}

// cloneCT makes a Target from a version template (D-200): a full clone,
// proof that it inherited no env (review S8), one config write with the
// Target's settings and its own env token, the disk grown, the start. A
// failure after the clone removes it. A clone gets no SSH keys (D-251).
func (d *Driver) cloneCT(ctx context.Context, spec driver.GuestSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	if err := checkCTSpec(spec); err != nil {
		return "", err
	}
	if len(spec.SSHKeys) > 0 {
		return "", errors.New("proxmox: a container from a built Image gets no SSH keys from Proxmox; use a Block such as deploy-user (D-251)")
	}
	if err := d.checkBridge(spec.Bridge); err != nil {
		return "", err
	}
	tpl, m, err := d.own(ctx, string(spec.Image))
	if err != nil {
		return "", fmt.Errorf("proxmox: image %s: %w", spec.Image, err)
	}
	if m.Type != typeCT || m.Template != 1 {
		return "", fmt.Errorf("proxmox: VMID %d is not a container template", tpl)
	}
	tcfg, err := d.ctConfig(ctx, tpl, true)
	if err != nil {
		return "", err
	}
	if have := sizeGB(parseProps(tcfg["rootfs"])["size"]); have > spec.DiskGB {
		return "", fmt.Errorf("proxmox: disk %d GB is smaller than the %d GB root fs of image %s (template %d); a disk cannot shrink: set disk to %d or more",
			spec.DiskGB, have, spec.ImageName, tpl, have)
	}
	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	defer d.releaseID(id)
	if err := d.c.Task(ctx, d.cfg.Node, "POST", ctPath(d, tpl)+"/clone", url.Values{
		"newid": {strconv.Itoa(id)}, "hostname": {spec.Name}, "pool": {d.cfg.Pool}, "full": {"1"}, "storage": {d.cfg.DiskStorage},
	}); err != nil {
		return "", halfMadeLeftCT(fmt.Errorf("proxmox: clone: %w", err), id, d.removeHalfMadeCT(ctx, id, spec.Name, false))
	}
	fail := func(err error) (driver.GuestID, error) {
		return "", scrub(halfMadeLeftCT(err, id, d.removeHalfMadeCT(ctx, id, spec.Name, true)), boot.Token)
	}
	if where, err := d.ctEnvLeft(ctx, id); err != nil {
		return fail(err)
	} else if where != "" {
		return fail(fmt.Errorf("proxmox: the clone %d inherited an env value (%s) from template %d; not used (D-234)", id, where, tpl))
	}
	cfg, err := d.ctConfig(ctx, id, true)
	if err != nil {
		return fail(err)
	}
	params := url.Values{
		"digest":       {cfg["digest"]},
		"cores":        {strconv.Itoa(spec.Cores)},
		"memory":       {strconv.Itoa(spec.MemoryMB)},
		"swap":         {strconv.Itoa(ctSwapMB)},
		"net0":         {ctNet0(spec)},
		"nameserver":   {strings.Join(d.cfg.Nameservers, " ")},
		"searchdomain": {d.cfg.SearchDomain},
		"onboot":       {"1"},
		"features":     {"nesting=1"},
		"description":  {ctDescription(driver.GuestSpec{ImageName: spec.ImageName, ImageVersion: spec.ImageVersion})},
		"env":          {tokenPrefix + boot.Token},
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/config", params); err != nil {
		return fail(fmt.Errorf("proxmox: configure: %w", err))
	}
	if have := sizeGB(parseProps(cfg["rootfs"])["size"]); spec.DiskGB > have {
		if err := d.c.MaybeTask(ctx, d.cfg.Node, "PUT", ctPath(d, id)+"/resize", url.Values{
			"disk": {"rootfs"}, "size": {strconv.Itoa(spec.DiskGB) + "G"},
		}); err != nil {
			return fail(fmt.Errorf("proxmox: resize root fs: %w", err))
		}
	}
	if err := d.c.Task(ctx, d.cfg.Node, "POST", ctPath(d, id)+"/status/start", nil); err != nil {
		return fail(fmt.Errorf("proxmox: start: %w", err))
	}
	return driver.GuestID(strconv.Itoa(id)), nil
}
