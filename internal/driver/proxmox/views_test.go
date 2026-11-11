package proxmox

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// T-056 host check 1: real answers of the two config views and of
// /pending for one VM (testdata/pve/views): nothing pending, after
// "qm set --memory 1536" on the running VM, and after "qm reboot". Proxmox
// gives one digest for the whole config file, so both views and /pending
// carry the same digest; it moves at the write and again when the pending
// value becomes active.
func TestRecordedViewsShareDigest(t *testing.T) {
	read := func(n int) json.RawMessage {
		b, err := os.ReadFile(fmt.Sprintf("testdata/pve/views/%04d.json", n))
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Body struct {
				Data json.RawMessage `json:"data"`
			} `json:"body"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		return r.Body.Data
	}
	type config struct {
		Digest string `json:"digest"`
		Memory string `json:"memory"`
	}
	type row struct {
		Key     string `json:"key"`
		Value   any    `json:"value"`
		Pending any    `json:"pending"`
	}
	wantMem := [][2]string{{"1024", "1024"}, {"1024", "1536"}, {"1536", "1536"}}
	var digests []string
	for i, first := range []int{1, 4, 7} {
		var run, next config
		var pending []row
		json.Unmarshal(read(first), &run)
		json.Unmarshal(read(first+1), &next)
		json.Unmarshal(read(first+2), &pending)
		pd := ""
		for _, r := range pending {
			if r.Key == "digest" {
				pd, _ = r.Value.(string)
			}
		}
		if run.Digest == "" || run.Digest != next.Digest || pd != run.Digest {
			t.Errorf("state %d: digests current=1 %q, config %q, pending %q", i, run.Digest, next.Digest, pd)
		}
		if run.Memory != wantMem[i][0] || next.Memory != wantMem[i][1] {
			t.Errorf("state %d: memory running %s, next %s, want %v", i, run.Memory, next.Memory, wantMem[i])
		}
		digests = append(digests, run.Digest)
	}
	if digests[0] == digests[1] || digests[1] == digests[2] {
		t.Errorf("digest did not move: %v", digests)
	}
}
