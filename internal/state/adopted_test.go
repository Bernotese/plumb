package state

import (
	"strings"
	"testing"
)

const adoptedTarget = `{"type":"vm",` + guest + `,"adopted":{"node":"pve","vmid":150}}`

// D-386: an adopted Target has no image; image and adopted together, or
// neither, are refused.
func TestAdoptedTarget(t *testing.T) {
	s, err := Validate(fixture(doc(KindTarget, "wiki", adoptedTarget)))
	if err != nil {
		t.Fatal(err)
	}
	a, ok := s.AdoptedOf("wiki")
	if !ok || a.Node != "pve" || a.VMID != 150 || a.DeleteGuest {
		t.Fatalf("%+v %v", a, ok)
	}
	r, err := s.Render("wiki")
	if err != nil || r.Image != "" {
		t.Fatalf("render: %+v %v", r, err)
	}
	if _, ok := s.AdoptedOf("nope"); ok {
		t.Fatal("unknown Target is adopted")
	}

	_, err = Validate(fixture(doc(KindTarget, "wiki", `{"type":"vm","image":"img",`+guest+`,"adopted":{"node":"pve","vmid":150}}`)))
	if err == nil || !strings.Contains(err.Error(), "an adopted Target has no image") {
		t.Fatalf("image and adopted: %v", err)
	}
	if _, err = Validate(fixture(doc(KindTarget, "wiki", `{"type":"vm",`+guest+`}`))); err == nil {
		t.Fatal("neither image nor adopted: accepted")
	}
	if _, err = Validate(fixture(doc(KindTarget, "wiki", `{"type":"vm",`+guest+`,"adopted":{"node":"pve","vmid":150,"extra":1}}`))); err == nil {
		t.Fatal("an unknown field in adopted: accepted")
	}
	s, err = Validate(fixture(doc(KindTarget, "wiki", `{"type":"vm",`+guest+`,"adopted":{"node":"pve","vmid":150,"delete_guest":true}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := s.AdoptedOf("wiki"); !a.DeleteGuest {
		t.Fatal("delete_guest not read")
	}
}
