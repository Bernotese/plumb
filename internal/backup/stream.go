package backup

import (
	"archive/tar"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Bernotese/plumb/internal/secret"
)

// Errors of a reader. ErrWrongKey comes before any decryption: the
// fingerprint of the header does not match the key.
var (
	ErrWrongKey = errors.New("backup: wrong recovery key (the key fingerprint does not match)")
	ErrFormat   = errors.New("backup: not a valid backup file")
	ErrCorrupt  = errors.New("backup: the file is damaged or was changed")
)

const tagSize = 16

// Fingerprint returns the key fingerprint of k as the header writes it.
func Fingerprint(k secret.Key) string { return hex.EncodeToString(k.BackupFingerprint()) }

// newHeader returns a header with fresh salt and nonce prefix.
func newHeader(k secret.Key, h Header) (Header, error) {
	salt := make([]byte, SaltBytes)
	prefix := make([]byte, NoncePrefixBytes)
	if _, err := rand.Read(salt); err != nil {
		return Header{}, err
	}
	if _, err := rand.Read(prefix); err != nil {
		return Header{}, err
	}
	h.V, h.KDF, h.Cipher, h.ChunkSize = Version, KDF, Cipher, ChunkSize
	h.Salt = base64.StdEncoding.EncodeToString(salt)
	h.NoncePrefix = base64.StdEncoding.EncodeToString(prefix)
	h.KeyFP = Fingerprint(k)
	return h, nil
}

// aead returns the cipher of a header and the nonce prefix.
func aead(k secret.Key, h Header) (cipher.AEAD, []byte, error) {
	salt, err := base64.StdEncoding.DecodeString(h.Salt)
	if err != nil || len(salt) != SaltBytes {
		return nil, nil, fmt.Errorf("%w: bad salt", ErrFormat)
	}
	prefix, err := base64.StdEncoding.DecodeString(h.NoncePrefix)
	if err != nil || len(prefix) != NoncePrefixBytes {
		return nil, nil, fmt.Errorf("%w: bad nonce prefix", ErrFormat)
	}
	key := k.BackupDataKey(salt)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	return g, prefix, nil
}

func nonce(prefix []byte, i uint32, last bool) []byte {
	n := make([]byte, 0, 12)
	n = append(n, prefix...)
	n = binary.BigEndian.AppendUint32(n, i)
	if last {
		return append(n, 1)
	}
	return append(n, 0)
}

// encWriter encrypts a plaintext stream into chunks. Close writes the last
// chunk (it may be empty).
type encWriter struct {
	w      io.Writer
	g      cipher.AEAD
	prefix []byte
	aad    []byte
	buf    []byte
	i      uint32
	closed bool
}

// newWriter writes Magic, the header and returns the chunk writer.
func newWriter(w io.Writer, k secret.Key, h Header) (*encWriter, error) {
	hdr, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if len(hdr) > MaxHeaderBytes {
		return nil, errors.New("backup: header too large")
	}
	pre := make([]byte, 0, len(Magic)+4+len(hdr))
	pre = append(pre, Magic...)
	pre = binary.BigEndian.AppendUint32(pre, uint32(len(hdr)))
	pre = append(pre, hdr...)
	if _, err := w.Write(pre); err != nil {
		return nil, err
	}
	g, prefix, err := aead(k, h)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pre)
	return &encWriter{w: w, g: g, prefix: prefix, aad: sum[:], buf: make([]byte, 0, ChunkSize)}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(e.buf) == ChunkSize {
			// More data follows: this chunk is not the last.
			if err := e.flush(false); err != nil {
				return n, err
			}
		}
		c := min(len(p), ChunkSize-len(e.buf))
		e.buf = append(e.buf, p[:c]...)
		p, n = p[c:], n+c
	}
	return n, nil
}

func (e *encWriter) flush(last bool) error {
	if e.i == ^uint32(0) {
		return errors.New("backup: too many chunks")
	}
	ct := e.g.Seal(nil, nonce(e.prefix, e.i, last), e.buf, e.aad)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	if _, err := e.w.Write(l[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	clear(e.buf)
	e.buf = e.buf[:0]
	e.i++
	return nil
}

// Close writes the last chunk.
func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

// ReadHeader reads Magic and the header. The header is not authenticated
// yet: show it only as "claimed" until the chunks opened. pre is the
// exact bytes before the first chunk (the AAD input).
func ReadHeader(r io.Reader) (h Header, pre []byte, err error) {
	var fixed [len(Magic) + 4]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return Header{}, nil, fmt.Errorf("%w: short file", ErrFormat)
	}
	if string(fixed[:len(Magic)]) != Magic {
		return Header{}, nil, fmt.Errorf("%w: wrong magic", ErrFormat)
	}
	n := binary.BigEndian.Uint32(fixed[len(Magic):])
	if n == 0 || n > MaxHeaderBytes {
		return Header{}, nil, fmt.Errorf("%w: header length %d", ErrFormat, n)
	}
	hdr := make([]byte, n)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return Header{}, nil, fmt.Errorf("%w: short header", ErrFormat)
	}
	dec := json.NewDecoder(bytes.NewReader(hdr))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Header{}, nil, fmt.Errorf("%w: header: %v", ErrFormat, err)
	}
	if h.V != Version {
		return Header{}, nil, fmt.Errorf("%w: format version %d (this Plumb reads %d)", ErrFormat, h.V, Version)
	}
	if h.KDF != KDF || h.Cipher != Cipher || h.ChunkSize != ChunkSize {
		return Header{}, nil, fmt.Errorf("%w: unknown crypto parameters", ErrFormat)
	}
	return h, append(fixed[:], hdr...), nil
}

// decReader opens the chunks one by one.
type decReader struct {
	r      io.Reader
	g      cipher.AEAD
	prefix []byte
	aad    []byte
	i      uint32
	buf    []byte
	done   bool
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *decReader) next() error {
	var l [4]byte
	if _, err := io.ReadFull(d.r, l[:]); err != nil {
		return fmt.Errorf("%w: the file ends without its last chunk", ErrCorrupt)
	}
	n := binary.BigEndian.Uint32(l[:])
	if n < tagSize || n > ChunkSize+tagSize {
		return fmt.Errorf("%w: chunk %d has length %d", ErrCorrupt, d.i, n)
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return fmt.Errorf("%w: the file ends inside chunk %d", ErrCorrupt, d.i)
	}
	// Try "not last" first, then "last": the flag is part of the nonce.
	pt, err := d.g.Open(nil, nonce(d.prefix, d.i, false), ct, d.aad)
	if err != nil {
		pt, err = d.g.Open(nil, nonce(d.prefix, d.i, true), ct, d.aad)
		if err != nil {
			return fmt.Errorf("%w: chunk %d does not open", ErrCorrupt, d.i)
		}
		d.done = true
		var extra [1]byte
		if m, _ := io.ReadFull(d.r, extra[:]); m > 0 {
			return fmt.Errorf("%w: data after the last chunk", ErrCorrupt)
		}
	} else if len(pt) != ChunkSize {
		return fmt.Errorf("%w: chunk %d is short and not the last", ErrCorrupt, d.i)
	}
	d.i++
	d.buf = pt
	return nil
}

// Open checks the header and the key and returns a reader of the
// plaintext tar stream. ErrWrongKey comes before any decryption. Every
// read error of the stream is ErrCorrupt: a cut, changed, reordered or
// swapped chunk never opens.
func Open(r io.Reader, k secret.Key) (Header, io.Reader, error) {
	h, pre, err := ReadHeader(r)
	if err != nil {
		return Header{}, nil, err
	}
	if subtle.ConstantTimeCompare([]byte(h.KeyFP), []byte(Fingerprint(k))) != 1 {
		return h, nil, ErrWrongKey
	}
	g, prefix, err := aead(k, h)
	if err != nil {
		return h, nil, err
	}
	sum := sha256.Sum256(pre)
	return h, &decReader{r: r, g: g, prefix: prefix, aad: sum[:]}, nil
}

// Read opens a backup and walks its tar entries: exactly PlainEntries in
// order, regular files, sizes as the manifest says, nothing after them.
// fn gets each entry after the manifest; nil fn only verifies. Read
// returns the header and the manifest only when the whole file is good.
func Read(r io.Reader, k secret.Key, fn func(name string, size int64, r io.Reader) error) (Header, Manifest, error) {
	h, plain, err := Open(r, k)
	if err != nil {
		return h, Manifest{}, err
	}
	tr := tar.NewReader(plain)
	var m Manifest
	for i, want := range PlainEntries {
		hd, err := tr.Next()
		if err != nil {
			return h, Manifest{}, tarErr(err, "entry "+want)
		}
		if hd.Name != want || hd.Typeflag != tar.TypeReg || hd.Size < 0 {
			return h, Manifest{}, fmt.Errorf("%w: entry %d is %q, want the file %q", ErrCorrupt, i, hd.Name, want)
		}
		if i == 0 {
			if hd.Size > 1<<20 {
				return h, Manifest{}, fmt.Errorf("%w: manifest too large", ErrCorrupt)
			}
			dec := json.NewDecoder(io.LimitReader(tr, hd.Size))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&m); err != nil {
				return h, Manifest{}, tarErr(err, "manifest")
			}
			if m.V != Version || m.InstallID != h.InstallID || m.SchemaVer != h.SchemaVer || m.CreatedAt != h.CreatedAt {
				return h, Manifest{}, fmt.Errorf("%w: the manifest does not match the header", ErrCorrupt)
			}
			continue
		}
		if size, ok := m.Sizes[want]; !ok || size != hd.Size {
			return h, Manifest{}, fmt.Errorf("%w: %s has %d bytes, the manifest says %d", ErrCorrupt, want, hd.Size, size)
		}
		var body io.Reader = tr
		if fn != nil {
			if err := fn(want, hd.Size, tr); err != nil {
				return h, Manifest{}, err
			}
		}
		// Read the rest (all of it when fn is nil), so every chunk opens.
		if _, err := io.Copy(io.Discard, body); err != nil {
			return h, Manifest{}, tarErr(err, want)
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		return h, Manifest{}, fmt.Errorf("%w: data after the last entry", ErrCorrupt)
	}
	// The tar end blocks and padding: read to the last chunk.
	if _, err := io.Copy(io.Discard, plain); err != nil {
		return h, Manifest{}, tarErr(err, "end")
	}
	return h, m, nil
}

func tarErr(err error, what string) error {
	if errors.Is(err, ErrCorrupt) || errors.Is(err, ErrFormat) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", ErrCorrupt, what, err)
}
