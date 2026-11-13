package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

// grow uses only the mounted root and its direct disk. It never accepts a device from the controller.
func (a *Agent) grow(ctx context.Context, body wire.GrowBody) (item wire.Item) {
	item = wire.Item{Resource: wire.GrowItemID, Status: wire.StatusFailed}
	fail := func(err error) wire.Item { item.Output = err.Error(); return item }
	if body.MinBytes <= 0 {
		return fail(fmt.Errorf("grow: min_bytes must be positive"))
	}
	run := a.env.Run.Run
	out, err := run(ctx, "findmnt", "--noheadings", "--output", "SOURCE,FSTYPE", "--target", "/")
	if err != nil {
		return fail(err)
	}
	root := strings.Fields(string(out))
	if len(root) != 2 || (root[1] != "ext4" && root[1] != "xfs") {
		return fail(fmt.Errorf("grow: root must be an ext4 or xfs disk partition"))
	}
	type block struct {
		Path, Type, PKName string
		Start, Size        json.Number
	}
	var inventory struct{ Blockdevices []block }
	scan := func() error {
		out, err := run(ctx, "lsblk", "--json", "--bytes", "--list", "--output", "PATH,TYPE,PKNAME,START,SIZE")
		if err != nil {
			return err
		}
		return json.Unmarshal(out, &inventory)
	}
	if err := scan(); err != nil {
		return fail(err)
	}
	var part, disk block
	for _, b := range inventory.Blockdevices {
		if b.Path == root[0] {
			part = b
		}
	}
	for _, b := range inventory.Blockdevices {
		if filepath.Base(b.Path) == filepath.Base(part.PKName) {
			disk = b
		}
	}
	if part.Type != "part" || disk.Type != "disk" {
		return fail(fmt.Errorf("grow: root must be a direct disk partition; LVM and RAID are unsupported"))
	}
	start, err := part.Start.Int64()
	if err != nil {
		return fail(err)
	}
	for _, b := range inventory.Blockdevices {
		if b.Type == "part" && b.PKName == part.PKName && b.Path != part.Path {
			other, err := b.Start.Int64()
			if err != nil {
				return fail(err)
			}
			if other >= start {
				return fail(fmt.Errorf("grow: root is not the last partition of its disk"))
			}
		}
	}
	number, err := os.ReadFile(filepath.Join(a.env.Root, "sys/class/block", filepath.Base(part.Path), "partition"))
	if err != nil {
		return fail(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(number)))
	if err != nil || n <= 0 {
		return fail(fmt.Errorf("grow: invalid root partition number"))
	}
	deadline := time.NewTimer(wire.GrowWaitSeconds * time.Second)
	defer deadline.Stop()
	for {
		size, err := disk.Size.Int64()
		if err != nil {
			return fail(err)
		}
		if size >= body.MinBytes {
			break
		}
		// Non-SCSI disks have no rescan file; their kernel size can still update asynchronously.
		rescan := filepath.Join(a.env.Root, "sys/class/block", filepath.Base(disk.Path), "device/rescan")
		f, err := os.OpenFile(rescan, os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString("1\n")
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-deadline.C:
			return fail(fmt.Errorf("grow: disk remains smaller than min_bytes"))
		case <-time.After(time.Second):
		}
		if err := scan(); err != nil {
			return fail(err)
		}
		for _, b := range inventory.Blockdevices {
			if b.Path == disk.Path {
				disk = b
			}
		}
	}
	out, err = run(ctx, "growpart", disk.Path, strconv.Itoa(n))
	var exit *resource.ExitError
	unchanged := errors.As(err, &exit) && exit.Code == 1 && strings.HasPrefix(string(out), "NOCHANGE:")
	if err != nil && !unchanged {
		return fail(err)
	}
	// Always finish a previous partial grow, even when growpart has nothing left to do.
	if root[1] == "ext4" {
		_, err = run(ctx, "resize2fs", part.Path)
	} else {
		_, err = run(ctx, "xfs_growfs", "/")
	}
	if err != nil {
		return fail(err)
	}
	item.Status = wire.StatusApplied
	if unchanged {
		item.Status = wire.StatusUnchanged
	}
	return item
}
