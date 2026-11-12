package proxmox

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCTFetchEndlessBodyStopsAtCap(t *testing.T) {
	e := newCTFetch(t)
	setLimit(t, &maxDownload, 1024)
	stopped := make(chan struct{})
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.(http.Flusher).Flush() // chunked: no Content-Length
		chunk := make([]byte, 256)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := e.d.FetchImage(ctx, e.spec)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("endless body: %v", err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("server kept streaming after the cap")
	}
	if len(e.f.uploads) != 0 {
		t.Fatal("uploaded endless body")
	}
	e.noTempFiles(t)
}

func TestCTFetchBodyLongerThanContentLength(t *testing.T) {
	e := newCTFetch(t)
	length := len(e.base) - 1
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Bypass net/http's server-side check on writes beyond Content-Length.
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", length)
		buf.Write(e.base)
		buf.Flush()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The real HTTP transport cuts at the declared length. Check the bytes
	// on disk, then verify that the truncated archive never reaches upload.
	f, err := os.Create(filepath.Join(t.TempDir(), "download"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := e.d.download(ctx, e.spec.URL, f, e.tmp); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, e.base[:length]) {
		t.Fatalf("download was not cut at Content-Length: %d bytes, %v", len(got), err)
	}
	_, err = e.d.FetchImage(ctx, e.spec)
	if err == nil || !strings.Contains(err.Error(), "pinned SHA-512") {
		t.Fatalf("truncated body: %v", err)
	}
	if len(e.f.uploads) != 0 {
		t.Fatal("uploaded body longer than Content-Length")
	}
	e.noTempFiles(t)
}

func TestCTFetchContentLengthJustOverCap(t *testing.T) {
	e := newCTFetch(t)
	setLimit(t, &maxDownload, 1024)
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxDownload+1, 10))
		w.(http.Flusher).Flush()
		// Send no body. A downloader that tries to read it times out.
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := e.d.FetchImage(ctx, e.spec)
	if err == nil || !strings.Contains(err.Error(), "Content-Length 1025") {
		t.Fatalf("oversized Content-Length: %v", err)
	}
	if len(e.f.uploads) != 0 {
		t.Fatal("uploaded oversized body")
	}
	e.noTempFiles(t)
}

func TestCTFetchContentLengthAtCap(t *testing.T) {
	e := newCTFetch(t)
	setLimit(t, &maxDownload, int64(len(e.base)))
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(e.base)))
		_, _ = w.Write(e.base)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := e.d.FetchImage(ctx, e.spec); err != nil {
		t.Fatalf("Content-Length equal to cap: %v", err)
	}
	if len(e.f.uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(e.f.uploads))
	}
	e.noTempFiles(t)
}
