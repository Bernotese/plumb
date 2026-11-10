// Package proxmox is the Driver for Proxmox VE (ARCHITECTURE §2, §5, §7,
// D-029, D-032). It has its own thin REST client on the standard library:
// no SDK. It is the only package that talks to Proxmox.
package proxmox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ClientConfig configures the REST client.
type ClientConfig struct {
	// URL of the API, for example https://pve.lan:8006. The client adds /api2/json.
	URL string
	// TokenID is user@realm!name, for example plumb@pve!plumb.
	TokenID string
	// Secret is the token secret. It is never logged.
	Secret string
	// CAFile is a PEM file to trust (the host's pve-root-ca.pem). Empty:
	// the system roots. Verification is never skipped (D-032).
	CAFile string
	// TaskPoll is the poll interval for async tasks. Default 500 ms.
	TaskPoll time.Duration
	// HTTPClient is for tests that bring their own transport. Its TLS
	// settings are used as given.
	HTTPClient *http.Client
}

// Client is a Proxmox VE API client.
type Client struct {
	base string
	auth string
	http *http.Client
	poll time.Duration
}

// NewClient returns a client. It fails when the CA file cannot be read.
func NewClient(cfg ClientConfig) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: URL must be https://host[:port], not %q", cfg.URL)
	}
	if !strings.Contains(cfg.TokenID, "!") || cfg.Secret == "" {
		return nil, errors.New("proxmox: a token id (user@realm!name) and a secret are required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		tc := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("proxmox: CA file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("proxmox: no certificate in %s", cfg.CAFile)
			}
			tc.RootCAs = pool
		}
		hc = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment}}
	}
	poll := cfg.TaskPoll
	if poll == 0 {
		poll = 500 * time.Millisecond
	}
	return &Client{
		base: strings.TrimSuffix(cfg.URL, "/") + "/api2/json",
		auth: "PVEAPIToken=" + cfg.TokenID + "=" + cfg.Secret,
		http: hc,
		poll: poll,
	}, nil
}

// APIError is an error answer of the API.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
	Errors       map[string]string // per parameter
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("proxmox: %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
	for k, v := range e.Errors {
		msg += fmt.Sprintf("; %s: %s", k, strings.TrimSpace(v))
	}
	return msg
}

// NotFound reports whether the API says the object does not exist. PVE
// answers 500 with "does not exist" for a missing VM (to verify on host).
func (e *APIError) NotFound() bool {
	return e.Status == http.StatusNotFound || strings.Contains(e.Message, "does not exist")
}

// Forbidden reports a permission error of the token.
func (e *APIError) Forbidden() bool { return e.Status == http.StatusForbidden }

// do sends one request. params are form fields for POST/PUT/DELETE and
// query fields for GET. out receives the "data" member.
func (c *Client) do(ctx context.Context, method, path string, params url.Values, out any) error {
	u := c.base + path
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
	} else {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL holds no secret; the error is safe to show.
		return fmt.Errorf("proxmox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var env struct {
		Data    json.RawMessage   `json:"data"`
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode >= 300 {
		msg := env.Message
		if msg == "" {
			// PVE puts the reason into the status line.
			msg = strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprint(resp.StatusCode)))
		}
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: msg, Errors: env.Errors}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("proxmox: %s %s: decode: %w", method, path, err)
	}
	return nil
}

// Get reads a resource.
func (c *Client) Get(ctx context.Context, path string, params url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, params, out)
}

// Post sends a change. Many endpoints answer with a task id (UPID); use
// PostWait for those.
func (c *Client) Post(ctx context.Context, path string, params url.Values, out any) error {
	return c.do(ctx, http.MethodPost, path, params, out)
}

// Put sends a change.
func (c *Client) Put(ctx context.Context, path string, params url.Values, out any) error {
	return c.do(ctx, http.MethodPut, path, params, out)
}

// PostWait sends a change and waits for its task, if the answer is one.
func (c *Client) PostWait(ctx context.Context, node, path string, params url.Values) error {
	return c.wait(ctx, node, http.MethodPost, path, params)
}

// DeleteWait deletes and waits for the task.
func (c *Client) DeleteWait(ctx context.Context, node, path string, params url.Values) error {
	return c.wait(ctx, node, http.MethodDelete, path, params)
}

// PutWait sends a change and waits for its task, if the answer is one.
func (c *Client) PutWait(ctx context.Context, node, path string, params url.Values) error {
	return c.wait(ctx, node, http.MethodPut, path, params)
}

func (c *Client) wait(ctx context.Context, node, method, path string, params url.Values) error {
	var upid string
	var data json.RawMessage
	if err := c.do(ctx, method, path, params, &data); err != nil {
		return err
	}
	if len(data) == 0 || json.Unmarshal(data, &upid) != nil || !strings.HasPrefix(upid, "UPID:") {
		return nil // a synchronous answer
	}
	return c.WaitTask(ctx, node, upid)
}

// TaskError is a task that ended with an exit status other than OK.
type TaskError struct {
	UPID, ExitStatus string
	Log              []string // the last lines of the task log
}

func (e *TaskError) Error() string {
	msg := fmt.Sprintf("proxmox: task %s failed: %s", e.UPID, e.ExitStatus)
	if len(e.Log) > 0 {
		msg += ": " + strings.Join(e.Log, " | ")
	}
	return msg
}

// WaitTask waits until a task stopped. A task whose exit status is not OK
// is a *TaskError with the end of its log.
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid)
	t := time.NewTicker(c.poll)
	defer t.Stop()
	for {
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.Get(ctx, path+"/status", nil, &st); err != nil {
			return err
		}
		if st.Status == "stopped" {
			if st.ExitStatus == "OK" {
				return nil
			}
			te := &TaskError{UPID: upid, ExitStatus: st.ExitStatus}
			var lines []struct {
				T string `json:"t"`
			}
			if c.Get(ctx, path+"/log", url.Values{"start": {"0"}, "limit": {"500"}}, &lines) == nil {
				for i := max(0, len(lines)-5); i < len(lines); i++ {
					te.Log = append(te.Log, lines[i].T)
				}
			}
			return te
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
