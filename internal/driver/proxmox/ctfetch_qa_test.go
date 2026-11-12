package proxmox

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Resolve a hostname through a local DNS server, so the real download
// dialer must check the resolved address. No connection leaves the VM.
func TestCTFetchRefusesResolvedAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "169.254.1.1", "169.254.169.254", "fe80::1", "0.0.0.0", "::", "224.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer dns.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				buf := make([]byte, 512)
				for {
					n, peer, err := dns.ReadFrom(buf)
					if err != nil {
						return
					}
					// The Go resolver sends one uncompressed question.
					end := 12
					for end < n && buf[end] != 0 {
						end += int(buf[end]) + 1
					}
					if end+5 > n {
						continue
					}
					end += 5
					reply := append([]byte(nil), buf[:end]...)
					binary.BigEndian.PutUint16(reply[2:4], 0x8180)
					binary.BigEndian.PutUint16(reply[6:8], 0)
					binary.BigEndian.PutUint16(reply[10:12], 0) // omit query's EDNS record
					ip := net.ParseIP(address)
					kind := binary.BigEndian.Uint16(buf[end-4 : end-2])
					var data []byte
					if kind == 1 {
						data = ip.To4()
					}
					if kind == 28 && ip.To4() == nil {
						data = ip.To16()
					}
					if data != nil {
						binary.BigEndian.PutUint16(reply[6:8], 1)
						reply = append(reply, 0xc0, 0x0c, 0, byte(kind), 0, 1, 0, 0, 0, 0, 0, byte(len(data)))
						reply = append(reply, data...)
					}
					_, _ = dns.WriteTo(reply, peer)
				}
			}()
			old := net.DefaultResolver
			net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "udp4", dns.LocalAddr().String())
			}}
			defer func() { net.DefaultResolver = old; dns.Close(); <-done }()
			e := newCTFetch(t)
			e.d.ct.dialOK = nil
			e.spec.URL = "http://template.test/base.tar.gz"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = e.d.FetchImage(ctx, e.spec)
			if err == nil || !strings.Contains(err.Error(), "not allowed for a template download") {
				t.Fatalf("resolved %s: %v", address, err)
			}
			if len(e.f.uploads) != 0 {
				t.Fatal("uploaded refused template")
			}
			e.noTempFiles(t)
		})
	}
}

func TestCTFetchCancelDuringDownload(t *testing.T) {
	e := newCTFetch(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(e.base[:len(e.base)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	result := make(chan error, 1)
	go func() { _, err := e.d.FetchImage(ctx, e.spec); result <- err }()
	// Wait for bytes on disk, rather than cancelling before the fetch starts.
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
waiting:
	for {
		select {
		case <-deadline:
			t.Fatal("download did not reach the temp file")
		case err := <-result:
			t.Fatalf("fetch ended before cancellation: %v", err)
		case <-tick.C:
			files, _ := filepath.Glob(filepath.Join(e.tmp, "ct-*.download"))
			for _, path := range files {
				if st, err := os.Stat(path); err == nil && st.Size() > 0 {
					break waiting
				}
			}
		}
	}
	cancel()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not stop after cancellation")
	}
	if len(e.f.uploads) != 0 {
		t.Fatal("uploaded cancelled template")
	}
	e.noTempFiles(t)
}
