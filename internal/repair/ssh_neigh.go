package repair

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// arpFile is the IPv4 neighbour table of the controller's network
// namespace (a test changes it).
var arpFile = "/proc/net/arp"

// neighMAC returns the MAC that the controller's neighbour table holds
// for an on-link IPv4 address (review S1 of #298). It sends one UDP
// datagram to the discard port first, so the kernel resolves the address;
// a routed address gets no entry of its own and is refused. IPv6 is not
// checked: such an address is never dialled.
func neighMAC(ctx context.Context, ip net.IP) (string, error) {
	v4 := ip.To4()
	if v4 == nil {
		return "", errors.New("no neighbour check for IPv6")
	}
	if c, err := net.DialTimeout("udp4", net.JoinHostPort(v4.String(), "9"), time.Second); err == nil {
		c.Write([]byte{0})
		c.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if mac, ok := arpLookup(v4.String()); ok {
			return mac, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return "", fmt.Errorf("%s has no neighbour entry (not on-link)", v4)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// arpLookup reads a complete entry (flags 0x2) of ip from arpFile.
func arpLookup(ip string) (string, bool) {
	f, err := os.Open(arpFile)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// IP address  HW type  Flags  HW address  Mask  Device
		x := strings.Fields(sc.Text())
		if len(x) < 6 || x[0] != ip {
			continue
		}
		if x[2] != "0x2" && x[2] != "0x6" {
			return "", false // incomplete or failed
		}
		return strings.ToLower(x[3]), true
	}
	return "", false
}
