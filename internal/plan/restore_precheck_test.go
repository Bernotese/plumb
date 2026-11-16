package plan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
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
	if got, err := backupNICs(map[string]string{"net0": "virtio=x,bridge=vmbr9,trunks=20;30-32"}); err != nil || !reflect.DeepEqual(got[0].Trunks, []int{20, 30, 31, 32}) {
		t.Errorf("trunks: %+v %v", got, err)
	}
	for _, bad := range []string{"bridge=vmbr0,tag=0", "bridge=vmbr0,tag=4095", "bridge=vmbr0,tag=x",
		"bridge=vmbr0,trunks=0", "bridge=vmbr0,trunks=20;x", "bridge=vmbr0,trunks=30-20", "bridge=vmbr0,trunks=4090-4095"} {
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
	// T-253 (host 3b): every flag that the network needs. vmbr9 is not
	// named as a bridge here (only its tag 20), so tag 30 needs both.
	if err := check(configVolume{conf: map[string]string{"net1": "virtio=y,bridge=vmbr9,tag=30"}}); !strings.Contains(err.Error(), "install.sh with --extra-bridge vmbr9 --extra-vlan vmbr9:30 (a repair run)") {
		t.Errorf("flag: %v", err)
	}
	// A named bridge needs only the tag's flag.
	if err := check(configVolume{conf: map[string]string{"net1": "virtio=y,bridge=vmbr0,tag=30"}}); !strings.Contains(err.Error(), "install.sh with --extra-vlan vmbr0:30 (a repair run)") {
		t.Errorf("flag of a named bridge: %v", err)
	}
	// L2 of #369: each trunk VLAN must be named too.
	for conf, want := range map[string]string{
		"virtio=y,bridge=vmbr9,tag=20,trunks=20;30": "bridge vmbr9 tag 30",
		"virtio=y,bridge=vmbr0,trunks=20":           "bridge vmbr0 tag 20",
	} {
		if err := check(configVolume{conf: map[string]string{"net1": conf}}); !errors.Is(err, ErrNotRestorable) || !strings.Contains(err.Error(), want) {
			t.Errorf("trunks %s: %v", conf, err)
		}
	}
	// L1 of #369: a config that cannot be parsed refuses (before the stop).
	if err := check(configVolume{conf: map[string]string{"net0": "virtio=x,bridge=vmbr0,tag=9999"}}); !errors.Is(err, ErrNotRestorable) ||
		!strings.Contains(err.Error(), "cannot be checked") {
		t.Errorf("bad tag: %v", err)
	}
	if err := check(configVolume{err: errors.New("proxmox: 500")}); err != nil {
		t.Errorf("an unreadable config refuses: %v", err)
	}
	if err := check(oneVolume{}); err != nil {
		t.Errorf("no reader: %v", err)
	}
}

// T-253 (host 3b): the Target NIC refusal names every flag, as the restore
// pre-check does (one rule, netFlags).
func TestNotAllowedNamesEveryFlag(t *testing.T) {
	e := &Engine{cfg: Config{NetAllowed: func(bridge string, tag int) bool {
		return bridge == "vmbr0" && tag == 0 || bridge == "vmbr9" && tag == 20
	}}}
	for nic, want := range map[driver.NIC]string{
		{Bridge: "vmbr9", Tag: 30}: "install.sh with --extra-bridge vmbr9 --extra-vlan vmbr9:30 (a repair run)",
		{Bridge: "vmbr0", Tag: 30}: "install.sh with --extra-vlan vmbr0:30 (a repair run)",
		{Bridge: "vmbr7"}:          "install.sh with --extra-bridge vmbr7 (a repair run)",
	} {
		if err := e.notAllowed("db1", 1, nic); !errors.Is(err, ErrNetworkNotAllowed) || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v", nic, err)
		}
	}
}
