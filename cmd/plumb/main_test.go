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
	if got := out.String(); got != "plumb "+version+"\n" {
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
