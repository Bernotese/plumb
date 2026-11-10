package resource

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestSymlinkQAEdges(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuest(t)
	p := filepath.Join(g.root, "link")
	for _, target := range []string{"link", "../missing/../target", strings.Repeat("../", 1365)} {
		converge(t, build(t, res("symlink", "/link", map[string]any{"target": target}), g.env()))
		if got := readLink(t, p); got != target {
			t.Fatalf("target = %q, want %q", got, target)
		}
	}
	// Use a real observed plan, then swap the link before Apply.
	for _, state := range []string{"present", "absent"} {
		rs := build(t, res("symlink", "/link", map[string]any{"target": "new", "state": state}), g.env())
		obs, err := rs.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		changes := rs.Diff(obs)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, p, "keep")
		if err := rs.Apply(ctx, changes); err == nil {
			t.Fatal("stale plan replaced a file")
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != "keep" {
			t.Fatalf("file changed: %q %v", b, err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("old", p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSymlinkQANonLinks(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"empty directory", "socket"} {
		t.Run(kind, func(t *testing.T) {
			g := newFakeGuest(t)
			p := filepath.Join(g.root, "node")
			if kind == "socket" {
				listener, err := net.Listen("unix", p)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			} else if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []string{"present", "absent"} {
				rs := build(t, res("symlink", "/node", map[string]any{"target": "x", "state": state}), g.env())
				obs, err := rs.Observe(ctx)
				if err != nil {
					t.Fatal(err)
				}
				changes := rs.Diff(obs)
				if len(changes) == 0 || wire.MaxRisk(changes) != wire.RiskDestructive {
					t.Fatalf("plan: %+v", changes)
				}
				if err := rs.Apply(ctx, changes); err == nil {
					t.Fatal("Apply accepted a non-link")
				}
				after, err := os.Lstat(p)
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("node changed: %v", err)
				}
			}
		})
	}
}
