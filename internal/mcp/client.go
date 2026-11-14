// Package mcp is the MCP server of Plumb (`plumb mcp`, D-364, ARCHITECTURE
// §18): a thin client of the public HTTP API with one API token. It has no
// other way into the controller: every rule (tiers, human-only reasons, the
// copy rule, rate limits) is the server's.
package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxResult is the most bytes of one API answer that a tool returns; a
// longer answer is cut and marked truncated (D-364).
const MaxResult = 256 << 10

// requestTimeout bounds one API call (the event read has its own bound).
const requestTimeout = 30 * time.Second

// Client calls the Plumb API with one bearer token.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// ClientConfig configures a Client.
type ClientConfig struct {
	// URL of the controller, for example https://plumb.lan:8443. Plain
	// http only for a loopback host (plumb serve --dev).
	URL string
	// Token is the API token (tok_….secret).
	Token string
	// CAPEM is the controller CA (ca.crt of the controller); it is the only
	// trusted root for an https URL (the pin). Required for https.
	CAPEM []byte
}

// NewClient checks the config and returns a client.
func NewClient(c ClientConfig) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(c.URL, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("the controller URL must be http(s)://host[:port]")
	}
	u.Path = ""
	if strings.TrimSpace(c.Token) == "" || strings.ContainsAny(c.Token, " \t\r\n") {
		return nil, errors.New("an API token is required (PLUMB_TOKEN or --token-file)")
	}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 4, IdleConnTimeout: time.Minute}
	switch u.Scheme {
	case "https":
		if len(c.CAPEM) == 0 {
			return nil, errors.New("an https controller needs its CA certificate (PLUMB_CA or --ca): the pin")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(c.CAPEM) {
			return nil, errors.New("the CA file holds no PEM certificate")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	case "http":
		if !loopbackHost(u.Hostname()) {
			return nil, errors.New("plain http is allowed only for a loopback controller (plumb serve --dev)")
		}
	default:
		return nil, errors.New("the controller URL must be http(s)://host[:port]")
	}
	return &Client{base: u, token: c.Token, http: &http.Client{
		Transport: tr,
		// A redirect could send the token elsewhere: refuse every one.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Answer is one API answer as the tools pass it on.
type Answer struct {
	Status    int
	Body      []byte // at most MaxResult bytes
	Truncated bool
}

// APIError is a refused call: the problem of the API (no token, no secret
// in it), or a rate limit with the wait.
type APIError struct {
	Status     int
	Type       string
	Title      string
	Detail     string
	RetryAfter int // seconds; for 429
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plumb API answered %d", e.Status)
	if e.Type != "" {
		fmt.Fprintf(&b, " (%s)", e.Type)
	}
	if e.Title != "" {
		b.WriteString(": " + e.Title)
	}
	if e.Detail != "" {
		b.WriteString(". " + e.Detail)
	}
	if e.Status == http.StatusTooManyRequests {
		fmt.Fprintf(&b, " Wait %d seconds before the next call of this kind.", max(e.RetryAfter, 1))
	}
	return b.String()
}

// Do calls the API. path starts with /api/v1/; body is marshalled as JSON
// when not nil. A 2xx answer is returned (cut at MaxResult); every other
// status is an *APIError.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := c.request(ctx, method, path, query, body)
	if err != nil {
		return Answer{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Answer{}, fmt.Errorf("call %s %s: %w", method, path, redact(err, c.token))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxResult+1))
	if err != nil {
		return Answer{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Answer{}, problemOf(resp, b)
	}
	a := Answer{Status: resp.StatusCode, Body: b}
	if len(b) > MaxResult {
		a.Body, a.Truncated = b[:MaxResult], true
	}
	return a, nil
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	u := *c.base
	u.Path = path
	u.RawQuery = query.Encode()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "plumb-mcp")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func problemOf(resp *http.Response, b []byte) *APIError {
	e := &APIError{Status: resp.StatusCode}
	var p struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(b, &p) == nil {
		e.Type = strings.TrimPrefix(p.Type, "urn:plumb:problem:")
		e.Title, e.Detail = clip(p.Title, 200), clip(p.Detail, 1000)
	}
	if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 {
		e.RetryAfter = v
	}
	return e
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// redact removes the token from an error text (a URL error can echo a
// request line; the token is a header, but be sure).
func redact(err error, token string) error {
	if token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
}

// Event is one event of the stream.
type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Events reads the event stream from after since for at most wait or max
// events, then closes it (D-364: events_since).
func (c *Client) Events(ctx context.Context, since int64, wait time.Duration, maxEvents int) ([]Event, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	q := url.Values{"since": {strconv.FormatInt(since, 10)}}
	req, err := c.request(ctx, http.MethodGet, "/api/v1/events", q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("call GET /api/v1/events: %w", redact(err, c.token))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, problemOf(resp, b)
	}
	return readEvents(resp.Body, maxEvents), nil
}

// readEvents parses server-sent events (id, event, data lines) until EOF,
// a read error (the deadline) or max events. A line longer than 1 MiB or
// a data line that is not JSON ends the read.
func readEvents(r io.Reader, maxEvents int) []Event {
	out := []Event{}
	br := newLineReader(r, 1<<20)
	var cur Event
	have := false
	for len(out) < maxEvents {
		line, err := br.line()
		if err != nil {
			return out
		}
		switch {
		case line == "":
			if have && cur.Type != "" {
				out = append(out, cur)
			}
			cur, have = Event{}, false
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "id: "):
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				return out
			}
			cur.Seq, have = n, true
		case strings.HasPrefix(line, "event: "):
			cur.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			d := strings.TrimPrefix(line, "data: ")
			if !json.Valid([]byte(d)) {
				return out
			}
			cur.Data = json.RawMessage(d)
		}
	}
	return out
}

// lineReader reads lines of at most max bytes.
type lineReader struct {
	r   io.Reader
	buf []byte
	max int
}

func newLineReader(r io.Reader, max int) *lineReader { return &lineReader{r: r, max: max} }

func (l *lineReader) line() (string, error) {
	for {
		if i := bytes.IndexByte(l.buf, '\n'); i >= 0 {
			s := string(l.buf[:i])
			l.buf = l.buf[i+1:]
			return strings.TrimSuffix(s, "\r"), nil
		}
		if len(l.buf) > l.max {
			return "", errors.New("event line too long")
		}
		chunk := make([]byte, 4096)
		n, err := l.r.Read(chunk)
		l.buf = append(l.buf, chunk[:n]...)
		if err != nil && n == 0 {
			return "", err
		}
	}
}
