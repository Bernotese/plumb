package proxmox

// FetchImage for an lxc Image (T-038 §3, D-224, D-229, D-230, D-236,
// D-237): the controller downloads the Proxmox system template, checks the
// pinned SHA-512 and repacks it (ctpack), and uploads the result as vztmpl
// to the image storage. A file of the same name is reused.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/proxmox/ctpack"
	"github.com/Bernotese/plumb/internal/wire"
)

// Limits of D-236. Tests lower them.
var (
	maxDownload  int64 = 1 << 30
	minFree      int64 = 2 << 30
	checkEvery   int64 = 64 << 20
	fetchTimeout       = 15 * time.Minute
)

// CTSource is what the controller gives the driver for container
// templates: a private temp directory on the disk of the database, the
// Plumb CA and the agent URL that a container reaches (D-082).
type CTSource struct {
	TempDir  string // created 0700; the start removes old ct-* files in it
	CA       []byte
	AgentURL string
}

// ctState holds the CTSource and the image spec per template file, for
// the one retry of a reused file (review S10).
type ctState struct {
	mu    sync.Mutex
	src   *CTSource
	specs map[driver.ImageRef]driver.ImageSpec
	// dialOK decides which address a download may dial (D-237). Tests
	// replace it to reach their loopback server.
	dialOK func(net.IP) bool
	// free returns the free bytes of a directory (statfs).
	free func(string) (int64, error)
}

// SetCTSource enables lxc Images. serve calls it once the link CA exists.
// It makes the temp directory and removes old temp files of a fetch that
// a stop cut off.
func (d *Driver) SetCTSource(s CTSource) error {
	if _, err := ctpack.Name("check", ctpack.Options{SHA512: strings.Repeat("0", 128), CA: s.CA, AgentURL: s.AgentURL}); err != nil {
		return fmt.Errorf("proxmox: container templates: %w", err)
	}
	if err := os.MkdirAll(s.TempDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.TempDir, 0o700); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(s.TempDir, "ct-*"))
	for _, f := range old {
		_ = os.Remove(f)
	}
	d.ct.mu.Lock()
	defer d.ct.mu.Unlock()
	src := s
	src.CA = bytes.Clone(s.CA)
	d.ct.src = &src
	return nil
}

func (d *Driver) ctSource() (CTSource, error) {
	d.ct.mu.Lock()
	defer d.ct.mu.Unlock()
	if d.ct.src == nil {
		return CTSource{}, errors.New("proxmox: container templates are not set up in this controller")
	}
	return *d.ct.src, nil
}

var sha512Hex = regexp.MustCompile(`^[0-9a-f]{128}$`)

// ctImageName makes an Image name fit the file name of ctpack: lower
// case, digits and "-" (an Image name may hold dots).
func ctImageName(name string) string {
	return strings.Trim(safeName.ReplaceAllString(strings.ReplaceAll(strings.ToLower(name), ".", "-"), "-"), "-")
}

// TemplateRef is the ref of the template file that FetchImage makes for
// an lxc Image, without a host call (T-085).
func (d *Driver) TemplateRef(spec driver.ImageSpec) (driver.ImageRef, error) {
	if spec.Kind != wire.KindLXC {
		return "", driver.ErrNotImplemented
	}
	src, err := d.ctSource()
	if err != nil {
		return "", err
	}
	name, err := ctpack.Name(ctImageName(spec.Name), ctpack.Options{SHA512: spec.SHA512, CA: src.CA, AgentURL: src.AgentURL})
	if err != nil {
		return "", fmt.Errorf("proxmox: %w", err)
	}
	return driver.ImageRef(d.cfg.ImageStorage + ":vztmpl/" + name), nil
}

// templateFiles lists the container template files of an Image and how
// many pool containers name each one in their description.
func (d *Driver) templateFiles(ctx context.Context, image string) ([]driver.ImageObject, error) {
	var files []storageVolume
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.ImageStorage)+"/content",
		url.Values{"content": {"vztmpl"}}, &files); err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(d.cfg.ImageStorage) + `:vztmpl/plumb-ct-` + regexp.QuoteMeta(ctImageName(image)) + `-[0-9a-f]{12}\.tar\.gz$`)
	var out []driver.ImageObject
	for _, f := range files {
		if re.MatchString(f.VolID) {
			out = append(out, driver.ImageObject{Kind: driver.ImageTemplateFile, Image: image, Ref: driver.ImageRef(f.VolID), Name: f.VolID, SizeBytes: f.Size})
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	gs, err := d.guests(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if g.Type != typeCT {
			continue
		}
		cfg, err := d.ctConfig(ctx, g.VMID, true)
		if err != nil {
			if errors.Is(notFound(err), driver.ErrNotFound) {
				continue // deleted meanwhile
			}
			return nil, err
		}
		for _, line := range strings.Split(cfg["description"], "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), templateMarker)
			if !ok {
				continue
			}
			for i := range out {
				if out[i].Name == v {
					out[i].Containers++
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b driver.ImageObject) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// fetchCT returns the template file of an lxc Image; force skips the
// reuse of an existing file (the retry of review S10).
func (d *Driver) fetchCT(ctx context.Context, spec driver.ImageSpec, force bool) (driver.ImageRef, error) {
	if !sha512Hex.MatchString(spec.SHA512) || spec.SHA256 != "" {
		return "", errors.New("proxmox: an lxc image needs a SHA-512 and no SHA-256 (D-224)")
	}
	u, err := url.Parse(spec.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("proxmox: the template URL must be http:// or https:// without user info (D-237)")
	}
	if err := d.TemplateContentError(); err != nil {
		return "", err
	}
	src, err := d.ctSource()
	if err != nil {
		return "", err
	}
	opts := ctpack.Options{SHA512: spec.SHA512, CA: src.CA, AgentURL: src.AgentURL}
	name, err := ctpack.Name(ctImageName(spec.Name), opts)
	if err != nil {
		return "", fmt.Errorf("proxmox: %w", err)
	}
	ref := driver.ImageRef(d.cfg.ImageStorage + ":vztmpl/" + name)
	lock := d.imageLock(name)
	lock.Lock()
	defer lock.Unlock()
	d.ct.mu.Lock()
	if d.ct.specs == nil {
		d.ct.specs = map[driver.ImageRef]driver.ImageSpec{}
	}
	d.ct.specs[ref] = spec
	d.ct.mu.Unlock()
	if !force {
		have, err := d.hasTemplateFile(ctx, ref)
		if err != nil {
			return "", err
		}
		if have {
			return ref, nil // the name holds the pin, the CA and the URL (D-230)
		}
	}
	if err := d.buildAndUpload(ctx, src, spec.URL, opts, name); err != nil {
		return "", err
	}
	return ref, nil
}

// hasTemplateFile lists the vztmpl files of the image storage (host B4).
func (d *Driver) hasTemplateFile(ctx context.Context, ref driver.ImageRef) (bool, error) {
	var list []struct {
		VolID string `json:"volid"`
	}
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.ImageStorage)+"/content", url.Values{"content": {"vztmpl"}}, &list); err != nil {
		return false, err
	}
	for _, v := range list {
		if v.VolID == string(ref) {
			return true, nil
		}
	}
	return false, nil
}

// fetchErr is the one text of a stopped fetch (D-236): it names the rule
// and says that nothing was kept.
func fetchErr(format string, a ...any) error {
	return fmt.Errorf("template download stopped: "+format+"; nothing was kept", a...)
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30)) }

// reserve stops a fetch when the disk of the database would keep less
// than minFree (D-236).
func (d *Driver) reserve(dir string) error {
	free := d.ct.free
	if free == nil {
		free = statfsFree
	}
	n, err := free(dir)
	if err != nil {
		return fmt.Errorf("template download stopped: cannot read the free space of %s: %v", dir, err)
	}
	if n < minFree {
		return fetchErr("the controller disk would have less than %s free (now %s)", gib(minFree), gib(n))
	}
	return nil
}

func statfsFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// buildAndUpload downloads, repacks and uploads one template. Both temp
// files go on every path.
func (d *Driver) buildAndUpload(ctx context.Context, src CTSource, rawURL string, opts ctpack.Options, name string) error {
	if err := d.reserve(src.TempDir); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	dl, err := tempFile(src.TempDir, "ct-*.download")
	if err != nil {
		return err
	}
	defer removeFile(dl)
	if err := d.download(ctx, rawURL, dl, src.TempDir); err != nil {
		return err
	}
	out, err := tempFile(src.TempDir, "ct-*.tar.gz")
	if err != nil {
		return err
	}
	defer removeFile(out)
	sum := sha256.New()
	w := &guardWriter{w: io.MultiWriter(out, sum), check: func() error { return d.reserve(src.TempDir) }}
	if _, err := dl.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := ctpack.Pack(ctx, dl, w, opts); err != nil {
		var pe *ctpack.Error
		switch {
		case w.err != nil:
			return w.err
		case errors.Is(err, ctpack.ErrPin):
			return fmt.Errorf("proxmox: %w; check the sha512 of the Image against the signed Proxmox index (D-224)", err)
		case errors.As(err, &pe):
			return fmt.Errorf("proxmox: the template is refused: %w", err)
		}
		return fmt.Errorf("proxmox: repack: %w", err)
	}
	removeFile(dl) // the base is not needed for the upload
	size := w.n
	if err := d.uploadSpace(ctx, size); err != nil {
		return err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return d.upload(ctx, out, size, hex.EncodeToString(sum.Sum(nil)), name)
}

func tempFile(dir, pattern string) (*os.File, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		removeFile(f)
		return nil, err
	}
	return f, nil
}

func removeFile(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// guardWriter counts and checks the free space after every checkEvery
// bytes. Its first error stops every later write.
type guardWriter struct {
	w     io.Writer
	n     int64
	next  int64
	check func() error
	err   error
}

func (g *guardWriter) Write(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.w.Write(p)
	g.n += int64(n)
	if err != nil {
		return n, err
	}
	if g.n >= g.next {
		g.next = g.n + checkEvery
		if g.err = g.check(); g.err != nil {
			return n, g.err
		}
	}
	return n, nil
}

// dialCheck refuses loopback, link-local (169.254.169.254 too),
// unspecified and multicast addresses after DNS; private LAN addresses are
// allowed (D-237).
func dialCheck(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast())
}

// downloadClient follows D-237: no proxy, no redirect, the dialled address
// is checked, no cookie, no auth.
func (d *Driver) downloadClient() *http.Client {
	ok := d.ct.dialOK
	if ok == nil {
		ok = dialCheck
	}
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !ok(ip) {
				return fmt.Errorf("the address %s is not allowed for a template download (loopback, link-local, multicast; D-237)", host)
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ForceAttemptHTTP2:     true,
			DisableCompression:    true, // the bytes on disk are the bytes of the pin
		},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return fmt.Errorf("the template URL redirects to %s; put the final URL into the Image (D-237)", req.URL.Host)
		},
	}
}

// download writes the body of url into f with the limits of D-236. An
// error shows the status and the size, never the body.
func (d *Driver) download(ctx context.Context, rawURL string, f *os.File, dir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "plumb")
	resp, err := d.downloadClient().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("template download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("template download failed: HTTP %d (%d bytes)", resp.StatusCode, resp.ContentLength)
	}
	if resp.ContentLength > maxDownload {
		return fetchErr("larger than %s (Content-Length %d)", gib(maxDownload), resp.ContentLength)
	}
	limit := maxDownload
	if resp.ContentLength >= 0 {
		limit = resp.ContentLength
	}
	w := &guardWriter{w: f, check: func() error { return d.reserve(dir) }}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	switch {
	case w.err != nil:
		return w.err
	case err != nil:
		return fmt.Errorf("template download failed after %d bytes: %v", n, err)
	case n > limit && resp.ContentLength >= 0:
		return fetchErr("longer than its Content-Length (%d)", resp.ContentLength)
	case n > limit:
		return fetchErr("larger than %s", gib(maxDownload))
	case resp.ContentLength >= 0 && n < resp.ContentLength:
		return fmt.Errorf("template download failed: %d of %d bytes", n, resp.ContentLength)
	}
	return nil
}

// uploadSpace: pveproxy buffers the upload in /var/tmp and copies it to
// the image storage, both on the host root fs (host B1): 2 × the size.
func (d *Driver) uploadSpace(ctx context.Context, size int64) error {
	var st struct {
		Avail int64 `json:"avail"`
	}
	if err := d.c.Get(ctx, d.node()+"/storage/"+url.PathEscape(d.cfg.ImageStorage)+"/status", nil, &st); err != nil {
		return err
	}
	if st.Avail < 2*size {
		return fmt.Errorf("proxmox: storage %s has %s free; the upload of the template (%s) needs %s", d.cfg.ImageStorage, gib(st.Avail), gib(size), gib(2*size))
	}
	return nil
}

// upload sends the file as multipart with the field order of the web UI
// (host B1: content, checksum-algorithm, checksum, file) and a
// Content-Length (a chunked body gives 501). Proxmox checks the SHA-256
// of the copy. An upload over an existing name overwrites it (B2).
func (d *Driver) upload(ctx context.Context, f *os.File, size int64, sum, name string) error {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	if err := mw.SetBoundary("plumb-" + randHex(12)); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"content", "vztmpl"}, {"checksum-algorithm", "sha256"}, {"checksum", sum}} {
		if err := mw.WriteField(kv[0], kv[1]); err != nil {
			return err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="filename"; filename="%s"`, name))
	h.Set("Content-Type", "application/octet-stream")
	if _, err := mw.CreatePart(h); err != nil {
		return err
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n"
	body := io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(f, size), strings.NewReader(tail))
	path := d.node() + "/storage/" + url.PathEscape(d.cfg.ImageStorage) + "/upload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.c.base+path, body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(head.Len()) + size + int64(len(tail))
	req.Header.Set("Authorization", d.c.auth)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	hc := *d.c.http
	hc.Timeout = fetchTimeout
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox: upload of the template: %w", err)
	}
	defer resp.Body.Close()
	data, err := d.c.answer(resp, http.MethodPost, path)
	if err != nil {
		return fmt.Errorf("proxmox: upload of the template: %w", err)
	}
	var upid string
	if json.Unmarshal(data, &upid) != nil || !strings.HasPrefix(upid, "UPID:") {
		return fmt.Errorf("proxmox: upload of the template: no task id in the answer")
	}
	if err := d.c.WaitTask(ctx, d.cfg.Node, upid); err != nil {
		return fmt.Errorf("proxmox: upload of the template: %w", err)
	}
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// archiveError says that Proxmox could not unpack a template file (review
// S10): a reused file may be damaged. Proxmox names tar and the
// decompressor in the error of the create task.
func archiveError(err error) bool {
	return reArchive.MatchString(err.Error())
}

var reArchive = regexp.MustCompile(`(?i)\btar\b|zstd|gzip|unexpected end of (file|archive)`)
