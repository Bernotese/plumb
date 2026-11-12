package state

import (
	"strings"
	"testing"
)

func TestLXCImageShapeBoundaries(t *testing.T) {
	for _, pin := range []string{"", strings.Repeat("a", 127), strings.Repeat("a", 129), strings.Repeat("g", 128)} {
		t.Run("pin-"+pin, func(t *testing.T) {
			if _, err := Validate([]Doc{doc(KindImage, "ct", `{"type":"lxc","url":"`+ctURL+`","sha512":"`+pin+`"}`)}); err == nil {
				t.Fatal("accepted an invalid SHA-512 pin")
			}
		})
	}
	for _, url := range []string{"ftp://example.org/a.tar.zst", "file:///tmp/a.tar.zst", "//example.org/a.tar.zst"} {
		t.Run(url, func(t *testing.T) {
			if _, err := Validate([]Doc{doc(KindImage, "ct", `{"type":"lxc","url":"`+url+`","sha512":"`+sha512+`"}`)}); err == nil {
				t.Fatal("accepted a URL outside http(s)")
			}
		})
	}
}

func TestLXCTargetImageReferences(t *testing.T) {
	for _, tt := range []struct {
		name string
		docs []Doc
		text string
	}{
		{"missing image", []Doc{lxcTarget(`"disk":2`)}, "Image"},
		{"vm image for lxc", []Doc{doc(KindImage, "ct", `{"type":"vm","url":"https://example.org/a.img","sha256":"`+strings.Repeat("a", 64)+`"}`), lxcTarget(`"disk":2`)}, "is for vm"},
		{"lxc image for vm", []Doc{lxcImage(""), doc(KindTarget, "v1", `{"type":"vm","image":"ct","guest":{"cores":1,"memory":512,"disk":2,"bridge":"vmbr0"}}`)}, "is for lxc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.docs)
			errAt(t, err, KindTarget, "/spec/image", tt.text)
		})
	}
}
