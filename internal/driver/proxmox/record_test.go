//go:build pve

package proxmox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// recorder writes every exchange with the real host to testdata/pve form
// (make test-pve with PLUMB_PVE_RECORD=DIR). It never writes a header, so
// the token is never in a recording; scrub removes the host, the node
// name, addresses, MACs and SSH keys.
type recorder struct {
	next  http.RoundTripper
	dir   string
	scrub func(string) string
	mu    sync.Mutex
	seq   int
}

func newRecorder(next http.RoundTripper, dir, baseURL, node string) *recorder {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Hostname()
	}
	ipv4 := regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	mac := regexp.MustCompile(`\b([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`)
	sshKey := sshKeyRE // leakscan_test.go (review S1 of #167)
	// The enrollment token of a container (env, D-231): in a form, in a
	// config answer and in a pending row (T-079).
	envToken := regexp.MustCompile(`(PLUMB_ENROLL_TOKEN(?:=|%3D|\\u003d))[A-Za-z0-9_-]+`)
	// Each token gets its own placeholder, so the replay still sees an
	// old and a new token as two values (TokenPending).
	tokens := map[string]int{}
	scrub := func(s string) string {
		s = envToken.ReplaceAllStringFunc(s, func(m string) string {
			sub := envToken.FindStringSubmatch(m)
			tok := strings.TrimPrefix(m, sub[1])
			if tokens[tok] == 0 {
				tokens[tok] = len(tokens) + 1
			}
			return fmt.Sprintf("%sscrubbed-%d", sub[1], tokens[tok])
		})
		if host != "" {
			s = strings.ReplaceAll(s, host, "pve.example")
		}
		if node != "" && node != "pve" {
			s = regexp.MustCompile(`\b`+regexp.QuoteMeta(node)+`\b`).ReplaceAllString(s, "pve")
		}
		s = sshKey.ReplaceAllString(s, "$1${2}AAAAtestkey")
		s = mac.ReplaceAllString(s, "BC:24:11:00:00:01")
		// IPv6: only candidates that parse as an address (not a MAC, a
		// time or a UPID), review N2.
		s = ipv6Candidate.ReplaceAllStringFunc(s, func(c string) string {
			if a, err := netip.ParseAddr(c); err == nil && a.Is6() {
				return "2001:db8::10"
			}
			return c
		})
		return ipv4.ReplaceAllString(s, "192.0.2.10")
	}
	return &recorder{next: next, dir: dir, scrub: scrub}
}

// recording is one exchange; replay_test.go reads it.
type recording struct {
	Seq    int             `json:"seq"`
	Method string          `json:"method"`
	Path   string          `json:"path"` // below /api2/json
	Query  string          `json:"query,omitempty"`
	Form   string          `json:"form,omitempty"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var form []byte
	if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/") {
		// A template upload: hundreds of MB, file bytes only (T-079).
		form = []byte(fmt.Sprintf("<multipart upload, %d bytes>", req.ContentLength))
	} else if req.Body != nil {
		form, _ = io.ReadAll(req.Body)
		// Keep the length: a body without it goes out chunked, and
		// pveproxy answers 501 "chunked transfer encoding not supported".
		req = req.Clone(req.Context())
		req.ContentLength = int64(len(form))
		req.Body = io.NopCloser(bytes.NewReader(form))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(form)), nil }
		if len(form) == 0 {
			req.Body, req.GetBody = http.NoBody, nil
		}
	}
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(body))

	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	rec := recording{Seq: r.seq, Method: req.Method, Path: r.scrub(strings.TrimPrefix(req.URL.Path, "/api2/json")),
		Query: r.scrub(req.URL.RawQuery), Form: r.scrub(string(form)), Status: resp.StatusCode}
	clean := r.scrub(string(body))
	if json.Valid([]byte(clean)) {
		rec.Body = json.RawMessage(clean)
	} else {
		rec.Body, _ = json.Marshal(clean) // keep a non-JSON answer as a string
	}
	out, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("%04d.json", r.seq)), append(out, '\n'), 0o644)
	return resp, nil
}
