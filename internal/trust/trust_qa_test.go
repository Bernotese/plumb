package trust

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"
)

func FuzzExactEntryQA(f *testing.F) {
	f.Add("image.img", byte(0xab))
	f.Add("system/image.tar.zst", byte(0))
	f.Fuzz(func(t *testing.T, name string, b byte) {
		if name == "" || strings.ContainsAny(name, "\x00\r\n") || strings.TrimSpace(name) != name {
			t.Skip()
		}
		h := strings.Repeat("0123456789abcdef"[int(b)%16:int(b)%16+1], 64)
		line := []byte(h + " *" + name + "\n")
		sum, err := ParseSums(line, name, 64)
		if err != nil || sum != h {
			t.Fatalf("exact entry: %q %v", sum, err)
		}
		if _, err := ParseSums(append(append([]byte{}, line...), line...), name, 64); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("duplicate: %v", err)
		}
		if _, err := ParseSums(line, name+".other", 64); !errors.Is(err, ErrNoEntry) {
			t.Fatalf("other: %v", err)
		}
		index := []byte("Location: " + name + "\nsha512sum: " + h + h + "\n")
		sum, err = ParseAplinfo(index, name)
		if err != nil || sum != h+h {
			t.Fatalf("index: %q %v", sum, err)
		}
		if _, err := ParseAplinfo(append(append([]byte{}, index...), append([]byte("\n"), index...)...), name); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("index duplicate: %v", err)
		}
	})
}

func TestGunzipBoundaryQA(t *testing.T) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte("abcd"))
	_ = zw.Close()
	if out, err := gunzip(b.Bytes(), 4); err != nil || string(out) != "abcd" {
		t.Fatalf("boundary %q %v", out, err)
	}
	if _, err := gunzip(b.Bytes(), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("overflow %v", err)
	}
}
