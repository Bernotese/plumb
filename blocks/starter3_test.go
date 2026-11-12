package blocks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
)

// T-095: the slice 3 starter Blocks render on a VM Target through the
// controller's own validation, and docker-compose is refused on an LXC
// Target (sysctl is vm only, D-260, D-262).
func starter3Docs(t *testing.T, kind string, uses []any) []state.Doc {
	t.Helper()
	image := map[string]any{"type": "vm", "url": "https://example.invalid/debian.img", "sha256": strings.Repeat("0", 64)}
	guest := map[string]any{"cores": 1, "memory": 1024, "disk": 10, "bridge": "vmbr0"}
	if kind == "lxc" {
		image = map[string]any{"type": "lxc", "url": "http://example.invalid/debian.tar.zst", "sha512": strings.Repeat("0", 128)}
		guest["disk"] = 4
	}
	img, _ := json.Marshal(image)
	docs := []state.Doc{{Kind: state.KindImage, Name: "img", Spec: img}}
	for _, name := range []string{"ssh-hardening", "common-tools", "shell", "docker-compose", "deploy-user"} {
		docs = append(docs, blockDoc(t, name+".yaml"))
	}
	spec, err := json.Marshal(map[string]any{"type": kind, "image": "img", "guest": guest, "blocks": uses})
	if err != nil {
		t.Fatal(err)
	}
	return append(docs, state.Doc{Kind: state.KindTarget, Name: "t1", Spec: spec})
}

var composeUse = map[string]any{"use": "docker-compose", "params": map[string]any{
	"name": "whoami", "compose": "services:\n  web:\n    image: busybox\n", "env": map[string]any{"secret": "whoami-env"},
	"distro": "ubuntu", "suite": "resolute",
}}

func TestStarter3RenderOnVM(t *testing.T) {
	uses := []any{
		map[string]any{"use": "ssh-hardening"},
		map[string]any{"use": "common-tools"},
		map[string]any{"use": "shell"},
		composeUse,
	}
	set, err := state.Validate(starter3Docs(t, "vm", uses))
	if err != nil {
		t.Fatal(err)
	}
	r, err := set.Render("t1")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, res := range r.Resources {
		byID[res.ID] = res.Attrs
	}
	repo := byID["apt_repository:docker"]
	if repo == nil || repo["uris"].([]any)[0] != "https://download.docker.com/linux/ubuntu" || repo["suites"].([]any)[0] != "resolute" {
		t.Fatalf("docker repo: %+v", repo)
	}
	if !strings.Contains(repo["key"].(string), "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Fatal("no pinned key")
	}
	env := byID["file:/opt/plumb-compose-whoami/.env"]
	if env["mode"] != "0600" || env["content"] != state.Marker("whoami-env") {
		t.Fatalf(".env: %+v", env)
	}
	if !strings.Contains(byID["file:/opt/plumb-compose-whoami/compose.yaml"]["content"].(string), "busybox") {
		t.Fatal("compose file")
	}
	unit := byID["unit:plumb-compose-whoami.service"]["content"].(string)
	for _, want := range []string{"up --detach", "StandardOutput=null", "StandardError=null"} {
		if !strings.Contains(unit, want) {
			t.Errorf("compose unit lacks %q", want)
		}
	}
	// No secret marker anywhere but the .env file.
	for id, attrs := range byID {
		raw, _ := json.Marshal(attrs)
		if id != "file:/opt/plumb-compose-whoami/.env" && strings.ContainsRune(string(raw), state.MarkStart) {
			t.Errorf("%s holds a secret marker", id)
		}
	}
	sshd := byID["file:/etc/ssh/sshd_config.d/10-plumb-hardening.conf"]["content"].(string)
	for _, want := range []string{"PasswordAuthentication no", "PubkeyAuthentication yes", "PermitRootLogin prohibit-password"} {
		if !strings.Contains(sshd, want) {
			t.Errorf("sshd drop-in lacks %q", want)
		}
	}
	for _, bad := range []string{"AllowUsers", "AllowGroups", "DenyUsers", "AuthorizedKeysFile"} {
		if strings.Contains(sshd, bad) {
			t.Errorf("sshd drop-in sets %s (could lock out the Plumb keys)", bad)
		}
	}
	// ssh-hardening never manages ssh.service or ssh.socket itself.
	for id := range byID {
		if id == "service:ssh" || id == "service:ssh.service" || id == "service:ssh.socket" || id == "service:sshd" {
			t.Errorf("ssh-hardening manages %s", id)
		}
	}
	if byID["file:/etc/profile.d/plumb-shell.sh"] == nil || byID["package:htop"] == nil {
		t.Fatal("shell or common-tools missing")
	}
}

func TestStarter3ComposeRefusedOnLXC(t *testing.T) {
	set, err := state.Validate(starter3Docs(t, "lxc", []any{composeUse}))
	if err == nil {
		_, err = set.Render("t1")
	}
	if err == nil || !strings.Contains(err.Error(), "sysctl") {
		t.Fatalf("docker-compose on lxc: %v", err)
	}
	// The other three work in a container.
	set, err = state.Validate(starter3Docs(t, "lxc", []any{
		map[string]any{"use": "ssh-hardening"}, map[string]any{"use": "common-tools"}, map[string]any{"use": "shell"},
	}))
	if err == nil {
		_, err = set.Render("t1")
	}
	if err != nil {
		t.Fatal(err)
	}
}
