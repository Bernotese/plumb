package state

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func nicDocs(guest string, extra ...Doc) []Doc {
	img := Doc{Kind: KindImage, Name: "deb", Spec: []byte(`{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	return append([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"vm","image":"deb","guest":` + guest + `}`)}}, extra...)
}

func fakeMAC(n int) string { return "02:00:00:00:00:0" + string(rune('0'+n)) }

// T-220 (D-403, D-419): one network resource per more NIC of a VM, with
// Plumb's MAC; every card gets one, also without an address.
func TestAddNICNetworks(t *testing.T) {
	set, err := Validate(nicDocs(`{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[` +
		`{"bridge":"vmbr9"},{"bridge":"vmbr9","tag":20,"ip":"10.9.20.5/24","gateway":"10.9.20.1"},{"bridge":"vmbr9","ip":"dhcp"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Render("web1")
	if err != nil {
		t.Fatal(err)
	}
	before := len(r.Resources)
	if err := r.AddNICNetworks(fakeMAC, 0); err != nil {
		t.Fatal(err)
	}
	got := r.Resources[before:]
	if len(got) != 3 {
		t.Fatalf("resources %+v", got)
	}
	want := []map[string]any{
		{"name": "nic1", "mac": fakeMAC(1), "dhcp": false},
		{"name": "nic2", "mac": fakeMAC(2), "dhcp": false, "addresses": "10.9.20.5/24", "gateway": "10.9.20.1"},
		{"name": "nic3", "mac": fakeMAC(3), "dhcp": true},
	}
	for i, res := range got {
		if res.ID != wire.ID("network", NICNetworkName(i+1)) || res.Type != "network" || res.Block != NICBlock {
			t.Errorf("resource %d: %+v", i, res)
		}
		if len(res.Attrs) != len(want[i]) {
			t.Errorf("resource %d attrs %v, want %v", i, res.Attrs, want[i])
		}
		for k, v := range want[i] {
			if res.Attrs[k] != v {
				t.Errorf("resource %d %s = %v, want %v", i, k, res.Attrs[k], v)
			}
		}
	}
}

// A container gets no network resource: Proxmox sets its address.
func TestAddNICNetworksLXC(t *testing.T) {
	img := Doc{Kind: KindImage, Name: "ct", Spec: []byte(`{"type":"lxc","url":"http://download.proxmox.com/a.tar.zst","sha512":"` + strings.Repeat("0", 128) + `"}`)}
	set, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web2", Spec: []byte(`{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.6/24"}]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := set.Render("web2")
	before := len(r.Resources)
	if err := r.AddNICNetworks(fakeMAC, 0); err != nil || len(r.Resources) != before {
		t.Fatalf("container: %v %+v", err, r.Resources)
	}
}

// A Block network resource named like a card is refused at the render;
// a Block or override with mac is refused by the server check (D-419).
func TestNICNetworkRefusals(t *testing.T) {
	block := func(name, attrs string) Doc {
		return Doc{Kind: KindBlock, Name: "net", Spec: []byte(`{"resources":[{"network":{"name":"` + name + `"` + attrs + `}}]}`)}
	}
	guest := `{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"}]}`
	withBlock := func(b Doc) []Doc {
		docs := nicDocs(guest, b)
		docs[1].Spec = []byte(`{"type":"vm","image":"deb","guest":` + guest + `,"blocks":[{"use":"net"}]}`)
		return docs
	}
	set, err := Validate(withBlock(block("nic1", `,"addresses":"192.0.2.10/24"`)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := set.Render("web1")
	if err := r.AddNICNetworks(fakeMAC, 0); err == nil || !strings.Contains(err.Error(), "network card 1 of guest.nics") {
		t.Fatalf("name clash: %v", err)
	}
	set, err = Validate(withBlock(block("extra", `,"mac":"02:00:00:00:00:01"`)))
	if err != nil {
		t.Fatal(err) // the schema has mac (agent capability); the server check refuses it
	}
	if _, err := set.Networks(); err == nil || !strings.Contains(err.Error(), "a network card is chosen by guest.nics") {
		t.Fatalf("Block mac: %v", err)
	}
}

// The static address of a more NIC takes part in the address checks: no
// other Target may use it (T-220).
func TestNICAddressTaken(t *testing.T) {
	docs := nicDocs(`{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.5/24"}]}`)
	docs = append(docs, Doc{Kind: KindTarget, Name: "web3", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.5/24"}]}}`)})
	set, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	nets, err := set.Networks()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckNetworks(nets, []string{"web3"}, nil); err == nil || !strings.Contains(err.Error(), "guest.nics[0]") {
		t.Fatalf("a NIC address of another Target passed: %v", err)
	}
}

// Review M1 of #336: after the last card up to absentTo, nic<N> is absent
// without mac (an agent that knows network passes the gate); a Block
// network with such a name is refused too.
func TestAddNICNetworksAbsent(t *testing.T) {
	set, err := Validate(nicDocs(`{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := set.Render("web1")
	before := len(r.Resources)
	if err := r.AddNICNetworks(fakeMAC, 3); err != nil {
		t.Fatal(err)
	}
	got := r.Resources[before:]
	if len(got) != 3 || got[0].ID != "network:nic1" || got[0].Attrs["mac"] != fakeMAC(1) {
		t.Fatalf("resources %+v", got)
	}
	for i, res := range got[1:] {
		name := NICNetworkName(i + 2)
		if res.ID != "network:"+name || len(res.Attrs) != 2 || res.Attrs["name"] != name || res.Attrs[wire.AttrState] != wire.StateAbsent {
			t.Errorf("absent %d: %+v", i+2, res)
		}
	}
	// No nics at all: every index is absent.
	set2, _ := Validate(nicDocs(`{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"}`))
	r2, _ := set2.Render("web1")
	n2 := len(r2.Resources)
	if err := r2.AddNICNetworks(fakeMAC, 3); err != nil || len(r2.Resources) != n2+3 {
		t.Fatalf("no nics: %v %+v", err, r2.Resources)
	}
	// A Block network named nic3 clashes with the absent one.
	b := Doc{Kind: KindBlock, Name: "net", Spec: []byte(`{"resources":[{"network":{"name":"nic3","addresses":"192.0.2.10/24"}}]}`)}
	docs := nicDocs(`{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"}]}`, b)
	docs[1].Spec = []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"}]},"blocks":[{"use":"net"}]}`)
	set3, err := Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := set3.Render("web1")
	if err := r3.AddNICNetworks(fakeMAC, 3); err == nil || !strings.Contains(err.Error(), "network card 3") {
		t.Fatalf("clash with an absent index: %v", err)
	}
	// absentTo 0 (an agent without network): none.
	r4, _ := set2.Render("web1")
	n4 := len(r4.Resources)
	if err := r4.AddNICNetworks(fakeMAC, 0); err != nil || len(r4.Resources) != n4 {
		t.Fatalf("absentTo 0: %+v", r4.Resources)
	}
}
