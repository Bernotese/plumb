// Package ctpack repacks a Proxmox system template for an LXC Target
// (T-038 §2, D-225, D-230, D-236). It copies the 512-byte blocks of the
// base tar unchanged up to the end of its last entry and appends the Plumb
// entries as USTAR: the CA, agent.json, the first-boot installer and its
// units. It adds no agent binary: the container fetches the current agent
// from the controller at its first boot, as a VM does (D-082, D-206).
//
// The rules here give robustness, not a security boundary: the boundary is
// the pinned SHA-512 of the base (D-224), which Pack checks before it
// parses a single byte.
package ctpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Limits of D-225 and D-236. MaxUnpacked counts the bytes that come out of
// the decoder, not the sizes in the tar headers.
const (
	MaxUnpacked = 4 << 30
	MaxEntries  = 500_000

	decoderMaxWindow = 128 << 20
	decoderMaxMemory = 256 << 20

	blockSize  = 512
	recordSize = 10240 // GNU tar's default record (host A3)

	// FormatVersion is part of the file name. Raise it when the bytes
	// that Pack writes for the same inputs change.
	FormatVersion = 1
)

// The limits in force; tests lower them.
var (
	maxUnpacked int64 = MaxUnpacked
	maxEntries        = MaxEntries
)

// Options are the inputs of a repack besides the base file.
type Options struct {
	SHA512   string // pinned SHA-512 of the base, 128 hex digits
	CA       []byte // PEM of the Plumb CA
	AgentURL string // https://host[:port] of the controller (D-082)
}

// Error is a refusal of the base or a limit. Its text is for the user and
// names the rule; it never holds file content.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func refuse(format string, a ...any) error { return &Error{msg: fmt.Sprintf(format, a...)} }

// ErrPin is returned when the base does not match the pinned SHA-512.
var ErrPin = errors.New("the template does not match its pinned SHA-512")

var (
	hex128   = regexp.MustCompile(`^[0-9a-f]{128}$`)
	agentURL = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$|^https://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?$`)
	pemLine  = regexp.MustCompile(`^(-----(BEGIN|END) CERTIFICATE-----|[A-Za-z0-9+/=]*)$`)
	imageRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

func (o Options) check() error {
	if !hex128.MatchString(o.SHA512) {
		return fmt.Errorf("ctpack: the pin is not 128 lower-case hex digits")
	}
	if !agentURL.MatchString(o.AgentURL) {
		return fmt.Errorf("ctpack: bad agent URL %q (https://host[:port])", o.AgentURL)
	}
	// The same rule as install.sh: PEM characters only, so nothing can
	// break agent.json.
	ca := strings.TrimRight(strings.ReplaceAll(string(o.CA), "\r\n", "\n"), "\n")
	if !strings.Contains(ca, "-----BEGIN CERTIFICATE-----") {
		return fmt.Errorf("ctpack: the CA holds no PEM certificate")
	}
	for _, l := range strings.Split(ca, "\n") {
		if !pemLine.MatchString(l) {
			return fmt.Errorf("ctpack: the CA holds more than PEM certificates")
		}
	}
	return nil
}

// Name is the file name of the repacked template of an Image (D-230):
// plumb-ct-<image>-<d12>.tar.gz, d = SHA-256 over the pin, the CA, the
// agent URL and the format version. Same inputs, same name.
func Name(image string, o Options) (string, error) {
	if !imageRe.MatchString(image) {
		return "", fmt.Errorf("ctpack: bad image name %q", image)
	}
	if err := o.check(); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, part := range []string{o.SHA512, string(o.CA), o.AgentURL, fmt.Sprint(FormatVersion)} {
		fmt.Fprintf(h, "%d:%s\n", len(part), part)
	}
	return "plumb-ct-" + image + "-" + hex.EncodeToString(h.Sum(nil))[:12] + ".tar.gz", nil
}

// Pack checks src against the pinned SHA-512, then writes the repacked
// template as .tar.gz to dst. src is read twice (hash, then repack). dst
// holds garbage when Pack fails: the caller removes it. Pack stops when ctx
// is done; it leaves no goroutine behind on any path.
func Pack(ctx context.Context, src io.ReadSeeker, dst io.Writer, o Options) error {
	if err := o.check(); err != nil {
		return err
	}
	h := sha512.New()
	if _, err := io.Copy(h, ctxReader{ctx, src}); err != nil {
		return fmt.Errorf("ctpack: hash the template: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != o.SHA512 {
		return ErrPin
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("ctpack: %w", err)
	}
	return repack(ctx, src, dst, o)
}

func repack(ctx context.Context, src io.Reader, dst io.Writer, o Options) error {
	plain, closeDec, err := decompress(src)
	if err != nil {
		return err
	}
	defer closeDec()

	gz, err := gzip.NewWriterLevel(dst, gzip.BestSpeed)
	if err != nil {
		return err
	}
	// Fixed header: no name, no mtime, OS "unknown". Same inputs → same
	// bytes.
	gz.Header = gzip.Header{OS: 255}

	// The base goes to gz through a writer that holds back its last
	// 1024 bytes: archive/tar reads the two zero blocks of the end marker,
	// and they must not be copied (the Plumb entries come before them).
	held := &holdBack{w: gz, n: 2 * blockSize}
	cr := &countReader{ctx: ctx, r: plain, max: maxUnpacked}
	end, prefix, err := scan(io.TeeReader(cr, held), cr)
	if held.err != nil {
		return fmt.Errorf("ctpack: write: %w", held.err)
	}
	if err != nil {
		return err
	}
	if held.written != end {
		return refuse("the template has an unexpected end (base end %d, copied %d)", end, held.written)
	}

	cw := &countWriter{w: gz, n: end}
	if err := writePlumb(cw, o, prefix); err != nil {
		return fmt.Errorf("ctpack: write: %w", err)
	}
	if pad := (recordSize - cw.n%recordSize) % recordSize; pad > 0 {
		if _, err := cw.Write(make([]byte, pad)); err != nil {
			return fmt.Errorf("ctpack: write: %w", err)
		}
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("ctpack: write: %w", err)
	}
	return ctx.Err()
}

// decompress picks the decoder by magic. Only zstd (every Proxmox system
// template, host A1) and gzip are read.
func decompress(src io.Reader) (io.Reader, func(), error) {
	br := &peekReader{r: src}
	magic, err := br.peek(262)
	if err != nil {
		return nil, nil, fmt.Errorf("ctpack: read: %w", err)
	}
	switch {
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		d, err := zstd.NewReader(br,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(decoderMaxWindow),
			zstd.WithDecoderMaxMemory(decoderMaxMemory),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("ctpack: zstd: %w", err)
		}
		return zstdErr{d}, d.Close, nil
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		z, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, refuse("the template is not a valid gzip file: %v", err)
		}
		z.Multistream(true)
		return z, func() { _ = z.Close() }, nil
	}
	return nil, nil, refuse("the template is %s; Plumb reads only zstd and gzip templates", formatName(magic))
}

func formatName(m []byte) string {
	switch {
	case bytes.HasPrefix(m, []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		return "xz"
	case bytes.HasPrefix(m, []byte("BZh")):
		return "bzip2"
	case bytes.HasPrefix(m, []byte{0x04, 0x22, 0x4d, 0x18}):
		return "lz4"
	case len(m) >= 262 && bytes.Equal(m[257:262], []byte("ustar")):
		return "an uncompressed tar"
	}
	return "in an unknown format"
}

// zstdErr turns decoder errors into refusals of the template.
type zstdErr struct{ d *zstd.Decoder }

func (z zstdErr) Read(p []byte) (int, error) {
	n, err := z.d.Read(p)
	if err != nil && err != io.EOF {
		var e *Error
		if !errors.As(err, &e) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = refuse("the template is not a valid zstd stream: %v", err)
		}
	}
	return n, err
}

// The paths that Pack adds, normalised (no "./", no trailing "/").
var plumbPaths = []string{
	"etc/plumb-agent",
	"etc/plumb-agent/ca.pem",
	"etc/plumb-agent/agent.json",
	"usr/local/sbin/plumb-agent-install",
	"etc/systemd/system/plumb-agent-install.service",
	"etc/systemd/system/plumb-agent.service",
	"etc/systemd/system/multi-user.target.wants/plumb-agent-install.service",
	"etc/systemd/system/multi-user.target.wants/plumb-agent.service",
}

// The parents of the Plumb paths. Each must be a plain directory in the
// base: the last entry of its name decides.
var plumbParents = []string{
	"etc",
	"etc/systemd",
	"etc/systemd/system",
	"etc/systemd/system/multi-user.target.wants",
	"usr",
	"usr/local",
	"usr/local/sbin",
}

// fetchTools: the first-boot script needs one of them (D-082).
var fetchTools = []string{"usr/bin/curl", "usr/bin/wget", "usr/bin/python3"}

func normName(n string) string {
	n = strings.TrimPrefix(n, "./")
	n = path.Clean("/" + n)
	return strings.TrimPrefix(n, "/")
}

// scan walks the base tar and checks the rules of D-230. It returns the
// end of the last entry (the offset of the end marker) and the name prefix
// of the base: "./" when its first entry starts with "./", else "".
func scan(r io.Reader, cr *countReader) (end int64, prefix string, err error) {
	tr := tar.NewReader(r)
	watch := map[string]byte{} // name → typeflag of its last entry
	for _, p := range plumbPaths {
		watch[p] = 0
	}
	for _, p := range plumbParents {
		watch[p] = 0
	}
	for _, p := range fetchTools {
		watch[p] = 0
	}
	seen := map[string]bool{}
	entries := 0
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return 0, "", readErr(nerr)
		}
		entries++
		if entries == 1 && (strings.HasPrefix(hdr.Name, "./") || hdr.Name == ".") {
			prefix = "./"
		}
		if entries > maxEntries {
			return 0, "", refuse("the template has more than %d entries", maxEntries)
		}
		switch hdr.Typeflag {
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return 0, "", refuse("the template has device nodes or fifos (%s); an unprivileged container cannot make them", hdr.Name)
		case tar.TypeGNUSparse:
			return 0, "", refuse("the template has sparse files (%s)", hdr.Name)
		}
		if isPAXSparse(hdr) {
			return 0, "", refuse("the template has sparse files (%s)", hdr.Name)
		}
		n := normName(hdr.Name)
		if _, ok := watch[n]; ok {
			watch[n] = hdr.Typeflag
			seen[n] = true
		}
		// The data of the entry starts here; archive/tar skips it on the
		// next call. Header-only types have no data blocks.
		size := hdr.Size
		if headerOnly(hdr.Typeflag) {
			size = 0
		}
		end = cr.n + (size+blockSize-1)/blockSize*blockSize
	}
	// archive/tar ends at two zero blocks; a stream that just stops at a
	// block boundary also gives io.EOF. Only a real end marker counts.
	if entries == 0 {
		return 0, "", refuse("the template has no entries")
	}
	if cr.n != end+2*blockSize {
		return 0, "", refuse("the template is truncated (no end marker)")
	}
	for _, p := range plumbPaths {
		if seen[p] {
			return 0, "", refuse("the template already has /%s", p)
		}
	}
	for _, p := range plumbParents {
		if !seen[p] {
			return 0, "", refuse("the template has no directory /%s", p)
		}
		if watch[p] != tar.TypeDir {
			return 0, "", refuse("/%s in the template is not a directory", p)
		}
	}
	ok := false
	for _, p := range fetchTools {
		ok = ok || seen[p]
	}
	if !ok {
		return 0, "", refuse("the template has none of /usr/bin/curl, /usr/bin/wget, /usr/bin/python3; the first boot cannot fetch the agent")
	}
	return end, prefix, nil
}

func headerOnly(f byte) bool {
	switch f {
	case tar.TypeLink, tar.TypeSymlink, tar.TypeChar, tar.TypeBlock, tar.TypeDir, tar.TypeFifo:
		return true
	}
	return false
}

func isPAXSparse(h *tar.Header) bool {
	for k := range h.PAXRecords {
		if strings.HasPrefix(k, "GNU.sparse.") {
			return true
		}
	}
	return false
}

func readErr(err error) error {
	var e *Error
	switch {
	case errors.As(err, &e), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, io.ErrUnexpectedEOF):
		return refuse("the template is truncated")
	}
	return refuse("the template is not a valid tar archive: %v", err)
}

// countReader counts the decoder output, stops at max and at ctx.
type countReader struct {
	ctx context.Context
	r   io.Reader
	n   int64
	max int64
}

func (c *countReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	if c.n >= c.max {
		// One more byte proves the stream is larger than the limit.
		var b [1]byte
		k, err := io.ReadFull(c.r, b[:])
		if k > 0 {
			return 0, refuse("the unpacked template is larger than %s", sizeText(c.max))
		}
		return 0, err
	}
	if rest := c.max - c.n; int64(len(p)) > rest {
		p = p[:rest]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// peekReader lets decompress look at the first bytes.
type peekReader struct {
	r   io.Reader
	buf []byte
}

func (p *peekReader) peek(n int) ([]byte, error) {
	p.buf = make([]byte, n)
	k, err := io.ReadFull(p.r, p.buf)
	p.buf = p.buf[:k]
	if err == io.ErrUnexpectedEOF || (err == io.EOF && k == 0) {
		err = nil
	}
	return p.buf, err
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}

// holdBack passes everything to w except the last n bytes it got.
type holdBack struct {
	w       io.Writer
	n       int
	buf     []byte
	written int64
	err     error
}

func (h *holdBack) Write(p []byte) (int, error) {
	if h.err != nil {
		return 0, h.err
	}
	h.buf = append(h.buf, p...)
	if out := len(h.buf) - h.n; out > 0 {
		k, err := h.w.Write(h.buf[:out])
		h.written += int64(k)
		if err != nil {
			h.err = err
			return 0, err
		}
		h.buf = append(h.buf[:0], h.buf[out:]...)
	}
	return len(p), nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func sizeText(n int64) string {
	if n >= 1<<30 && n%(1<<30) == 0 {
		return fmt.Sprintf("%d GiB", n>>30)
	}
	return fmt.Sprintf("%d bytes", n)
}
