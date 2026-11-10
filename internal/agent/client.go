package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// errUnauthorized is a 401 from the controller: the credential is not valid
// any more, or the enrollment token was refused.
var errUnauthorized = errors.New("controller answered 401")

// maxRetryAfter bounds a Retry-After from the controller.
const maxRetryAfter = 10 * time.Minute

// statusError is any other non-2xx answer.
type statusError struct {
	Status int
	Msg    string
}

func (e *statusError) Error() string { return e.Msg }

// permanent reports whether the controller refused the request for good:
// a 4xx other than 401, 408 and 429. Sending the same request again cannot
// work (for example 409 "unknown command id" after a controller restart).
func permanent(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.Status >= 400 && se.Status < 500 &&
		se.Status != http.StatusUnauthorized && se.Status != http.StatusRequestTimeout && se.Status != http.StatusTooManyRequests
}

// busyError is a 429 or 503: the controller is busy. The agent keeps its
// credential and waits at least RetryAfter (D-055).
type busyError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *busyError) Error() string {
	return fmt.Sprintf("controller busy (%d), retry after %s", e.Status, e.RetryAfter)
}

// retryAfter parses a Retry-After header: seconds or an HTTP date.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.Atoi(h); err == nil {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), maxRetryAfter)
}

// maxCommandBytes limits the body of a command from the controller.
const maxCommandBytes = 16 << 20

// client talks to the agent listener. It trusts only the pinned Plumb CA.
type client struct {
	base string
	http *http.Client
}

func newClient(cfg Config, pollWait time.Duration) (*client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(cfg.CA)) {
		return nil, errors.New("ca holds no PEM certificate")
	}
	tr := &http.Transport{
		// Only the Plumb CA: no system roots, no proxy from the environment.
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: pollWait + 15*time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       90 * time.Second,
	}
	return &client{
		base: strings.TrimSuffix(cfg.URL, "/"),
		http: &http.Client{Transport: tr, Timeout: pollWait + 30*time.Second},
	}, nil
}

// do sends a JSON request. A non-nil out is filled from a 200 answer.
// It returns the status code.
func (c *client) do(ctx context.Context, method, path, cred string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	lim := io.LimitReader(resp.Body, maxCommandBytes+1)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return resp.StatusCode, errUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return resp.StatusCode, &busyError{Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	case resp.StatusCode == http.StatusOK && out != nil:
		b, err := io.ReadAll(lim)
		if err != nil {
			return resp.StatusCode, err
		}
		if len(b) > maxCommandBytes {
			return resp.StatusCode, fmt.Errorf("%s: answer larger than %d bytes", path, maxCommandBytes)
		}
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s: %w", path, err)
		}
		return resp.StatusCode, nil
	case resp.StatusCode/100 == 2:
		_, _ = io.Copy(io.Discard, lim)
		return resp.StatusCode, nil
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, &statusError{Status: resp.StatusCode,
			Msg: fmt.Sprintf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))}
	}
}

func (c *client) enroll(ctx context.Context, req wire.EnrollRequest) (wire.EnrollResponse, error) {
	var resp wire.EnrollResponse
	_, err := c.do(ctx, http.MethodPost, wire.PathEnroll, "", req, &resp)
	if err == nil && (resp.Credential == "" || resp.AgentID == "") {
		err = errors.New("enroll: answer without agent_id or credential")
	}
	return resp, err
}

// poll waits for the next command. It returns nil when there is none.
func (c *client) poll(ctx context.Context, cred string, wait int) (*wire.Command, error) {
	var cmd wire.Command
	code, err := c.do(ctx, http.MethodGet, wire.PathPoll+"?wait="+strconv.Itoa(wait), cred, nil, &cmd)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNoContent {
		return nil, nil
	}
	return &cmd, nil
}

// report sends a report. A report above wire.MaxReportBytes is replaced by
// one without items that says so.
func (c *client) report(ctx context.Context, cred string, r wire.Report) error {
	if b, err := json.Marshal(r); err != nil || len(b) > wire.MaxReportBytes {
		r = wire.Report{Type: r.Type, CommandID: r.CommandID, Error: "report too large; items dropped"}
	}
	_, err := c.do(ctx, http.MethodPost, wire.PathReport, cred, r, nil)
	return err
}
