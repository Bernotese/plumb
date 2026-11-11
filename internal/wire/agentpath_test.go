package wire

import "testing"

func TestIsAgentPath(t *testing.T) {
	yes := []string{
		"/etc/plumb-agent", "/etc/plumb-agent/agent.json", "/var/lib/plumb-agent/credential",
		"/var/lib/plumb-agent/", "/var/lib/x/../plumb-agent/marks", "//var/lib/plumb-agent/a",
		"/usr/local/bin/plumb-agent", "/usr/local/sbin/plumb-agent-install",
		"/etc/systemd/system/plumb-agent.service", "/etc/systemd/system/plumb-agent.service.d/x.conf",
		"/run/systemd/system/plumb-agent.service.d", "/lib/systemd/system/plumb-agent.service",
		"/etc/systemd/system/multi-user.target.wants/plumb-agent.service",
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
