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
	"log/slog"
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
	// Secret is the token secret. It is never logged or printed.
	Secret string
	// CAFile is a PEM file to trust (the host's pve-root-ca.pem). Empty:
	// the system roots. Verification is never skipped (D-032).
	CAFile string
	// TaskPoll is the poll interval for async tasks. Default 500 ms.
	TaskPoll time.Duration
	// TaskTimeout bounds the wait for one task. Default 30 min (an image
	// download can be slow). The task is stopped when the wait gives up.
	TaskTimeout time.Duration
}

// String redacts the secret, so a %v or %+v never prints it.
func (c ClientConfig) String() string {
	return fmt.Sprintf("{URL:%s TokenID:%s Secret:[redacted] CAFile:%s}", c.URL, c.TokenID, c.CAFile)
}

// LogValue redacts the secret in slog.
func (c ClientConfig) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Client is a Proxmox VE API client.
type Client struct {
	base        string
	auth        string // holds the secret: never print a Client
	http        *http.Client
	poll        time.Duration
	taskTimeout time.Duration
}

// String redacts the token.
func (c *Client) String() string { return "proxmox.Client{" + c.base + "}" }

// LogValue redacts the token in slog.
func (c *Client) LogValue() slog.Value { return slog.StringValue(c.String()) }

// NewClient returns a client. It fails when the CA file cannot be read.
func NewClient(cfg ClientConfig) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: URL must be https://host[:port], not %q", cfg.URL)
	}
	if !strings.Contains(cfg.TokenID, "!") || cfg.Secret == "" {
		return nil, errors.New("proxmox: a token id (user@realm!name) and a secret are required")
	}
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
	hc := &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment},
		// A redirect would send the token to another place.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c := &Client{
		base:        strings.TrimSuffix(cfg.URL, "/") + "/api2/json",
		auth:        "PVEAPIToken=" + cfg.TokenID + "=" + cfg.Secret,
		http:        hc,
		poll:        cfg.TaskPoll,
		taskTimeout: cfg.TaskTimeout,
	}
	if c.poll == 0 {
		c.poll = 500 * time.Millisecond
	}
	if c.taskTimeout == 0 {
		c.taskTimeout = 30 * time.Minute
	}
	return c, nil
}

// APIError is an error answer of the API.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
	Errors       map[string]string // per parameter
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("proxmox: %s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Message))
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

// errNotJSON marks a 2xx answer that is no API envelope (for example a
// proxy page). It is never success.
var errNotJSON = errors.New("the answer is not a Proxmox API answer")

// do sends one request. params are form fields for POST/PUT and query
// fields for GET/DELETE. It returns the raw "data" member.
func (c *Client) do(ctx context.Context, method, path string, params url.Values) (json.RawMessage, error) {
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
		return nil, err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL holds no secret; the error is safe to show.
		return nil, fmt.Errorf("proxmox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	return c.answer(resp, method, path)
}

// answer reads an API answer: the "data" member, or an APIError.
func (c *Client) answer(resp *http.Response, method, path string) (json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var env map[string]json.RawMessage
	jsonErr := json.Unmarshal(raw, &env)
	if resp.StatusCode >= 300 {
		ae := &APIError{Method: method, Path: path, Status: resp.StatusCode}
		if jsonErr == nil {
			_ = json.Unmarshal(env["message"], &ae.Message)
			_ = json.Unmarshal(env["errors"], &ae.Errors)
		}
		if ae.Message == "" {
			// PVE puts the reason into the status line.
			ae.Message = strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprint(resp.StatusCode)))
		}
		return nil, ae
	}
	if _, ok := env["data"]; jsonErr != nil || !ok {
		return nil, fmt.Errorf("proxmox: %s %s: %w (status %d)", method, path, errNotJSON, resp.StatusCode)
	}
	return env["data"], nil
}

// Get reads a resource into out.
func (c *Client) Get(ctx context.Context, path string, params url.Values, out any) error {
	data, err := c.do(ctx, http.MethodGet, path, params)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("proxmox: GET %s: decode: %w", path, err)
	}
	return nil
}

// Task sends a change that always starts a task, and waits for it. An
// answer without a UPID is an error, not success (review T-012 #3).
func (c *Client) Task(ctx context.Context, node, method, path string, params url.Values) error {
	upid, err := c.start(ctx, method, path, params)
	if err != nil {
		return err
	}
	if upid == "" {
		return fmt.Errorf("proxmox: %s %s: no task id in the answer", method, path)
	}
	return c.WaitTask(ctx, node, upid)
}

// MaybeTask sends a change that answers with a task id or with null
// (config, template; host items 6 and 8), and waits for a task.
func (c *Client) MaybeTask(ctx context.Context, node, method, path string, params url.Values) error {
	upid, err := c.start(ctx, method, path, params)
	if err != nil || upid == "" {
		return err
	}
	return c.WaitTask(ctx, node, upid)
}

// start sends a change and returns its UPID, or "" for a null answer.
// Any other data is an error.
func (c *Client) start(ctx context.Context, method, path string, params url.Values) (string, error) {
	data, err := c.do(ctx, method, path, params)
	if err != nil {
		return "", err
	}
	if len(data) == 0 || string(data) == "null" {
		return "", nil
	}
	var upid string
	if json.Unmarshal(data, &upid) != nil || !strings.HasPrefix(upid, "UPID:") {
		return "", fmt.Errorf("proxmox: %s %s: unexpected answer %.80s", method, path, data)
	}
	return upid, nil
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

// ErrTaskTimeout is returned when a task did not end within TaskTimeout.
// The client asked Proxmox to stop it.
var ErrTaskTimeout = errors.New("proxmox: the task did not end in time; it was stopped")

// maxPollFailures is how many status reads in a row may fail before the
// wait gives up. One failed read does not end a running task's wait.
const maxPollFailures = 5

// WaitTask waits until a task stopped. A task whose exit status is not OK
// is a *TaskError with the end of its log. The wait is bounded by
// TaskTimeout; when it gives up (timeout, context end, too many failed
// reads) it stops the task, so nothing keeps running unseen (review #4).
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid)
	wctx, cancel := context.WithTimeout(ctx, c.taskTimeout)
	defer cancel()
	t := time.NewTicker(c.poll)
	defer t.Stop()
	failures := 0
	for {
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		err := c.Get(wctx, path+"/status", nil, &st)
		switch {
		case err == nil:
			failures = 0
		case wctx.Err() != nil:
		default:
			failures++
			if failures >= maxPollFailures {
				c.stopTask(path)
				return fmt.Errorf("proxmox: task %s: cannot read its status: %w", upid, err)
			}
		}
		if err == nil && st.Status == "stopped" {
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
		case <-wctx.Done():
			c.stopTask(path)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w (%s after %v)", ErrTaskTimeout, upid, c.taskTimeout)
		case <-t.C:
		}
	}
}

// stopTask asks Proxmox to stop a task. It is best effort with its own
// short deadline, because the caller's context may be done.
func (c *Client) stopTask(taskPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = c.do(ctx, http.MethodDelete, taskPath, nil)
}

// scrub removes secret values from an error text: the raw value and its
// base64 form. PVE echoes rejected parameter values in its errors (review
// T-012 #2); the enrollment token must never reach a run step or a log.
func scrub(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	var all []string
	for _, s := range secrets {
		if s != "" {
			all = append(all, s, b64(s))
		}
	}
	clean := func(s string) string {
		for _, v := range all {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
		return s
	}
	// A joined error (a failure plus a failed cleanup) is cleaned part by
	// part; errors.As below would keep only its first typed part.
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts := j.Unwrap()
		out := make([]error, 0, len(parts))
		for _, e := range parts {
			out = append(out, scrub(e, secrets...))
		}
		return errors.Join(out...)
	}
	switch e := err.(type) {
	case *APIError:
		c := *e
		c.Message = clean(e.Message)
		c.Errors = map[string]string{}
		for k, v := range e.Errors {
			c.Errors[k] = clean(v)
		}
		return &c
	case *TaskError:
		c := *e
		c.ExitStatus = clean(e.ExitStatus)
		c.Log = nil
		for _, l := range e.Log {
			c.Log = append(c.Log, clean(l))
		}
		return &c
	}
	// A wrapped error keeps its whole (cleaned) text and still unwraps to
	// the cleaned inner error, so errors.As works and no context is lost.
	if inner := errors.Unwrap(err); inner != nil {
		return &scrubbed{msg: clean(err.Error()), inner: scrub(inner, secrets...)}
	}
	return errors.New(clean(err.Error()))
}

// scrubbed is a wrapped error after scrub.
type scrubbed struct {
	msg   string
	inner error
}

func (e *scrubbed) Error() string { return e.msg }
func (e *scrubbed) Unwrap() error { return e.inner }
