//go:build pve

package pve_test

import (
	"fmt"
	"strings"
)

func inventory(s map[int]string) []string {
	out := []string{}
	for _, n := range sortedIDs(s) {
		kind := "guest"
		if strings.HasSuffix(s[n], " template=1") {
			kind = "template"
		}
		out = append(out, fmt.Sprintf("%d %s", n, kind))
	}
	return out
}
