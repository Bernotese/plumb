package link

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// ServeListener returns only after the open requests ended: their handlers
// use the store, which the caller closes next (serve shutdown).
func TestServeListenerWaitsForOpenRequests(t *testing.T) {
	f := newFixture(t)
	cred := f.agent("web1")
	entered, release := make(chan struct{}), make(chan struct{})
	f.link.cfg.OnReport = func(context.Context, Delivery) error {
		close(entered)
		<-release
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.link.ServeListener(ctx, ln) }()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: f.link.ClientTLSConfig()}}
	raw, _ := json.Marshal(wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "t", Hostname: "web1", OS: "debian 13", Addrs: []string{}}})
	req, _ := http.NewRequest("POST", "https://"+ln.Addr().String()+wire.PathReport, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+cred)
	req.Header.Set("Content-Type", "application/json")
	sent := make(chan int, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			sent <- 0
			return
		}
		resp.Body.Close()
		sent <- resp.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the report did not reach OnReport")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("ServeListener returned while a request was open: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeListener did not return")
	}
	if code := <-sent; code != http.StatusNoContent {
		t.Fatalf("the open report got %d", code)
	}
}
