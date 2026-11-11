package proxmox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
)

// ListBuilds lists the build guests in the pool and the range (D-203): a
// marker line "plumb-build: <image> v<N>" and the exact name
// plumb-build-<image>-v<N>; a converted one has Template set. Name and template come from the config, as in
// own. A guest whose name and marker do not match is not listed.
func (d *Driver) ListBuilds(ctx context.Context) ([]driver.BuildGuest, error) {
	ms, err := d.members(ctx)
	if err != nil {
		return nil, err
	}
	var out []driver.BuildGuest
	for _, m := range ms {
		if m.VMID < d.cfg.VMIDMin || m.VMID > d.cfg.VMIDMax {
			continue
		}
		cfg, err := d.config(ctx, m.VMID)
		if errors.Is(err, driver.ErrNotFound) {
			continue // gone meanwhile
		}
		if err != nil {
			return nil, err
		}
		image, version, ok := parseBuildLine(lineValue(cfg["description"], buildMarker))
		if !ok || cfg["name"] != buildName(image, version) {
			continue
		}
		out = append(out, driver.BuildGuest{ID: driver.GuestID(strconv.Itoa(m.VMID)), Image: image, Version: version,
			Template: cfg["template"] == "1"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// parseBuildLine reads "<image> v<N>" of a build marker.
func parseBuildLine(v string) (string, int, bool) {
	image, n, ok := strings.Cut(v, " v")
	if !ok || checkImageName(image) != nil {
		return "", 0, false
	}
	version, err := strconv.Atoi(n)
	if err != nil || version < 1 || strconv.Itoa(version) != n {
		return "", 0, false
	}
	return image, version, true
}

// RemoveBuild deletes one build guest after the full guard of T-073 plus
// the build marker: in the range and the pool (own), the exact name, the
// marker line of this version, no template. removeHalfMade then waits for
// the lock, stops the guest, deletes it and proves that it is gone.
func (d *Driver) RemoveBuild(ctx context.Context, gid driver.GuestID, image string, version int) error {
	if err := checkImageName(image); err != nil {
		return err
	}
	name := buildName(image, version)
	id, m, err := d.own(ctx, string(gid))
	if err != nil {
		return err
	}
	cfg, err := d.config(ctx, id)
	if err != nil {
		return err
	}
	if m.Name != name || m.Template == 1 || !hasLine(cfg["description"], buildLine(image, version)) {
		return fmt.Errorf("proxmox: VMID %d is not the build guest %s (name %q, template %d): not removed", id, name, m.Name, m.Template)
	}
	if err := d.removeHalfMade(ctx, id, name, true); err != nil {
		return fmt.Errorf("proxmox: remove build guest %s (VMID %d): %w", name, id, err)
	}
	return nil
}
