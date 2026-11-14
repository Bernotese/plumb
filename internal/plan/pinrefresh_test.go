package plan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
)

// verifiedPinOnly is the exception to image-recipe (D-335): only a
// pin_refresh whose pins are verified and whose documents differ only in
// the pin.
func TestVerifiedPinOnly(t *testing.T) {
	url := "https://cloud-images.ubuntu.com/releases/26.04/release/u.img"
	old, newer := strings.Repeat("1", 64), strings.Repeat("2", 64)
	spec := func(sha, title string) json.RawMessage {
		return json.RawMessage(`{"type":"vm","title":"` + title + `","url":"` + url + `","sha256":"` + sha + `","verify":{"source":"ubuntu-cloud"}}`)
	}
	base, err := state.Validate([]state.Doc{{Kind: state.KindImage, Name: "u", Spec: spec(old, "a")}})
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	pin := PinRefresh{Image: "u", Source: "ubuntu-cloud", Signed: true, Algorithm: "sha256", Old: old, New: newer, SignedAt: &t2, LastSignedAt: &t1}
	in := func(origin string, p PinRefresh, put state.Doc) Input {
		return Input{Origin: origin, Proposal: Proposal{Put: []state.Doc{put}, Pins: []PinRefresh{p}}}
	}
	good := state.Doc{Kind: state.KindImage, Name: "u", Spec: spec(newer, "a")}
	if !verifiedPinOnly(base, in(OriginPinRefresh, pin, good)) {
		t.Fatal("a verified pin-only change is refused")
	}
	unverified := pin
	unverified.LastSignedAt = nil
	for name, c := range map[string]Input{
		"other origin":     in(OriginProposal, pin, good),
		"other field":      in(OriginPinRefresh, pin, state.Doc{Kind: state.KindImage, Name: "u", Spec: spec(newer, "b")}),
		"other pin":        in(OriginPinRefresh, pin, state.Doc{Kind: state.KindImage, Name: "u", Spec: spec(strings.Repeat("3", 64), "a")}),
		"unverified":       in(OriginPinRefresh, unverified, good),
		"unknown image":    in(OriginPinRefresh, PinRefresh{Image: "x", Signed: true, SignedAt: &t2, LastSignedAt: &t1, Algorithm: "sha256", New: newer}, good),
		"no pins":          {Origin: OriginPinRefresh, Proposal: Proposal{Put: []state.Doc{good}}},
		"a delete as well": {Origin: OriginPinRefresh, Proposal: Proposal{Put: []state.Doc{good}, Pins: []PinRefresh{pin}, Delete: []DocKey{{Kind: "Block", Name: "x"}}}},
	} {
		if verifiedPinOnly(base, c) {
			t.Errorf("%s: accepted", name)
		}
	}
}
