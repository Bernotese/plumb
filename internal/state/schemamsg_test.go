package state

import (
	"strings"
	"testing"
)

// T-134 F9: every "not" and the listed rules of the schemas have a plain
// message, and each listed location exists.
func TestSchemaMessagesComplete(t *testing.T) {
	if _, err := schemaMessages(); err != nil {
		t.Fatal(err)
	}
}

// T-134 F9: a failed rule names the attributes and says the rule, not
// "'not' failed" or a repeat of the value.
func TestSchemaRuleMessages(t *testing.T) {
	const key = "-----BEGIN PGP PRIVATE KEY BLOCK-----\\nsecret-key-text\\n-----END PGP PUBLIC KEY BLOCK-----"
	for _, c := range []struct{ res, want string }{
		{`{"firewall":{"name":"main","allow_no_ssh":true,"ssh_port":22}}`, "allow_no_ssh is true: ssh_port and ssh_sources must then be missing"},
		{`{"firewall":{"name":"main","allow_no_ssh":true,"ssh_sources":"10.0.0.0/8"}}`, "allow_no_ssh is true: ssh_port and ssh_sources must then be missing"},
		{`{"firewall":{"name":"main","state":"absent","policy":"drop"}}`, "state is absent: only name and state are allowed"},
		{`{"file":{"path":"/a/../b"}}`, "attribute /path: must not contain a .. component"},
		{`{"directory":{"path":"/a/.."}}`, "attribute /path: must not contain a .. component"},
		{`{"symlink":{"path":"/l","target":"a\u0000"}}`, "attribute /target: must not contain a NUL character"},
		{`{"unit":{"name":"plumb-agent.service","content":"x"}}`, "attribute /name: must not start with plumb-agent"},
		{`{"command":{"name":"c","argv":["/bin/../x"],"creates":"/x"}}`, "attribute /argv/0: must not contain a .. component"},
		{`{"command":{"name":"c","argv":["/bin/x","a\u0000"],"creates":"/x"}}`, "attribute /argv/1: must not contain a NUL character"},
		{`{"user":{"name":"u","home":"/home/../root"}}`, "attribute /home: must not contain a .. component"},
		{`{"apt_repository":{"name":"r","uris":["https://x.invalid"],"suites":["s"],"key":"` + key + `"}}`, "attribute /key: must hold exactly one public key block and no private key"},
	} {
		s, err := Validate([]Doc{doc(KindImage, "img", image), doc(KindBlock, "b", `{"resources":[`+c.res+`]}`), target(`"blocks":[{"use":"b"}]`)})
		if err == nil {
			_, err = s.Render("t1")
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.res, err, c.want)
			continue
		}
		if msg := err.Error(); strings.Contains(msg, "failed") || strings.Contains(msg, "secret-key-text") {
			t.Errorf("%s: raw text: %s", c.res, msg)
		}
	}
	for _, c := range []struct{ guest, want string }{
		{`"ip":"dhcp","gateway":"10.0.0.1"`, "/spec/guest/ip: gateway needs a static ip, not dhcp"},
		{`"gateway":"10.0.0.1"`, "/spec/guest: gateway needs a static ip: set ip too"},
	} {
		_, err := Validate([]Doc{doc(KindImage, "img", image), doc(KindTarget, "t1", `{"type":"vm","image":"img","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0",`+c.guest+`}}`)})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.guest, err, c.want)
		}
	}
	const built = `{"type":"vm","url":"https://example.invalid/i.img","sha256":"0000000000000000000000000000000000000000000000000000000000000000","build":{"guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"blocks":[]},"schedule":{"at":"03:00",`
	for _, c := range []struct{ sched, want string }{
		{`"every":"day","weekday":"mon"}`, "/spec/schedule/every: weekday needs every: week"},
		{`"every":"week","weekday":"mon","day":1}`, "/spec/schedule/every: day needs every: month"},
	} {
		_, err := Validate([]Doc{doc(KindImage, "img", built+c.sched+`}`)})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.sched, err, c.want)
		}
	}
}
