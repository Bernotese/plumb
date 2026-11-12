package wire

import "testing"

func TestIsAgentPath(t *testing.T) {
	yes := []string{
		"/etc/plumb-agent", "/etc/plumb-agent/agent.json", "/var/lib/plumb-agent/credential",
		"/var/lib/plumb-agent/", "/var/lib/x/../plumb-agent/marks", "//var/lib/plumb-agent/a",
		"/usr/local/bin/plumb-agent", "/usr/local/sbin/plumb-agent-install", "/usr/local/bin/.plumb-agent.new",
		"/etc/systemd/system/plumb-agent.service", "/etc/systemd/system/plumb-agent.service.d/x.conf",
		"/run/systemd/system/plumb-agent.service.d", "/lib/systemd/system/plumb-agent.service",
		"/etc/systemd/system/multi-user.target.wants/plumb-agent.service",
		// Aliases and more unit dirs (review S1 of T-059).
		"/var/run/systemd/system/plumb-agent.service", "/var/run/systemd/system/plumb-agent.service.d/x.conf",
		"/etc/systemd/system.control/plumb-agent.service.d/50-x.conf", "/run/systemd/system.control/plumb-agent.service",
		"/run/systemd/transient/plumb-agent.service", "/run/systemd/generator.late/plumb-agent.service",
		"/usr/lib/systemd/system/plumb-agent.service",
	}
	no := []string{
		"", "relative/plumb-agent", "/etc/plumb-agent2", "/var/lib/plumb-agentx/a", "/var/lib/plumb",
		"/usr/local/bin/plumb-agent.old", "/etc/systemd/system/nginx.service",
		"/etc/systemd/system/multi-user.target.wants/nginx.service", "/etc/nginx/plumb-agent.service",
	}
	for _, p := range yes {
		if !IsAgentPath(p) {
			t.Errorf("IsAgentPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if IsAgentPath(p) {
			t.Errorf("IsAgentPath(%q) = true, want false", p)
		}
	}
}

func TestAgentPathOf(t *testing.T) {
	tests := []struct {
		r    Resource
		attr string
	}{
		{Resource{Type: "file", Attrs: map[string]any{"path": "/var/lib/plumb-agent/credential"}}, "path"},
		{Resource{Type: "symlink", Attrs: map[string]any{"path": "/tmp/x", "target": "/etc/plumb-agent/ca.pem"}}, "target"},
		{Resource{Type: "symlink", Attrs: map[string]any{"path": "/var/lib/x", "target": "../lib/plumb-agent/credential"}}, "target"},
		{Resource{Type: "user", Attrs: map[string]any{"name": "a", "home": "/var/lib/plumb-agent"}}, "home"},
		{Resource{Type: "symlink", Attrs: map[string]any{"path": "/tmp/x", "target": "plumb-agent"}}, ""},
		{Resource{Type: "package", Attrs: map[string]any{"name": "/var/lib/plumb-agent"}}, ""},
	}
	for _, tt := range tests {
		if attr, _ := AgentPathOf(tt.r); attr != tt.attr {
			t.Errorf("AgentPathOf(%v) = %q, want %q", tt.r.Attrs, attr, tt.attr)
		}
	}
}

func TestAgentConflicts(t *testing.T) {
	file := func(p string) Resource {
		return Resource{ID: "file:" + p, Type: "file", Attrs: map[string]any{"path": p, "content": "x"}}
	}
	link := func(p, target string) Resource {
		return Resource{ID: "symlink:" + p, Type: "symlink", Attrs: map[string]any{"path": p, "target": target}}
	}
	service := func(n string) Resource {
		return Resource{ID: "service:" + n, Type: "service", Attrs: map[string]any{"name": n}}
	}
	tests := []struct {
		name string
		rs   []Resource
		want string // id of the first conflict, "" for none
	}{
		{"parent link", []Resource{link("/opt/x", "/var/lib"), file("/opt/x/plumb-agent/credential")}, "file:/opt/x/plumb-agent/credential"},
		{"relative parent link", []Resource{link("/opt/x", "../var/lib"), file("/opt/x/plumb-agent/credential")}, "file:/opt/x/plumb-agent/credential"},
		{"two links", []Resource{link("/a", "/b"), link("/b", "/etc"), file("/a/plumb-agent/agent.json")}, "file:/a/plumb-agent/agent.json"},
		{"link to unit dir", []Resource{link("/opt/u", "/etc/systemd"), file("/opt/u/system/plumb-agent.service.d/x.conf")}, "file:/opt/u/system/plumb-agent.service.d/x.conf"},
		{"link loop", []Resource{link("/a", "/b"), link("/b", "/a"), file("/a/x")}, ""},
		{"service", []Resource{service("plumb-agent")}, "service:plumb-agent"},
		{"service with suffix", []Resource{service("plumb-agent.service")}, "service:plumb-agent.service"},
		{"unit", []Resource{{ID: "unit:plumb-agent.service", Type: "unit", Attrs: map[string]any{"name": "plumb-agent.service"}}}, "unit:plumb-agent.service"},
		{"unit timer", []Resource{{ID: "unit:plumb-agent.timer", Type: "unit", Attrs: map[string]any{"name": "plumb-agent.timer"}}}, "unit:plumb-agent.timer"},
		{"harmless", []Resource{service("nginx"), {ID: "unit:app.service", Type: "unit", Attrs: map[string]any{"name": "app.service"}}, link("/opt/x", "/var/lib"), file("/opt/x/nginx/a")}, ""},
	}
	for _, tt := range tests {
		got := AgentConflicts(tt.rs)
		switch {
		case tt.want == "" && len(got) > 0:
			t.Errorf("%s: conflicts %+v, want none", tt.name, got)
		case tt.want != "" && (len(got) == 0 || got[0].ID != tt.want):
			t.Errorf("%s: conflicts %+v, want %s", tt.name, got, tt.want)
		}
	}
}

// T-078: the first-boot installer unit of an LXC template (T-076) is the
// agent's too.
func TestIsAgentPathInstallUnit(t *testing.T) {
	for _, p := range []string{
		"/etc/systemd/system/plumb-agent-install.service",
		"/etc/systemd/system/plumb-agent-install.service.d/x.conf",
		"/etc/systemd/system/multi-user.target.wants/plumb-agent-install.service",
		"/lib/systemd/system/plumb-agent-install.service",
	} {
		if !IsAgentPath(p) {
			t.Errorf("IsAgentPath(%q) = false", p)
		}
	}
	for _, p := range []string{"/etc/systemd/system/plumb-agent-install.service.old", "/etc/systemd/system/plumb-agent-installer.service"} {
		if IsAgentPath(p) {
			t.Errorf("IsAgentPath(%q) = true", p)
		}
	}
	for _, n := range []string{"plumb-agent-install", "plumb-agent-install.service", "plumb-agent", "plumb-agent.service", "plumb-agent.timer", "plumb-agent.path", "plumb-agent-install.socket"} {
		if !IsAgentUnit(n) {
			t.Errorf("IsAgentUnit(%q) = false", n)
		}
	}
	if IsAgentUnit("plumb-agent-installer") || IsAgentUnit("nginx") || IsAgentUnit("plumb-agent.service.d") || IsAgentUnit("plumb-agent-x.service") {
		t.Error("IsAgentUnit too wide")
	}
}
