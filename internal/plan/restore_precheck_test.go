package plan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// configVolume is oneVolume with the backup's guest config (T-241).
type configVolume struct {
	oneVolume
	conf map[string]string
	err  error
}

func (c configVolume) BackupConfig(context.Context, string, RestoreVolume) (map[string]string, error) {
	return c.conf, c.err
}

func TestBackupNICs(t *testing.T) {
	got, err := backupNICs(map[string]string{
		"net1":  "virtio=02:00:00:00:00:02,bridge=vmbr9,tag=20,firewall=1",
		"net0":  "virtio=02:00:00:00:00:01,bridge=vmbr0",
		"net2":  "name=eth1,hwaddr=02:00:00:00:00:03,ip=dhcp,type=veth",
		"netx":  "bridge=vmbr7",
		"scsi0": "local-lvm:vm-9001-disk-0,size=4G",
	})
	want := []BackupNIC{{NIC: 0, Bridge: "vmbr0"}, {NIC: 1, Bridge: "vmbr9", Tag: 20}, {NIC: 2}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"bridge=vmbr0,tag=0", "bridge=vmbr0,tag=4095", "bridge=vmbr0,tag=x"} {
		if _, err := backupNICs(map[string]string{"net0": bad}); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// T-244 part 3: a backup with a NIC on a network that the install does not
// name is refused before anything changes, with the bridge, the tag and
// the installer flag. An unreadable config is no refusal (PVE refuses
// before it touches a disk, R1); without the reader there is no check.
func TestCheckBackupNetworks(t *testing.T) {
	ctx := context.Background()
	v := RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:s"}
	allowed := func(bridge string, tag int) bool {
		return bridge == "vmbr0" && tag == 0 || bridge == "vmbr9" && tag == 20
	}
	check := func(r GuestRestorer) error {
		e := &Engine{cfg: Config{GuestRestore: r, NetAllowed: allowed}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		return e.checkBackupNetworks(ctx, "db1", "9001", v)
	}
	ok := map[string]string{"net0": "virtio=x,bridge=vmbr0", "net1": "virtio=y,bridge=vmbr9,tag=20", "net2": "name=eth2,type=veth"}
	if err := check(configVolume{conf: ok}); err != nil {
		t.Fatalf("allowed networks: %v", err)
	}
	for conf, want := range map[string]string{
		"virtio=y,bridge=vmbr9,tag=30": "bridge vmbr9 tag 30, which the install does not name",
		"virtio=y,bridge=vmbr7":        "bridge vmbr7, which the install does not name",
	} {
		err := check(configVolume{conf: map[string]string{"net0": "virtio=x,bridge=vmbr0", "net1": conf}})
		if !errors.Is(err, ErrNotRestorable) || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "NIC 1 of backup "+v.ID) ||
			!strings.Contains(err.Error(), "Nothing was changed") {
			t.Errorf("%s: %v", conf, err)
		}
	}
	if err := check(configVolume{conf: map[string]string{"net1": "virtio=y,bridge=vmbr9,tag=30"}}); !strings.Contains(err.Error(), "--extra-vlan vmbr9:30") {
		t.Errorf("flag: %v", err)
	}
	if err := check(configVolume{err: errors.New("proxmox: 500")}); err != nil {
		t.Errorf("an unreadable config refuses: %v", err)
	}
	if err := check(oneVolume{}); err != nil {
		t.Errorf("no reader: %v", err)
	}
}
