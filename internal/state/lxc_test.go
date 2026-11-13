package state

import (
	"strings"
	"testing"
)

// T-075: the type rules of Images (D-224) and the LXC Target rules (D-226).

var sha512 = strings.Repeat("ab", 64)

const ctURL = "http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst"

func lxcImage(extra string) Doc {
	return doc(KindImage, "ct", `{"type":"lxc","url":"`+ctURL+`","sha512":"`+sha512+`"`+extra+`}`)
}

func lxcTarget(guest string) Doc {
	return doc(KindTarget, "c1", `{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"bridge":"vmbr0",`+guest+`}}`)
}

func TestLXCImageAccepted(t *testing.T) {
	for _, url := range []string{ctURL, "https://mirror.example.org/debian-13.tar.zst"} {
		d := doc(KindImage, "ct", `{"type":"lxc","url":"`+url+`","sha512":"`+sha512+`"}`)
		if _, err := Validate([]Doc{d, lxcTarget(`"disk":2`)}); err != nil {
			t.Errorf("%s: %v", url, err)
		}
	}
}

func TestImageTypeRules(t *testing.T) {
	const sha = "0000000000000000000000000000000000000000000000000000000000000000"
	build := `,"build":{"blocks":[],"guest":{"disk":10,"bridge":"vmbr0"}}`
	for _, tt := range []struct {
		name, spec, path, text string
	}{
		{"lxc with sha256", `{"type":"lxc","url":"` + ctURL + `","sha512":"` + sha512 + `","sha256":"` + sha + `"}`, "/spec/sha256", "pins sha512, not sha256"},
		{"lxc with sha256 only", `{"type":"lxc","url":"` + ctURL + `","sha256":"` + sha + `"}`, "/spec", "needs sha512"},
		{"lxc without pin", `{"type":"lxc","url":"` + ctURL + `"}`, "/spec", "needs sha512"},
		{"lxc build disk", `{"type":"lxc","url":"` + ctURL + `","sha512":"` + sha512 + `"` + strings.Replace(build, `"disk":10`, `"disk":1`, 1) + `}`, "/spec/build/guest/disk", "at least 2 GiB"},
		{"vm over http", `{"type":"vm","url":"http://example.org/x.img","sha256":"` + sha + `"}`, "/spec/url", "needs an https:// url"},
		{"vm with sha512", `{"type":"vm","url":"https://example.org/x.img","sha256":"` + sha + `","sha512":"` + sha512 + `"}`, "/spec/sha512", "pins sha256, not sha512"},
		{"vm without pin", `{"type":"vm","url":"https://example.org/x.img"}`, "/spec", "needs sha256"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate([]Doc{doc(KindImage, "x", tt.spec)})
			errAt(t, err, KindImage, tt.path, tt.text)
		})
	}
}

func TestLXCTargetRules(t *testing.T) {
	t.Run("user refused", func(t *testing.T) {
		_, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":8,"user":"plumb"`)})
		errAt(t, err, KindTarget, "/spec/guest/user", "no bootstrap user")
	})
	// D-249: the keys of a container live in its description (8 KiB).
	t.Run("ssh keys too large", func(t *testing.T) {
		key := `"ssh-rsa ` + strings.Repeat("A", 700) + ` k@example"`
		keys := strings.TrimSuffix(strings.Repeat(key+",", 10), ",")
		_, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":8,"ssh_keys":[` + keys + `]`)})
		errAt(t, err, KindTarget, "/spec/guest/ssh_keys", "at most 7168 fit")
		if _, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":8,"ssh_keys":[` + key + `,` + key + `]`)}); err != nil {
			t.Errorf("two RSA keys: %v", err)
		}
	})
	// Review S1 of #162: a key must come back byte-equal from the
	// container description.
	t.Run("ssh key edges", func(t *testing.T) {
		for _, k := range []string{`"ssh-ed25519 AAAAC3Nza one@example "`, `" ssh-ed25519 AAAAC3Nza"`, `"ssh-ed25519 AAAAC3Nza a\tb"`, `"ssh-ed25519 AAAAC3Nza a\u0000b"`, `"ssh-ed25519 AAAAC3Nza a\u007fb"`} {
			_, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":8,"ssh_keys":[` + k + `]`)})
			if err == nil || !strings.Contains(err.Error(), "white space") && !strings.Contains(err.Error(), "pattern") {
				t.Errorf("%s: %v", k, err)
			}
		}
		if _, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":8,"ssh_keys":["ssh-ed25519 AAAAC3Nza Jürgen's Schlüssel ✓"]`)}); err != nil {
			t.Errorf("non-ASCII comment: %v", err)
		}
	})
	t.Run("disk below 2 GiB", func(t *testing.T) {
		_, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":1`)})
		errAt(t, err, KindTarget, "/spec/guest/disk", "at least 2 GiB")
	})
	t.Run("keys, static ip and allow_restart", func(t *testing.T) {
		tgt := doc(KindTarget, "c1", `{"type":"lxc","image":"ct","allow_restart":true,"guest":{"cores":1,"memory":512,"disk":2,"bridge":"vmbr0","ip":"192.0.2.7/24","gateway":"192.0.2.1","ssh_keys":["ssh-ed25519 AAAA me"]}}`)
		set, err := Validate([]Doc{lxcImage(""), tgt})
		if err != nil {
			t.Fatal(err)
		}
		r, err := set.Render("c1")
		if err != nil {
			t.Fatal(err)
		}
		// The default bootstrap user is a cloud-init setting: not for lxc.
		if _, ok := r.Guest.Attrs["user"]; ok {
			t.Errorf("guest attrs of an LXC Target hold user: %v", r.Guest.Attrs)
		}
	})
	t.Run("vm keeps the default user", func(t *testing.T) {
		set, err := Validate(fixture(target(`"blocks":[]`)))
		if err != nil {
			t.Fatal(err)
		}
		r, err := set.Render("t1")
		if err != nil {
			t.Fatal(err)
		}
		if r.Guest.Attrs["user"] != "plumb" {
			t.Errorf("user = %v, want plumb", r.Guest.Attrs["user"])
		}
	})
}

// T-082: an lxc Image may have a build; its Targets get no ssh_keys (D-251).
func TestLXCBuild(t *testing.T) {
	built := lxcImage(`,"build":{"blocks":[],"guest":{"disk":4,"bridge":"vmbr0"}}`)
	if _, err := Validate([]Doc{built, lxcTarget(`"disk":4`)}); err != nil {
		t.Fatalf("lxc build: %v", err)
	}
	_, err := Validate([]Doc{built, lxcTarget(`"disk":4,"ssh_keys":["ssh-ed25519 AAAAC3Nza k"]`)})
	errAt(t, err, KindTarget, "/spec/guest/ssh_keys", "deploy-user (D-251)")
	if _, err := Validate([]Doc{lxcImage(""), lxcTarget(`"disk":4,"ssh_keys":["ssh-ed25519 AAAAC3Nza k"]`)}); err != nil {
		t.Errorf("stock lxc Image with keys: %v", err)
	}
	_, err = Validate([]Doc{built, lxcTarget(`"disk":3`)})
	errAt(t, err, KindTarget, "/spec/guest/disk", "cannot shrink")
}
