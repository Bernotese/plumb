package resource

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type rollbackQARunner struct {
	fail  bool
	calls []string
}

func (r *rollbackQARunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if r.fail && name == "netplan" {
		return nil, errors.New("apply failed")
	}
	return nil, nil
}

func TestNetworkRollbackQA(t *testing.T) {
	for _, previous := range []string{"", "confirmed network\n"} {
		t.Run(previous, func(t *testing.T) {
			root := t.TempDir()
			run := &rollbackQARunner{fail: true}
			env := NewEnv(root, run)
			env.StateDir = t.TempDir()
			file := hostPath(env, NetworkFile("extra"))
			copyPath := networkConfirmed(env, "extra")
			for _, p := range []string{file, copyPath} {
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(copyPath, []byte(previous), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("unconfirmed"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Restore(context.Background(), env, "network"); err == nil {
				t.Fatal("failed apply accepted")
			}
			if _, err := os.Stat(copyPath); err != nil {
				t.Fatal("lost retry copy", err)
			}
			if len(run.calls) != 1 || run.calls[0] != "netplan apply" {
				t.Fatalf("timer stopped before recovery: %v", run.calls)
			}
			run.fail = false
			if err := Restore(context.Background(), env, "network"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(file)
			if previous == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("absent file: %q %v", b, err)
				}
			} else if err != nil || string(b) != previous {
				t.Fatalf("restored %q %v", b, err)
			}
			if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
				t.Fatal("copy retained", err)
			}
			if !strings.Contains(strings.Join(run.calls, "\n"), "systemctl stop "+RollbackUnit("network")+".timer") {
				t.Fatal("timer not stopped")
			}
		})
	}
}

func TestNetworkRollbackScriptQA(t *testing.T) {
	for _, previous := range []string{"", "confirmed network\n"} {
		t.Run(previous, func(t *testing.T) {
			env := NewEnv(t.TempDir(), nil)
			env.StateDir = t.TempDir()
			file := hostPath(env, NetworkFile("extra"))
			cp := networkConfirmed(env, "extra")
			for _, p := range []string{file, cp} {
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(cp, []byte(previous), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("unconfirmed"), 0644); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			stamp := filepath.Join(bin, "called")
			if err := os.WriteFile(filepath.Join(bin, "netplan"), []byte("#!/bin/sh\n[ \"$1\" = apply ] && touch '"+stamp+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			script, err := networkRollbackScript(env, "extra")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script: %s %v", out, err)
			}
			b, err := os.ReadFile(file)
			if previous == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("absent: %q %v", b, err)
				}
			} else {
				if err != nil || string(b) != previous {
					t.Fatalf("restored: %q %v", b, err)
				}
				fi, _ := os.Stat(file)
				if fi.Mode().Perm() != 0600 {
					t.Fatal("unsafe mode")
				}
			}
			if _, err := os.Stat(stamp); err != nil {
				t.Fatal("netplan not applied", err)
			}
		})
	}
}
