//go:build pve

package pve_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"
)

type object = map[string]any
type apiClient struct {
	t          *testing.T
	client     *http.Client
	url, token string
}

func check(t *testing.T, err error, label string) {
	t.Helper()
	if err != nil {
		t.Fatal(label + " failed (details withheld)")
	}
}

func settings(t *testing.T) *apiClient {
	t.Helper()
	for _, key := range []string{"POOL", "IMAGE_STORAGE", "LXC_ID", "URL", "CA", "TOKEN_FILE", "ADMIN_PW_FILE", "GUEST_KEY", "PVE_SSH", "VM_IP_1", "VM_GW", "IMAGES_FILE",
		"IMAGE_DEBIAN13_URL", "IMAGE_DEBIAN13_SHA256", "IMAGE_UBUNTU2604_URL", "IMAGE_UBUNTU2604_SHA256"} {
		if os.Getenv("PLUMB_TEST_"+key) == "" {
			t.Skip("real-host settings absent; use scripts/test-slice-pve.sh")
		}
	}
	ca, err := os.ReadFile(os.Getenv("PLUMB_TEST_CA"))
	check(t, err, "read CA")
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	jar, err := cookiejar.New(nil)
	check(t, err, "cookie jar")
	return &apiClient{t: t, url: strings.TrimRight(os.Getenv("PLUMB_TEST_URL"), "/"),
		token: secret(t, "TOKEN_FILE"), client: &http.Client{Timeout: 30 * time.Second, Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}}
}

func secret(t *testing.T, key string) string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("PLUMB_TEST_" + key))
	check(t, err, "read credential")
	return strings.TrimSpace(string(b))
}

// Same request/JSON pattern as test/slice, with pinned TLS and redacted errors.
func (a *apiClient) call(method, path string, body any, status int) object {
	a.t.Helper()
	raw, err := json.Marshal(body)
	check(a.t, err, "encode request")
	req, err := http.NewRequest(method, a.url+"/api/v1"+path, bytes.NewReader(raw))
	check(a.t, err, "build request")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", a.url)
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req)
	check(a.t, err, "HTTP request")
	defer resp.Body.Close()
	if resp.StatusCode != status {
		a.t.Fatalf("HTTP %s: got %d, want %d (body withheld)", method, resp.StatusCode, status)
	}
	if status == 204 {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	check(a.t, err, "read response")
	var out object
	check(a.t, json.Unmarshal(b, &out), "decode response")
	return out
}

func id(v any) string { return fmt.Sprintf("%.0f", v) }

func wait(t *testing.T, label string, limit time.Duration, ready func() bool) {
	t.Helper()
	start := time.Now()
	for {
		done := ready()
		if time.Since(start) > limit {
			t.Fatal(label + " timed out")
		}
		if done {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("%s: %s", label, time.Since(start).Round(time.Millisecond))
}

func (a *apiClient) approve(p object, human bool) object {
	a.t.Helper()
	approver := a
	if human {
		h := *a
		h.token = ""
		login := h.call("POST", "/session", object{"username": "admin", "password": secret(a.t, "ADMIN_PW_FILE")}, 200)
		if login["must_change_password"] != false {
			a.t.Fatal("admin password change required")
		}
		defer h.call("DELETE", "/session", nil, 204)
		approver = &h
		a.t.Log("admin session approves destructive cleanup plan")
	} else if reasons, ok := p["human_only"].([]any); ok && len(reasons) != 0 {
		a.t.Fatal("unexpected human approval requirement")
	}
	run := approver.call("POST", "/plans/"+id(p["id"])+"/approve", nil, 202)
	wait(a.t, "run succeeds", 15*time.Minute, func() bool {
		run = a.call("GET", "/runs/"+id(run["id"]), nil, 200)
		if run["status"] == "failed" {
			a.t.Fatal("product run failed; details withheld; stop and report")
		}
		return run["status"] == "succeeded"
	})
	return run
}
