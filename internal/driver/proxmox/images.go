package proxmox

// Image builds (T-061, D-203, D-208, D-209): the build guest, the
// conversion into a versioned template, finding and listing templates, the
// guarded delete and the use of the disk storage.

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
)

// imageNameRE is the name pattern of an Image document (schema image.json).
// Every name that goes into a VM name, a description line or a guard regex
// passes it first.
var imageNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

// cleanupTimeout bounds a repair after a failed step, on its own deadline.
const cleanupTimeout = 2 * time.Minute

// BuildUser is the bootstrap user of a build guest (D-203). It has no SSH key.
const BuildUser = "plumb-build"

// Description lines of build guests and version templates (D-198, D-203).
const (
	buildMarker   = "plumb-build: "         // "plumb-build: <image> v<N>" on a build guest
	versionMarker = "plumb-image-version: " // "plumb-image-version: <N>" on a guest of a built version
	baseOwner     = "Plumb image "          // first line of a base template (FetchImage)
)

func checkImageName(name string) error {
	if !imageNameRE.MatchString(name) {
		return fmt.Errorf("proxmox: bad image name %q", name)
	}
	return nil
}

func buildName(image string, version int) string {
	return fmt.Sprintf("plumb-build-%s-v%d", image, version)
}

func versionName(image string, version int) string {
	return fmt.Sprintf("plumb-img-%s-v%d", image, version)
}

func buildLine(image string, version int) string {
	return fmt.Sprintf("%s%s v%d", buildMarker, image, version)
}

// imageNameRegexp matches every template name of one Image: a version
// (v<N>) or a base template (<sha12>). The name is quoted, so a "." in it
// matches only a "." (D-209).
func imageNameRegexp(image string) *regexp.Regexp {
	return regexp.MustCompile(`^plumb-img-` + regexp.QuoteMeta(image) + `-(v[0-9]+|[0-9a-f]{12})$`)
}

// hasLine reports whether a description holds the exact line.
func hasLine(description, line string) bool {
	for _, l := range strings.Split(description, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// lineValue returns the value of the first line "<prefix><value>".
func lineValue(description, prefix string) string {
	for _, l := range strings.Split(description, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), prefix); ok {
			return v
		}
	}
	return ""
}

// versionDescription is the description of a version template.
func versionDescription(img driver.BuiltImage) string {
	return fmt.Sprintf("%s%s\nversion=%d\nbuild=%s\nPlumb image template. Do not change it here.", imageMarker, img.Image, img.Version, img.Digest)
}

// isVersionOf checks the description lines of a version template.
func isVersionOf(description, image string, version int, digest string) bool {
	return hasLine(description, imageMarker+image) && hasLine(description, "version="+strconv.Itoa(version)) &&
		hasLine(description, "build="+digest)
}

// isBaseOf checks the description lines of a base template (FetchImage).
func isBaseOf(description, image, sum string) bool {
	return hasLine(description, baseOwner+image) && hasLine(description, "sha256="+sum)
}

// --- CreateBuild ---

// CreateBuild clones the base template into the build guest of one
// version (D-203): full clone, name plumb-build-<image>-v<N>, the marker
// line, onboot=0, the user plumb-build without a key, ciupgrade=1 when the
// build upgrades. A failure after the clone removes the half-made guest.
func (d *Driver) CreateBuild(ctx context.Context, spec driver.BuildSpec, boot driver.Bootstrap) (driver.GuestID, error) {
	if err := checkImageName(spec.Image); err != nil {
		return "", err
	}
	if spec.Version < 1 {
		return "", fmt.Errorf("proxmox: bad image version %d", spec.Version)
	}
	if boot.Token == "" {
		return "", errors.New("proxmox: a build guest needs an enrollment token")
	}
	name := buildName(spec.Image, spec.Version)
	gs := driver.GuestSpec{Name: name, ImageName: spec.Image, Image: spec.Base, Cores: spec.Cores, MemoryMB: spec.MemoryMB,
		DiskGB: spec.DiskGB, Bridge: spec.Bridge, IP: spec.IP, Gateway: spec.Gateway, User: BuildUser}
	if err := checkSpec(gs); err != nil {
		return "", err
	}
	if err := d.checkBridge(gs.Bridge); err != nil {
		return "", err
	}
	tpl, m, err := d.own(ctx, string(spec.Base))
	if err != nil {
		return "", fmt.Errorf("proxmox: base image %s: %w", spec.Base, err)
	}
	if m.Template != 1 {
		return "", fmt.Errorf("proxmox: VMID %d is not a template", tpl)
	}
	if err := d.checkDisk(ctx, tpl, gs); err != nil {
		return "", err
	}
	// One build per Image at a time (D-203): a guest with this name is the
	// rest of an earlier build, and the engine removes it first.
	ms, err := d.members(ctx)
	if err != nil {
		return "", err
	}
	for _, o := range ms {
		if o.Name == name {
			return "", fmt.Errorf("proxmox: VMID %d already has the name %s; remove the old build guest first", o.VMID, name)
		}
	}
	id, err := d.nextID(ctx)
	if err != nil {
		return "", err
	}
	defer d.releaseID(id)
	if err := d.c.Task(ctx, d.cfg.Node, "POST", vmPath(d, tpl)+"/clone", url.Values{
		"newid": {strconv.Itoa(id)}, "name": {name}, "pool": {d.cfg.Pool},
		"full": {"1"}, "storage": {d.cfg.DiskStorage},
	}); err != nil {
		return "", halfMadeLeft(fmt.Errorf("proxmox: clone: %w", err), id, d.removeHalfMade(ctx, id, name, false))
	}
	upgrade := "0"
	if spec.Upgrade {
		upgrade = "1" // cloud-init upgrades all packages at the first boot (D-204)
	}
	err = d.configureWith(ctx, id, gs, boot.Token, func(p url.Values) {
		p.Set("description", buildLine(spec.Image, spec.Version)+"\nPlumb image build. Do not change it here.")
		p.Set("onboot", "0")
		p.Set("ciupgrade", upgrade)
	})
	if err != nil {
		return "", scrub(halfMadeLeft(err, id, d.removeHalfMade(ctx, id, name, true)), boot.Token)
	}
	return driver.GuestID(strconv.Itoa(id)), nil
}

// --- MakeImage ---

// makeImageDelete are the keys that MakeImage removes (host A2): the
// bootstrap values and DNS of the build guest. ciupgrade is set to 0, not
// deleted: a deleted ciupgrade means "upgrade" in Proxmox.
var makeImageDelete = []string{"sshkeys", "ciuser", "ipconfig0", "cicustom", "nameserver", "searchdomain", "onboot"}

// MakeImage turns the sealed, stopped build guest into the version
// template (D-208). Guard: in the range and the pool, the exact build name
// and marker line of img, no template, no lock, stopped. Then one config
// write with the digest of the guard read, and the conversion. A config
// that changed after the guard is refused by Proxmox (host A2).
func (d *Driver) MakeImage(ctx context.Context, gid driver.GuestID, img driver.BuiltImage) (driver.ImageRef, error) {
	if err := checkImageName(img.Image); err != nil {
		return "", err
	}
	if img.Version < 1 || !sha256Hex.MatchString(img.Digest) {
		return "", errors.New("proxmox: a built image needs a version and a full build digest")
	}
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return "", err
	}
	bname := buildName(img.Image, img.Version)
	cfg, err := d.config(ctx, id)
	if err != nil {
		return "", err
	}
	switch {
	case m.Template == 1 || cfg["template"] == "1":
		return "", fmt.Errorf("proxmox: VMID %d is already a template", id)
	case cfg["name"] != bname:
		return "", fmt.Errorf("proxmox: VMID %d has the name %q, not the build name %s", id, cfg["name"], bname)
	case !hasLine(cfg["description"], buildLine(img.Image, img.Version)):
		return "", fmt.Errorf("proxmox: VMID %d has no build marker %q", id, buildLine(img.Image, img.Version))
	case cfg["lock"] != "":
		return "", fmt.Errorf("proxmox: VMID %d is locked (%s)", id, cfg["lock"])
	}
	st, err := d.status(ctx, id)
	if err != nil {
		return "", err
	}
	if st != "stopped" {
		return "", fmt.Errorf("proxmox: build guest %d is %s, not stopped", id, st)
	}
	var del []string
	for _, k := range makeImageDelete {
		if _, ok := cfg[k]; ok {
			del = append(del, k)
		}
	}
	params := url.Values{
		"digest":      {cfg["digest"]},
		"name":        {versionName(img.Image, img.Version)},
		"description": {versionDescription(img)},
		"ciupgrade":   {"0"},
		"smbios1":     {withoutSerial(cfg["smbios1"])},
	}
	if len(del) > 0 {
		params.Set("delete", strings.Join(del, ","))
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/config", params); err != nil {
		return "", fmt.Errorf("proxmox: clear the build guest %d (its config may have changed after the check): %w", id, err)
	}
	if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, id)+"/template", nil); err != nil {
		// Give the guest its build name and marker back, so the engine's
		// cleanup of build guests (D-203) still finds and proves it.
		return "", errors.Join(fmt.Errorf("proxmox: convert to template: %w", err), d.restoreBuild(ctx, id, img))
	}
	return driver.ImageRef(strconv.Itoa(id)), nil
}

// restoreBuild writes the build name and marker back after a failed
// conversion. It writes nothing to a template.
func (d *Driver) restoreBuild(ctx context.Context, id int, img driver.BuiltImage) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	cfg, err := d.config(cleanup, id)
	if err != nil {
		return fmt.Errorf("proxmox: VMID %d keeps the template name; remove it by hand: %w", id, err)
	}
	if cfg["template"] == "1" {
		return nil // the conversion worked after all
	}
	if err := d.c.MaybeTask(cleanup, d.cfg.Node, "POST", vmPath(d, id)+"/config", url.Values{
		"digest":      {cfg["digest"]},
		"name":        {buildName(img.Image, img.Version)},
		"description": {buildLine(img.Image, img.Version) + "\nPlumb image build. Do not change it here."},
	}); err != nil {
		return fmt.Errorf("proxmox: VMID %d keeps the template name; remove it by hand: %w", id, err)
	}
	return nil
}

// withoutSerial removes the serial (the enrollment token) from smbios1 and
// keeps every other field, the uuid above all. base64 goes with the serial
// when no other encoded field is left.
func withoutSerial(old string) string {
	fields := parseProps(old)
	delete(fields, "serial")
	if fields["base64"] == "1" {
		other := false
		for k := range fields {
			if k != "uuid" && k != "base64" {
				other = true
			}
		}
		if !other {
			delete(fields, "base64")
		}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fields[k])
	}
	return strings.Join(parts, ",")
}

// --- FindImage ---

// FindImage finds the template of one version by its name and the full
// build digest line, in the pool and the VMID range (D-198). The list name
// can lag behind a rename (see member), so a member with no name or the
// build name is read too.
func (d *Driver) FindImage(ctx context.Context, image string, version int, digest string) (driver.ImageRef, error) {
	if err := checkImageName(image); err != nil {
		return "", err
	}
	if version < 1 || !sha256Hex.MatchString(digest) {
		return "", errors.New("proxmox: FindImage needs a version and a full build digest")
	}
	name := versionName(image, version)
	ms, err := d.members(ctx)
	if err != nil {
		return "", err
	}
	var found []int
	for _, m := range ms {
		if m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
			continue
		}
		if m.Name != name && m.Name != "" && m.Name != buildName(image, version) {
			continue
		}
		cfg, err := d.config(ctx, m.VMID)
		if errors.Is(err, driver.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if cfg["name"] == name && cfg["template"] == "1" && isVersionOf(cfg["description"], image, version, digest) {
			found = append(found, m.VMID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("proxmox: template %s with build=%s: %w", name, digest, driver.ErrNotFound)
	case 1:
		return driver.ImageRef(strconv.Itoa(found[0])), nil
	}
	return "", fmt.Errorf("proxmox: VMIDs %v are all template %s with the same build digest; remove the extra ones by hand", found, name)
}

// --- ListImages ---

// poolScan is one read of the pool configs and the disk storage volumes,
// for the linked-clone check (review S3).
type poolScan struct {
	configs map[int]map[string]string
	volumes []storageVolume
}

type storageVolume struct {
	VolID  string `json:"volid"`
	VMID   any    `json:"vmid"` // a number or a string, depending on the plugin
	Parent string `json:"parent"`
	Size   int64  `json:"size"`
}

func (v storageVolume) vmid() int {
	switch x := v.VMID.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

// scanPool reads the config of every pool guest in the range and the
// volume list of the disk storage. A guest that is gone between the two
// reads is skipped.
func (d *Driver) scanPool(ctx context.Context, ms []member) (*poolScan, error) {
	s := &poolScan{configs: map[int]map[string]string{}}
	for _, m := range ms {
		if m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
			continue
		}
		cfg, err := d.config(ctx, m.VMID)
		if errors.Is(err, driver.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		s.configs[m.VMID] = cfg
	}
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.DiskStorage)+"/content",
		url.Values{"content": {"images"}}, &s.volumes); err != nil {
		return nil, fmt.Errorf("proxmox: list the volumes of %s for the linked-clone check: %w", d.cfg.DiskStorage, err)
	}
	return s, nil
}

var diskKeyRE = regexp.MustCompile(`^(scsi|virtio|sata|ide)[0-9]+$|^efidisk0$|^tpmstate0$|^unused[0-9]+$`)

// diskVolumes returns the volume ids of the disk lines of a config, without
// cloud-init drives and CD-ROMs.
func diskVolumes(cfg map[string]string) []string {
	var out []string
	for k, v := range cfg {
		if !diskKeyRE.MatchString(k) {
			continue
		}
		p := parseProps(v)
		vol := p[""]
		if vol == "" || vol == "none" || p["media"] == "cdrom" || strings.Contains(vol, "cloudinit") {
			continue
		}
		out = append(out, vol)
	}
	slices.Sort(out)
	return out
}

// linkedClones returns the pool guests that are linked clones of the
// template: a disk line "<storage>:<base volume>/<own volume>" (the form
// that Proxmox gives a linked clone on lvmthin), or a volume of the disk
// storage whose parent is a base volume of the template.
func (s *poolScan) linkedClones(tpl int) []int {
	bases := map[string]bool{} // "storage:base-…" and "base-…"
	for _, vol := range diskVolumes(s.configs[tpl]) {
		bases[vol] = true
		if _, name, ok := strings.Cut(vol, ":"); ok {
			bases[name] = true
		}
	}
	if len(bases) == 0 {
		return nil
	}
	isBase := func(v string) bool {
		v, _, _ = strings.Cut(v, "@") // zfs and rbd parents: "base-…@__base__"
		return bases[v]
	}
	set := map[int]bool{}
	for id, cfg := range s.configs {
		if id == tpl {
			continue
		}
		for _, vol := range diskVolumes(cfg) {
			if base, _, ok := strings.Cut(vol, "/"); ok && isBase(base) {
				set[id] = true
			}
		}
	}
	for _, v := range s.volumes {
		id := v.vmid()
		if id == tpl || v.Parent == "" {
			continue
		}
		if isBase(v.Parent) {
			set[id] = true
		}
		if storage, _, ok := strings.Cut(v.VolID, ":"); ok && isBase(storage+":"+v.Parent) {
			set[id] = true
		}
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// ListImages lists the templates of an Image in the pool and the range
// (versions and base templates) and its import files in the image storage.
// A template counts only when its description names the Image with a full
// digest or checksum; a VM with the name and without that proof is not
// listed (and RemoveImage would refuse it). ListImages never says which
// import file is unused: the engine knows the checksum of the document.
func (d *Driver) ListImages(ctx context.Context, image string) ([]driver.ImageObject, error) {
	if err := checkImageName(image); err != nil {
		return nil, err
	}
	ms, err := d.members(ctx)
	if err != nil {
		return nil, err
	}
	scan, err := d.scanPool(ctx, ms)
	if err != nil {
		return nil, err
	}
	re := imageNameRegexp(image)
	var out []driver.ImageObject
	baseSums := map[string]string{} // sha12 → full checksum, for the import files
	ids := make([]int, 0, len(scan.configs))
	for id := range scan.configs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		cfg := scan.configs[id]
		name := cfg["name"]
		sm := re.FindStringSubmatch(name)
		if sm == nil || cfg["template"] != "1" {
			continue
		}
		obj := driver.ImageObject{Image: image, Ref: driver.ImageRef(strconv.Itoa(id)), Name: name,
			SizeBytes: int64(sizeMB(parseProps(cfg["scsi0"])["size"])) << 20, LinkedClones: len(scan.linkedClones(id))}
		desc := cfg["description"]
		if v, ok := strings.CutPrefix(sm[1], "v"); ok {
			n, err := strconv.Atoi(v)
			dg := lineValue(desc, "build=")
			if err != nil || n < 1 || !sha256Hex.MatchString(dg) || !isVersionOf(desc, image, n, dg) {
				continue
			}
			obj.Kind, obj.Version, obj.Digest = driver.ImageVersionTemplate, n, dg
		} else {
			sum := lineValue(desc, "sha256=")
			if !sha256Hex.MatchString(sum) || sum[:12] != sm[1] || !isBaseOf(desc, image, sum) {
				continue
			}
			obj.Kind, obj.Digest = driver.ImageBaseTemplate, sum
			baseSums[sm[1]] = sum
		}
		out = append(out, obj)
	}

	var files []storageVolume
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.ImageStorage)+"/content",
		url.Values{"content": {"import"}}, &files); err != nil {
		return nil, err
	}
	fileRE := regexp.MustCompile(`^` + regexp.QuoteMeta(d.cfg.ImageStorage) + `:import/plumb-` + regexp.QuoteMeta(image) + `-([0-9a-f]{12})\.qcow2$`)
	slices.SortFunc(files, func(a, b storageVolume) int { return strings.Compare(a.VolID, b.VolID) })
	for _, f := range files {
		sm := fileRE.FindStringSubmatch(f.VolID)
		if sm == nil {
			continue
		}
		out = append(out, driver.ImageObject{Kind: driver.ImageImportFile, Image: image, Name: f.VolID,
			Digest: baseSums[sm[1]], SizeBytes: f.Size})
	}
	return out, nil
}

// --- RemoveImage ---

// RemoveImage deletes one template of an Image after the guard of D-209.
// It reads the pool list first: Proxmox answers 403, not 404, for a VMID
// that is gone (host B2c), so a VMID that is not in the pool is
// driver.ErrNotFound and gets no other request. Then: the range,
// template: 1, no lock, the exact name, the description that names the
// Image with the full digest or checksum, and no linked clone in the pool
// (review S3). It never removes an import file.
func (d *Driver) RemoveImage(ctx context.Context, obj driver.ImageObject) error {
	if obj.Kind == driver.ImageImportFile {
		return fmt.Errorf("proxmox: %s is an import file; Plumb never deletes one (D-209): run pvesm free on the host", obj.Name)
	}
	if obj.Kind != driver.ImageVersionTemplate && obj.Kind != driver.ImageBaseTemplate {
		return fmt.Errorf("proxmox: unknown image object kind %q", obj.Kind)
	}
	if err := checkImageName(obj.Image); err != nil {
		return err
	}
	if !sha256Hex.MatchString(obj.Digest) {
		return fmt.Errorf("proxmox: %s: RemoveImage needs the full digest or checksum", obj.Name)
	}
	var want string
	if obj.Kind == driver.ImageVersionTemplate {
		if obj.Version < 1 {
			return fmt.Errorf("proxmox: %s: bad version %d", obj.Name, obj.Version)
		}
		want = versionName(obj.Image, obj.Version)
	} else {
		want = "plumb-img-" + obj.Image + "-" + obj.Digest[:12]
	}
	if obj.Name != want || !imageNameRegexp(obj.Image).MatchString(want) {
		return fmt.Errorf("proxmox: %q is not the template name %s of image %s", obj.Name, want, obj.Image)
	}
	id, err := vmid(string(obj.Ref))
	if err != nil {
		return err
	}
	if id < d.cfg.VMIDMin || id > d.cfg.VMIDMax {
		return fmt.Errorf("%w: VMID %d is outside %d–%d", ErrForeignGuest, id, d.cfg.VMIDMin, d.cfg.VMIDMax)
	}
	ms, err := d.members(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ms, func(m member) bool { return m.VMID == id }) {
		return fmt.Errorf("proxmox: template %s (VMID %d) is not in pool %s: %w", want, id, d.cfg.Pool, driver.ErrNotFound)
	}
	scan, err := d.scanPool(ctx, ms)
	if err != nil {
		return err
	}
	cfg, ok := scan.configs[id]
	if !ok {
		return fmt.Errorf("proxmox: template %s (VMID %d): %w", want, id, driver.ErrNotFound)
	}
	desc := cfg["description"]
	switch {
	case cfg["template"] != "1":
		return fmt.Errorf("proxmox: VMID %d is no template; RemoveImage removes only templates", id)
	case cfg["lock"] != "":
		return fmt.Errorf("proxmox: template %d is locked (%s); try again later", id, cfg["lock"])
	case cfg["name"] != want:
		return fmt.Errorf("proxmox: VMID %d has the name %q, not %s; not removed", id, cfg["name"], want)
	case obj.Kind == driver.ImageVersionTemplate && !isVersionOf(desc, obj.Image, obj.Version, obj.Digest):
		return fmt.Errorf("proxmox: template %d does not name image %s v%d with build=%s in its description; not removed", id, obj.Image, obj.Version, obj.Digest)
	case obj.Kind == driver.ImageBaseTemplate && !isBaseOf(desc, obj.Image, obj.Digest):
		return fmt.Errorf("proxmox: template %d does not name image %s with sha256=%s in its description; not removed", id, obj.Image, obj.Digest)
	}
	if linked := scan.linkedClones(id); len(linked) > 0 {
		return fmt.Errorf("proxmox: template %s (VMID %d) has linked clones in the pool (VMIDs %v); Proxmox would delete it under them: remove them or make them full clones first", want, id, linked)
	}
	if err := d.c.Task(ctx, d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}); err != nil {
		return fmt.Errorf("proxmox: delete template %s (VMID %d): %w", want, id, err)
	}
	return nil
}

// --- DiskSpace ---

// DiskSpace reads the use of the disk storage (D-203): the storage status
// gives the size and the real use (for lvmthin: the data of the thin
// pool). When the token can read the thin pools of the node (it needs
// Sys.Audit), the metadata use of the pool with the same size is added.
func (d *Driver) DiskSpace(ctx context.Context) (driver.DiskSpace, error) {
	var st struct {
		Type  string `json:"type"`
		Total int64  `json:"total"`
		Used  int64  `json:"used"`
	}
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.DiskStorage)+"/status", nil, &st); err != nil {
		return driver.DiskSpace{}, fmt.Errorf("proxmox: read the use of %s: %w", d.cfg.DiskStorage, err)
	}
	if st.Total <= 0 {
		return driver.DiskSpace{}, fmt.Errorf("proxmox: storage %s reports no size (is it active?)", d.cfg.DiskStorage)
	}
	out := driver.DiskSpace{Storage: d.cfg.DiskStorage, TotalBytes: st.Total, UsedBytes: st.Used}
	if st.Type != "lvmthin" {
		return out, nil
	}
	var pools []struct {
		LV           string `json:"lv"`
		LVSize       int64  `json:"lv_size"`
		MetadataSize int64  `json:"metadata_size"`
		MetadataUsed int64  `json:"metadata_used"`
	}
	err := d.c.Get(ctx, d.node()+"/disks/lvmthin", nil, &pools)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 403 {
		return out, nil // no Sys.Audit: the metadata use stays unknown
	}
	if err != nil {
		return driver.DiskSpace{}, fmt.Errorf("proxmox: read the thin pools: %w", err)
	}
	// The storage status has no pool name; the pool of the same size is it.
	// Two pools of that size: unknown, not a guess.
	n := 0
	for _, p := range pools {
		if p.LVSize == st.Total && p.MetadataSize > 0 {
			n++
			out.MetaTotalBytes, out.MetaUsedBytes = p.MetadataSize, p.MetadataUsed
		}
	}
	if n != 1 {
		out.MetaTotalBytes, out.MetaUsedBytes = 0, 0
		return out, nil
	}
	out.MetaKnown = true
	return out, nil
}
