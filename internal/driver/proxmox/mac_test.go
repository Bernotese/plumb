package proxmox

import "testing"

func TestNet0MAC(t *testing.T) {
	for in, want := range map[string]string{
		"virtio=BC:24:11:AA:BB:CC,bridge=vmbr0":                             "bc:24:11:aa:bb:cc",
		"e1000=bc:24:11:00:00:01,bridge=vmbr0,firewall=1":                   "bc:24:11:00:00:01",
		"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:12:34:56,ip=dhcp,type=veth": "bc:24:11:12:34:56",
		"virtio,bridge=vmbr0":                                               "",
		"bridge=vmbr0,tag=AA:BB:CC:DD:EE:FF":                                "",
		"":                                                                  "",
	} {
		if got := net0MAC(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
