package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

// T-075, D-235: a new lxc Image, or a new url or sha512 of one, needs a
// human approver (image-recipe).
func TestLXCImageHumanOnly(t *testing.T) {
	pinA, pinB := strings.Repeat("aa", 64), strings.Repeat("bb", 64)
	const sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
	lxc := func(url, pin, title string) string {
		return `{"type":"lxc","url":"` + url + `","sha512":"` + pin + `","title":"` + title + `"}`
	}
	const u1, u2 = "http://download.proxmox.com/a.tar.zst", "http://mirror.example.org/a.tar.zst"
	set := func(spec string) *state.Set {
		t.Helper()
		if spec == "" {
			s, err := state.Validate(nil)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		s, err := state.Validate([]state.Doc{{Kind: state.KindImage, Name: "ct", Spec: json.RawMessage(spec)}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	vm := `{"type":"vm","url":"https://example.org/x.img","sha256":"` + sha256 + `"}`
	for _, tt := range []struct {
		name       string
		base, next string
		want       bool
	}{
		{"new lxc image", "", lxc(u1, pinA, "t"), true},
		{"new sha512", lxc(u1, pinA, "t"), lxc(u1, pinB, "t"), true},
		{"new url", lxc(u1, pinA, "t"), lxc(u2, pinA, "t"), true},
		{"vm turns lxc", vm, lxc(u1, pinA, "t"), true},
		{"title only", lxc(u1, pinA, "t"), lxc(u1, pinA, "other"), false},
		{"unchanged", lxc(u1, pinA, "t"), lxc(u1, pinA, "t"), false},
		{"lxc image deleted", lxc(u1, pinA, "t"), "", false},
		{"new stock vm image", "", vm, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := imagesHumanOnly(set(tt.base), set(tt.next))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("human only = %v, want %v", got, tt.want)
			}
		})
	}
}
