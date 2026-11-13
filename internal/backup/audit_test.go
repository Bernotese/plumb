package backup

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/secret"
)

func TestBackupHeaderLimits(t *testing.T) {
	k := secret.NewKey()
	raw := encrypt(t, k, nil)
	h, pre, err := ReadHeader(bytes.NewReader(raw))
	auditCheck(t, err)
	for _, field := range []string{"kdf", "cipher", "chunk_size", "unknown", "oversize", "salt", "nonce_prefix"} {
		t.Run(field, func(t *testing.T) {
			var obj map[string]any
			if err := json.Unmarshal(pre[len(Magic)+4:], &obj); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "kdf", "cipher":
				obj[field] = "unsupported"
			case "chunk_size":
				obj[field] = ChunkSize + 1
			case "unknown":
				obj[field] = true
			case "oversize":
				obj["plumb_version"] = strings.Repeat("x", MaxHeaderBytes)
			case "salt":
				obj[field] = base64.StdEncoding.EncodeToString(make([]byte, SaltBytes-1))
			case "nonce_prefix":
				obj[field] = base64.StdEncoding.EncodeToString(make([]byte, NoncePrefixBytes-1))
			}
			hb, err := json.Marshal(obj)
			auditCheck(t, err)
			bad := append([]byte(Magic), binary.BigEndian.AppendUint32(nil, uint32(len(hb)))...)
			bad = append(bad, hb...)
			bad = append(bad, raw[len(pre):]...)
			if _, _, err := Open(bytes.NewReader(bad), k); !errors.Is(err, ErrFormat) {
				t.Fatalf("accepted invalid header: %v (original %s)", err, h.KeyFP)
			}
		})
	}
}

func TestBackupChunkLimits(t *testing.T) {
	k := secret.NewKey()
	h, err := newHeader(k, Header{})
	auditCheck(t, err)
	for _, tc := range []struct {
		name string
		size int
		last bool
	}{
		{"oversize", ChunkSize + 1, true}, {"short nonfinal", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w, err := newWriter(&buf, k, h)
			auditCheck(t, err)
			ct := w.g.Seal(nil, nonce(w.prefix, 0, tc.last), make([]byte, tc.size), w.aad)
			buf.Write(binary.BigEndian.AppendUint32(nil, uint32(len(ct))))
			buf.Write(ct)
			if _, err := decrypt(buf.Bytes(), k); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("accepted invalid chunk: %v", err)
			}
		})
	}
}

func TestBackupTarContract(t *testing.T) {
	f := newFixture(t, nil)
	run := f.backup("system")
	raw := f.file(run.Name)
	h, _, err := ReadHeader(bytes.NewReader(raw))
	auditCheck(t, err)
	plain, err := decrypt(raw, f.key)
	auditCheck(t, err)
	for _, mutation := range []string{"name", "type", "manifest", "size", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			var buf bytes.Buffer
			tr, tw := tar.NewReader(bytes.NewReader(plain)), tar.NewWriter(&buf)
			for {
				hd, err := tr.Next()
				if err == io.EOF {
					break
				}
				auditCheck(t, err)
				body, err := io.ReadAll(tr)
				auditCheck(t, err)
				if hd.Name == EntryManifest && (mutation == "manifest" || mutation == "size") {
					var m Manifest
					if err := json.Unmarshal(body, &m); err != nil {
						t.Fatal(err)
					}
					if mutation == "manifest" {
						m.InstallID += "-other"
					} else {
						m.Sizes[EntryDB]++
					}
					body, err = json.Marshal(m)
					auditCheck(t, err)
					hd.Size = int64(len(body))
				}
				if hd.Name == EntryDB {
					if mutation == "name" {
						hd.Name = "unexpected.db"
					}
					if mutation == "type" {
						hd.Typeflag = tar.TypeSymlink
						hd.Linkname = "elsewhere"
						hd.Size = 0
						body = nil
					}
				}
				if err := tw.WriteHeader(hd); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(body); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "extra" {
				if err := tw.WriteHeader(&tar.Header{Name: "extra", Mode: 0600, Size: 0}); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			var encrypted bytes.Buffer
			w, err := newWriter(&encrypted, f.key, h)
			auditCheck(t, err)
			if _, err := w.Write(buf.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Read(bytes.NewReader(encrypted.Bytes()), f.key, nil); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("accepted invalid tar: %v", err)
			}
		})
	}
}

func auditCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
