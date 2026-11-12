package proxmox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// ctBase is a tiny system template: GNU tar with /usr/bin/curl, gzip.
func ctBase(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, h := range []*tar.Header{
		{Typeflag: tar.TypeDir, Name: "./", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./usr/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./usr/bin/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./usr/local/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./usr/local/sbin/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./etc/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./etc/systemd/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./etc/systemd/system/", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./etc/systemd/system/multi-user.target.wants/", Mode: 0o755},
		{Typeflag: tar.TypeReg, Name: "./usr/bin/curl", Mode: 0o755, Size: 4},
	} {
		h.Format, h.ModTime = tar.FormatGNU, time.Unix(1700000000, 0)
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			io.WriteString(tw, "curl")
		}
	}
	tw.Close()
	if pad := (10240 - b.Len()%10240) % 10240; pad > 0 {
		b.Write(make([]byte, pad))
	}
	var z bytes.Buffer
	gw := gzip.NewWriter(&z)
	gw.Write(b.Bytes())
	gw.Close()
	return z.Bytes()
}

func sum512(b []byte) string {
	s := sha512.Sum512(b)
	return hex.EncodeToString(s[:])
}

type ctFetchEnv struct {
	f    *fakePVE
	d    *Driver
	tmp  string
	base []byte
	hits atomic.Int32
	srv  *httptest.Server
	spec driver.ImageSpec
}

func newCTFetch(t *testing.T) *ctFetchEnv {
	t.Helper()
	e := &ctFetchEnv{f: newFakePVE(t), base: ctBase(t)}
	e.d = e.f.driver(t)
	e.d.templates = true
	e.d.ct.dialOK = func(net.IP) bool { return true } // the test server is on loopback
	e.d.ct.free = func(string) (int64, error) { return 10 << 30, nil }
	e.tmp = filepath.Join(t.TempDir(), "tmp")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a real certificate, PEM characters only")})
	if err := e.d.SetCTSource(CTSource{TempDir: e.tmp, CA: ca, AgentURL: "https://192.0.2.5:8444"}); err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("the download sends credentials")
		}
		w.Write(e.base)
	}))
	t.Cleanup(e.srv.Close)
	e.spec = driver.ImageSpec{Name: "debian-13.6-ct", Kind: wire.KindLXC, URL: e.srv.URL + "/system/debian-13.tar.gz", SHA512: sum512(e.base)}
	return e
}

func (e *ctFetchEnv) noTempFiles(t *testing.T) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(e.tmp, "*"))
	if len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestCTFetchUploadsAndReuses(t *testing.T) {
	e := newCTFetch(t)
	ctx := context.Background()
	ref, err := e.d.FetchImage(ctx, e.spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reTemplateFile.MatchString(string(ref)) || !strings.HasPrefix(string(ref), "local:vztmpl/plumb-ct-debian-13-6-ct-") {
		t.Errorf("ref %q", ref)
	}
	if got := strings.Join(e.f.uploadOrder, ","); got != "content,checksum-algorithm,checksum,filename" {
		t.Errorf("field order %s", got)
	}
	name := strings.TrimPrefix(string(ref), "local:vztmpl/")
	gz, err := gzip.NewReader(bytes.NewReader(e.f.uploads[name]))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "etc/plumb-agent/agent.json") {
		t.Errorf("no Plumb entries: %v", names)
	}
	e.noTempFiles(t)
	// The file exists: no download, no upload.
	ref2, err := e.d.FetchImage(ctx, e.spec)
	if err != nil || ref2 != ref || e.hits.Load() != 1 || e.f.count("POST /nodes/pve/storage/local/upload") != 1 {
		t.Errorf("reuse: %q %v, downloads %d, uploads %d", ref2, err, e.hits.Load(), e.f.count("POST /nodes/pve/storage/local/upload"))
	}
	// A container from it.
	s := ctSpec()
	s.Image = ref
	if _, err := e.d.Create(ctx, s, driver.Bootstrap{Token: ctToken}); err != nil {
		t.Fatal(err)
	}
}

func TestCTFetchRefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*testing.T, *ctFetchEnv)
		want string
	}{
		{"pin", func(t *testing.T, e *ctFetchEnv) { e.spec.SHA512 = strings.Repeat("0", 128) }, "pinned SHA-512"},
		{"sha256 too", func(t *testing.T, e *ctFetchEnv) { e.spec.SHA256 = strings.Repeat("0", 64) }, "no SHA-256"},
		{"scheme", func(t *testing.T, e *ctFetchEnv) { e.spec.URL = "ftp://example.org/x.tar.zst" }, "http:// or https://"},
		{"user info", func(t *testing.T, e *ctFetchEnv) {
			e.spec.URL = strings.Replace(e.spec.URL, "http://", "http://u:p@", 1)
		}, "without user info"},
		{"loopback", func(t *testing.T, e *ctFetchEnv) { e.d.ct.dialOK = nil }, "not allowed for a template download"},
		{"no vztmpl", func(t *testing.T, e *ctFetchEnv) { e.d.templates = false }, "no content vztmpl"},
		{"disk before", func(t *testing.T, e *ctFetchEnv) {
			e.d.ct.free = func(string) (int64, error) { return 1 << 30, nil }
		}, "less than 2.0 GiB free (now 1.0 GiB)"},
		{"upload space", func(t *testing.T, e *ctFetchEnv) { e.f.imagesAvail = 1 << 10 }, "needs"},
		{"status", func(t *testing.T, e *ctFetchEnv) {
			e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "secret page body", 404)
			})
		}, "HTTP 404"},
		{"redirect", func(t *testing.T, e *ctFetchEnv) {
			e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://mirror.example.org/x", http.StatusFound)
			})
		}, "redirects to mirror.example.org"},
		{"too large", func(t *testing.T, e *ctFetchEnv) { setLimit(t, &maxDownload, 100) }, "larger than"},
		{"too large without length", func(t *testing.T, e *ctFetchEnv) {
			setLimit(t, &maxDownload, 100)
			e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Transfer-Encoding", "chunked")
				w.(http.Flusher).Flush()
				w.Write(e.base)
			})
		}, "larger than"},
		{"disk falls", func(t *testing.T, e *ctFetchEnv) {
			setLimit(t, &checkEvery, 1)
			var n atomic.Int32
			e.d.ct.free = func(string) (int64, error) {
				if n.Add(1) > 2 {
					return 1 << 20, nil
				}
				return 10 << 30, nil
			}
		}, "less than 2.0 GiB free"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newCTFetch(t)
			c.mod(t, e)
			_, err := e.d.FetchImage(context.Background(), e.spec)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if strings.Contains(err.Error(), "secret page body") {
				t.Error("the error shows the body")
			}
			if len(e.f.uploads) != 0 {
				t.Error("uploaded")
			}
			e.noTempFiles(t)
		})
	}
}

func setLimit(t *testing.T, p *int64, v int64) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func TestCTDialCheck(t *testing.T) {
	for ip, ok := range map[string]bool{
		"127.0.0.1": false, "::1": false, "169.254.169.254": false, "fe80::1": false, "0.0.0.0": false, "::": false,
		"224.0.0.1": false, "ff02::1": false, "10.0.0.5": true, "192.168.1.2": true, "203.0.113.7": true, "2001:db8::1": true,
	} {
		if dialCheck(net.ParseIP(ip)) != ok {
			t.Errorf("%s: want %v", ip, ok)
		}
	}
}

// Review S10: a reused file that Proxmox cannot unpack is made again once.
func TestCTCreateRetriesDamagedTemplate(t *testing.T) {
	e := newCTFetch(t)
	ctx := context.Background()
	ref, err := e.d.FetchImage(ctx, e.spec)
	if err != nil {
		t.Fatal(err)
	}
	e.f.failNext["POST /nodes/pve/lxc"] = "unable to create CT 9000 - command 'tar xpf - --zstd' failed: exit code 2"
	s := ctSpec()
	s.Image = ref
	if _, err := e.d.Create(ctx, s, driver.Bootstrap{Token: ctToken}); err != nil {
		t.Fatal(err)
	}
	if n := e.f.count("POST /nodes/pve/storage/local/upload"); n != 2 {
		t.Errorf("uploads %d, want 2", n)
	}
	if n := e.f.countExact("POST /nodes/pve/lxc"); n != 2 {
		t.Errorf("creates %d, want 2", n)
	}
}

func TestSetCTSourceCleansOldTempFiles(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	dir := filepath.Join(t.TempDir(), "tmp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "ct-123.download"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")})
	if err := d.SetCTSource(CTSource{TempDir: dir, CA: ca, AgentURL: "https://192.0.2.5:8444"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ct-123.download")); !os.IsNotExist(err) {
		t.Error("old temp file kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Error("other file removed")
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if err := d.SetCTSource(CTSource{TempDir: dir, CA: []byte("junk"), AgentURL: "https://x:1"}); err == nil {
		t.Error("bad CA accepted")
	}
	if _, err := d.FetchImage(context.Background(), driver.ImageSpec{Name: "x", Kind: wire.KindLXC}); err == nil {
		t.Error("fetch without a pin")
	}
}
