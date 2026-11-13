package state

import (
	"encoding/json"
	"strings"
	"testing"
)

// D-344, D-345: verify.source fits type, url and pin; a vm Image may pin
// sha512 instead of sha256.
func TestImageVerifyRules(t *testing.T) {
	const sha = "0000000000000000000000000000000000000000000000000000000000000000"
	ubuntu := "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img"
	debian := "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2"
	for _, spec := range []string{
		`{"type":"vm","url":"` + ubuntu + `","sha256":"` + sha + `","verify":{"source":"ubuntu-cloud"}}`,
		`{"type":"vm","url":"` + debian + `","sha512":"` + sha512 + `","verify":{"source":"debian-https"}}`,
		`{"type":"vm","url":"` + debian + `","sha512":"` + sha512 + `"}`,
		`{"type":"lxc","url":"` + ctURL + `","sha512":"` + sha512 + `","verify":{"source":"proxmox-aplinfo"}}`,
	} {
		if _, err := Validate([]Doc{doc(KindImage, "x", spec)}); err != nil {
			t.Errorf("%s: %v", spec, err)
		}
	}
	for _, tt := range []struct {
		name, spec, path, text string
	}{
		{"ubuntu source on debian url", `{"type":"vm","url":"` + debian + `","sha256":"` + sha + `","verify":{"source":"ubuntu-cloud"}}`, "/spec/url", "needs a url under https://cloud-images.ubuntu.com/"},
		{"ubuntu source with sha512", `{"type":"vm","url":"` + ubuntu + `","sha512":"` + sha512 + `","verify":{"source":"ubuntu-cloud"}}`, "/spec/sha256", "gives a sha256 pin"},
		{"debian source with sha256", `{"type":"vm","url":"` + debian + `","sha256":"` + sha + `","verify":{"source":"debian-https"}}`, "/spec/sha512", "gives a sha512 pin"},
		{"proxmox source on vm", `{"type":"vm","url":"` + ubuntu + `","sha256":"` + sha + `","verify":{"source":"proxmox-aplinfo"}}`, "/spec/verify/source", "is for a lxc Image"},
		{"ubuntu source on lxc", `{"type":"lxc","url":"` + ctURL + `","sha512":"` + sha512 + `","verify":{"source":"ubuntu-cloud"}}`, "/spec/verify/source", "is for a vm Image"},
		{"proxmox source on a mirror", `{"type":"lxc","url":"https://mirror.example.org/system/x.tar.zst","sha512":"` + sha512 + `","verify":{"source":"proxmox-aplinfo"}}`, "/spec/url", "needs a url under"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate([]Doc{doc(KindImage, "x", tt.spec)})
			errAt(t, err, KindImage, tt.path, tt.text)
		})
	}
}

// A sha256 vm recipe keeps its hash (no sha512 key in its JSON), so no
// built Image of an older controller gets a new build by the upgrade.
func TestRecipeHashUnchangedForSHA256(t *testing.T) {
	r := Recipe{Type: "vm", URL: "https://x/y.img", SHA256: strings.Repeat("a", 64)}
	raw, err := json.Marshal(r)
	if err != nil || strings.Contains(string(raw), "sha512") {
		t.Fatalf("recipe JSON %s (%v)", raw, err)
	}
	with := r
	with.SHA256, with.SHA512 = "", strings.Repeat("b", 128)
	if with.Hash() == r.Hash() {
		t.Fatal("a sha512 pin must change the recipe")
	}
}
