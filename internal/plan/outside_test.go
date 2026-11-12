package plan

import (
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

// Review S2 of #169: the exact command, quoted values, pending additions
// named, raw lines by file.
func TestOutsideText(t *testing.T) {
	run := driver.GuestSpec{Mounts: []string{"mp0=/srv/data,mp=/data"}, Devices: []string{"dev1=/dev/fuse"},
		RawLXC: []string{"lxc.apparmor.profile: unconfined"}}
	next := run
	next.Mounts = append([]string{"mp0=/srv/data,mp=/data"}, `mp2=/x"; rm -rf /,mp=/y`)
	got := outsideText("9001", driver.GuestState{Spec: run, Pending: &next})
	want := `the container has additions that were made outside Plumb: mp0 "/srv/data,mp=/data", mp2 "/x\"; rm -rf /,mp=/y" (pending, active at the next start), dev1 "/dev/fuse", raw line "lxc.apparmor.profile: unconfined". ` +
		`The Plumb token cannot remove them; only root on the Proxmox host can (D-250). On the host: pct set 9001 --delete mp0,mp2,dev1; ` +
		`remove the line "lxc.apparmor.profile: unconfined" from /etc/pve/lxc/9001.conf. Then plan again, or ignore the Issue`
	if got != want {
		t.Errorf("text\n got %s\nwant %s", got, want)
	}
}
