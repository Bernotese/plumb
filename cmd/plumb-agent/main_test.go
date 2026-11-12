package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("version: exit code %d", code)
	}
	if got := out.String(); got != "plumb-agent "+version+"\n" {
		t.Errorf("version printed %q", got)
	}
	out.Reset()
	if code := run(nil, &out, &errOut); code != 2 {
		t.Errorf("no arguments: exit code %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage") || out.Len() != 0 {
		t.Errorf("no arguments: stdout %q, stderr %q", out.String(), errOut.String())
	}
}

func TestRunSubcommands(t *testing.T) {
	var out, errOut bytes.Buffer
	missing := t.TempDir() + "/none.json"
	if code := run([]string{"self-test", "-config", missing}, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Errorf("self-test without config: exit %d, stdout %q", code, out.String())
	}
	if code := run([]string{"guard", "-config", missing}, &out, &errOut); code != 1 {
		t.Errorf("guard without config: exit %d", code)
	}
	if code := run([]string{"run", "extra"}, &out, &errOut); code != 2 {
		t.Errorf("run with an extra argument: exit %d", code)
	}
}
