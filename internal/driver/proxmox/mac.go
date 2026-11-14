package proxmox

import "strings"

// net0MAC is the MAC of a net0 value: "virtio=BC:24:11:…,bridge=…" of a
// VM (the model key holds it) or "name=eth0,hwaddr=BC:24:11:…" of a
// container. Lower case; "" when there is none (review S1 of #298).
func net0MAC(net0 string) string {
	for k, v := range parseProps(net0) {
		switch k {
		case "virtio", "e1000", "e1000e", "rtl8139", "vmxnet3", "hwaddr", "macaddr":
			if macRE.MatchString(v) {
				return strings.ToLower(v)
			}
		}
	}
	return ""
}
