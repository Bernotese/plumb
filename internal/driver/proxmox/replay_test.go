package proxmox

// Offline replay of answers recorded on the real test host (T-012, make
// test-pve with PLUMB_PVE_RECORD). The recordings hold no header, so no
// token; host, node, addresses, MACs and SSH keys are scrubbed.

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// replayRecording is one recorded exchange (record_test.go writes it).
type replayRecording struct {
	Seq    int             `json:"seq"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  string          `json:"query"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// replayKey is method and path. A config read also keeps current=1: the
// running view and the view after the next cold start are two answers
// (D-190).
func replayKey(method, path, query string) string {
	k := method + " " + path
	if strings.HasSuffix(path, "/config") && method == "GET" {
		if q, err := url.ParseQuery(query); err == nil && q.Get("current") == "1" {
			k += "?current=1"
		}
	}
	return k
}

// replayServer answers each method+path with its recordings in order and
// repeats the last one (task polls). A request without a recording fails
// the test.
type replayServer struct {
	t      *testing.T
	mu     sync.Mutex
	queue  map[string][]replayRecording
	missed []string
}

func newReplay(t *testing.T, dir string) (*replayServer, *Driver) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings in %s: %v", dir, err)
	}
	sort.Strings(files)
	rs := &replayServer{t: t, queue: map[string][]replayRecording{}}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "PVEAPIToken") {
			t.Fatalf("%s holds a token header", f)
		}
		var r replayRecording
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		k := replayKey(r.Method, r.Path, r.Query)
		rs.queue[k] = append(rs.queue[k], r)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(rs.serve))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "pve-root-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(ClientConfig{URL: srv.URL, TokenID: "plumb@pve!plumb", Secret: "replay", CAFile: ca, TaskPoll: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{Client: c, Node: "pve", Pool: "plumb", Nameservers: []string{"192.0.2.53"}, SearchDomain: "plumb.test", Bridge: "vmbr0", DiskStorage: "local-lvm", ImageStorage: "plumb-images",
		SnippetStorage: "plumb-snippets", Snippet: "plumb-vendor.yaml", VMIDMin: 9000, VMIDMax: 9099})
	if err != nil {
		t.Fatal(err)
	}
	return rs, d
}

func (rs *replayServer) serve(w http.ResponseWriter, r *http.Request) {
	k := replayKey(r.Method, strings.TrimPrefix(r.URL.Path, "/api2/json"), r.URL.RawQuery)
	rs.mu.Lock()
	q := rs.queue[k]
	if len(q) == 0 {
		rs.missed = append(rs.missed, k)
		rs.mu.Unlock()
		http.Error(w, "no recording for "+k, 599)
		return
	}
	rec := q[0]
	if len(q) > 1 {
		rs.queue[k] = q[1:]
	}
	rs.mu.Unlock()
	var s string
	if json.Unmarshal(rec.Body, &s) == nil { // a non-JSON answer, kept as a string
		w.WriteHeader(rec.Status)
		_, _ = w.Write([]byte(s))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// The test image of the recording (T-012 log).
var replayImage = driver.ImageSpec{Name: "test-image", Kind: wire.KindVM,
	URL:    "https://cloud.debian.org/images/cloud/trixie/20261103-2651/debian-13-genericcloud-amd64-20261103-2651.qcow2",
	SHA256: "de9d63f5e5ec5927d7548bc05b6043b87457b8c1cb9bcb95365168e8d0293b4f"}

// TestReplayLifecycle runs the host lifecycle against the real answers.
func TestReplayLifecycle(t *testing.T) {
	rs, d := newReplay(t, "testdata/pve/lifecycle")
	// Registered first, so it runs last: after the cleanup of runLifecycle.
	t.Cleanup(func() {
		if len(rs.missed) > 0 {
			t.Errorf("requests without a recording: %v", rs.missed)
		}
	})
	runLifecycle(t, d, replayImage, "vmbr0")
}

// TestReplayCTLifecycle runs the container lifecycle (T-079) against the
// answers of the host: start check, reuse of the template file, create,
// live update, clear, re-enrollment, delete, H11.
func TestReplayCTLifecycle(t *testing.T) {
	rs, d := newReplay(t, "testdata/pve/ct-lifecycle")
	t.Cleanup(func() {
		if len(rs.missed) > 0 {
			t.Errorf("requests without a recording: %v", rs.missed)
		}
	})
	if _, err := d.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.SetCTSource(testCTSource(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	runCTLifecycle(t, d, hostCTImage, "vmbr0", []string{"ssh-ed25519 AAAAtestkey t079", "ssh-ed25519 AAAAtestkey Jürgen's Schlüssel ✓"})
}

// TestReplayCTBuild runs the container build lifecycle (T-082 H-B1)
// against the answers of the host, the late template worker included.
func TestReplayCTBuild(t *testing.T) {
	rs, d := newReplay(t, "testdata/pve/ct-build")
	t.Cleanup(func() {
		if len(rs.missed) > 0 {
			t.Errorf("requests without a recording: %v", rs.missed)
		}
	})
	if _, err := d.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.SetCTSource(testCTSource(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	runCTBuild(t, d, hostCTImage, "vmbr0")
}
