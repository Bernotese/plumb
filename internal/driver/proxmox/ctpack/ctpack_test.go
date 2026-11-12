package ctpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

const testCA = "-----BEGIN CERTIFICATE-----\nMIIBplumbCAline1\nline2+/=\n-----END CERTIFICATE-----\n"

func opts(base []byte) Options {
	s := sha512.Sum512(base)
	return Options{SHA512: hex.EncodeToString(s[:]), CA: []byte(testCA), AgentURL: "https://10.0.0.5:8444"}
}

// ent is one entry of a test base.
type ent struct {
	name   string
	flag   byte
	mode   int64
	body   string
	link   string
	uid    int
	format tar.Format
}

func dir(n string) ent  { return ent{name: n, flag: tar.TypeDir, mode: 0o755} }
func file(n string) ent { return ent{name: n, flag: tar.TypeReg, mode: 0o644, body: "x"} }

// stdBase is a small template with every feature of the real ones (host
// A3): GNU headers, a GNU long name, a hard link, setuid, owners other
// than root, a PAX header.
func stdBase() []ent {
	long := "./usr/share/doc/" + strings.Repeat("very-long-directory-name-", 6) + "/changelog.gz"
	return []ent{
		dir("./"),
		dir("./etc/"),
		dir("./etc/systemd/"),
		dir("./etc/systemd/system/"),
		dir("./etc/systemd/system/multi-user.target.wants/"),
		{name: "./etc/systemd/system/multi-user.target.wants/ssh.service", flag: tar.TypeSymlink, mode: 0o777, link: "/usr/lib/systemd/system/ssh.service"},
		dir("./usr/"),
		dir("./usr/bin/"),
		{name: "./usr/bin/wget", flag: tar.TypeReg, mode: 0o755, body: strings.Repeat("w", 1500)},
		{name: "./usr/bin/passwd", flag: tar.TypeReg, mode: 0o4755, body: "suid"},
		{name: "./usr/bin/wget-link", flag: tar.TypeLink, link: "./usr/bin/wget"},
		dir("./usr/local/"),
		dir("./usr/local/sbin/"),
		{name: "./var/cache/man/index.db", flag: tar.TypeReg, mode: 0o644, body: "m", uid: 6},
		{name: long, flag: tar.TypeReg, mode: 0o644, body: "long"},
		{name: "./opt/pax-file", flag: tar.TypeReg, mode: 0o644, body: "pax", format: tar.FormatPAX},
	}
}

// mkTar writes the entries like GNU tar: GNU headers, the end marker and
// padding to a whole 10 240-byte record. It returns the tar and the end
// of its last entry.
func mkTar(t testing.TB, es []ent) ([]byte, int) {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range es {
		f := e.format
		if f == 0 {
			f = tar.FormatGNU
		}
		h := &tar.Header{Typeflag: e.flag, Name: e.name, Linkname: e.link, Mode: e.mode, Uid: e.uid, Gid: e.uid,
			ModTime: time.Unix(1700000000, 0), Format: f}
		if e.flag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if f == tar.FormatPAX {
			h.PAXRecords = map[string]string{"comment": "plumb"}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, e.body)
	}
	tw.Flush()
	end := b.Len()
	tw.Close()
	if pad := (recordSize - b.Len()%recordSize) % recordSize; pad > 0 {
		b.Write(make([]byte, pad))
	}
	return b.Bytes(), end
}

func zst(t testing.TB, p []byte) []byte {
	t.Helper()
	e, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	return e.EncodeAll(p, nil)
}

func gz(t testing.TB, p []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(p)
	w.Close()
	return b.Bytes()
}

func pack(t testing.TB, base []byte, o Options) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	err := Pack(context.Background(), bytes.NewReader(base), &out, o)
	return out.Bytes(), err
}

func gunzip(t testing.TB, p []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPack(t *testing.T) {
	plain, end := mkTar(t, stdBase())
	for _, c := range []struct {
		name string
		base []byte
	}{{"zstd", zst(t, plain)}, {"gzip", gz(t, plain)}} {
		t.Run(c.name, func(t *testing.T) {
			o := opts(c.base)
			out, err := pack(t, c.base, o)
			if err != nil {
				t.Fatal(err)
			}
			got := gunzip(t, out)
			// D-230: the base blocks are byte-equal up to the end of the
			// last entry.
			if !bytes.Equal(got[:end], plain[:end]) {
				t.Fatal("the base prefix changed")
			}
			if len(got)%recordSize != 0 {
				t.Errorf("output %d bytes, not whole 10 240-byte records", len(got))
			}
			// Same inputs, same bytes.
			again, _ := pack(t, c.base, o)
			if !bytes.Equal(out, again) {
				t.Error("two packs differ")
			}
			checkEntries(t, got, len(stdBase()), o)
			gnuList(t, got)
		})
	}
}

// checkEntries reads the output with archive/tar: the base entries, then
// the Plumb entries as USTAR with root owners and the fixed mtime.
func checkEntries(t *testing.T, got []byte, nBase int, o Options) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(got))
	var hs []*tar.Header
	bodies := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		bodies[h.Name] = string(b)
		hs = append(hs, h)
	}
	if len(hs) != nBase+len(plumbPaths) {
		t.Fatalf("%d entries, want %d", len(hs), nBase+len(plumbPaths))
	}
	want := map[string]struct {
		flag byte
		mode int64
	}{
		"./etc/plumb-agent/":                                                       {tar.TypeDir, 0o755},
		"./etc/plumb-agent/ca.pem":                                                 {tar.TypeReg, 0o644},
		"./etc/plumb-agent/agent.json":                                             {tar.TypeReg, 0o644},
		"./usr/local/sbin/plumb-agent-install":                                     {tar.TypeReg, 0o755},
		"./etc/systemd/system/plumb-agent-install.service":                         {tar.TypeReg, 0o644},
		"./etc/systemd/system/plumb-agent.service":                                 {tar.TypeReg, 0o644},
		"./etc/systemd/system/multi-user.target.wants/plumb-agent-install.service": {tar.TypeSymlink, 0o777},
		"./etc/systemd/system/multi-user.target.wants/plumb-agent.service":         {tar.TypeSymlink, 0o777},
	}
	for _, h := range hs[nBase:] {
		w, ok := want[h.Name]
		if !ok {
			t.Errorf("unexpected entry %q", h.Name)
			continue
		}
		if h.Typeflag != w.flag || h.Mode != w.mode || h.Uid != 0 || h.Gid != 0 || !h.ModTime.Equal(mtime) || h.Format != tar.FormatUSTAR {
			t.Errorf("%s: %+v", h.Name, h)
		}
	}
	if bodies["./etc/plumb-agent/ca.pem"] != testCA {
		t.Errorf("ca.pem = %q", bodies["./etc/plumb-agent/ca.pem"])
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(bodies["./etc/plumb-agent/agent.json"]), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["url"] != o.AgentURL || cfg["ca"] != testCA || cfg["token_env"] != "PLUMB_ENROLL_TOKEN" || len(cfg) != 3 {
		t.Errorf("agent.json = %v", cfg)
	}
	if bodies["./usr/local/sbin/plumb-agent-install"] != FirstBootScript(o.AgentURL) {
		t.Error("first-boot script")
	}
	unit := bodies["./etc/systemd/system/plumb-agent-install.service"]
	if !strings.Contains(unit, "Type=oneshot") || !strings.Contains(unit, "ExecStart=/usr/local/sbin/plumb-agent-install") || strings.Contains(unit, "ConditionPathExists") {
		t.Errorf("install unit:\n%s", unit)
	}
	if bodies["./etc/systemd/system/plumb-agent.service"] != AgentUnit {
		t.Error("agent unit")
	}
	if strings.Contains(strings.Join(func() []string {
		var n []string
		for _, h := range hs {
			n = append(n, h.Name)
		}
		return n
	}(), "\n"), "usr/local/bin/plumb-agent") {
		t.Error("the template holds an agent binary")
	}
}

// gnuList lists the output with GNU tar, as Proxmox extracts it.
func gnuList(t *testing.T, got []byte) {
	t.Helper()
	v, err := exec.Command("tar", "--version").Output()
	if err != nil || !strings.Contains(string(v), "GNU tar") {
		if os.Getenv("CI") != "" {
			t.Fatal("CI needs GNU tar")
		}
		t.Log("no GNU tar; listing skipped")
		return
	}
	cmd := exec.Command("tar", "-tvf", "-")
	cmd.Stdin = bytes.NewReader(got)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("GNU tar: %v\n%s", err, out)
	}
	for _, want := range []string{
		"-rwsr-xr-x", // setuid kept
		"./usr/bin/wget-link link to ./usr/bin/wget",
		"very-long-directory-name-very",
		"drwxr-xr-x root/root         0 2026-01-01 00:00 ./etc/plumb-agent/",
		"-rwxr-xr-x root/root",
		"./usr/local/sbin/plumb-agent-install",
		"./etc/systemd/system/multi-user.target.wants/plumb-agent.service -> /etc/systemd/system/plumb-agent.service",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("GNU tar list misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "warning") || strings.Contains(string(out), "A lone zero block") {
		t.Errorf("GNU tar warns:\n%s", out)
	}
}

// A base without "./" gets Plumb names without it.
func TestPackNoDotPrefix(t *testing.T) {
	var es []ent
	for _, e := range stdBase() {
		if e.name == "./" {
			continue
		}
		e.name = strings.TrimPrefix(e.name, "./")
		es = append(es, e)
	}
	plain, _ := mkTar(t, es)
	base := zst(t, plain)
	out, err := pack(t, base, opts(base))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(gunzip(t, out), []byte("etc/plumb-agent/agent.json\x00")) || bytes.Contains(gunzip(t, out), []byte("./etc/plumb-agent")) {
		t.Error("prefix of the Plumb entries")
	}
}

func refuseErr(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v (%T), want a refusal with %q", err, err, want)
	}
	if !strings.Contains(e.Error(), want) {
		t.Fatalf("err = %q, want %q", e, want)
	}
}

func with(es []ent, add ...ent) []ent { return append(append([]ent{}, es...), add...) }

func without(es []ent, name string) []ent {
	var out []ent
	for _, e := range es {
		if e.name != name {
			out = append(out, e)
		}
	}
	return out
}

func TestRefuse(t *testing.T) {
	std := stdBase()
	cases := []struct {
		name string
		es   []ent
		want string
	}{
		{"plumb dir", with(std, dir("./etc/plumb-agent/")), "already has /etc/plumb-agent"},
		{"plumb file without ./", with(std, file("etc/systemd/system/plumb-agent.service")), "already has /etc/systemd/system/plumb-agent.service"},
		{"plumb path as absolute name", with(std, file("/usr/local/sbin/plumb-agent-install")), "already has /usr/local/sbin/plumb-agent-install"},
		{"plumb path not clean", with(std, file("./etc/systemd//system/../system/plumb-agent.service")), "already has"},
		{"hard link named like a plumb path", with(std, ent{name: "./etc/plumb-agent/agent.json", flag: tar.TypeLink, link: "./usr/bin/wget"}), "already has /etc/plumb-agent/agent.json"},
		{"symlink named like a plumb path", with(std, ent{name: "./etc/systemd/system/multi-user.target.wants/plumb-agent.service", flag: tar.TypeSymlink, link: "/dev/null"}), "already has"},
		{"parent: dir then symlink", with(std, ent{name: "./etc/systemd/system", flag: tar.TypeSymlink, link: "/tmp"}), "/etc/systemd/system in the template is not a directory"},
		{"parent: dir then file", with(std, file("./usr/local/sbin")), "/usr/local/sbin in the template is not a directory"},
		{"parent: hard link", with(std, ent{name: "etc/systemd", flag: tar.TypeLink, link: "./usr/bin/wget"}), "/etc/systemd in the template is not a directory"},
		{"parent missing", without(std, "./usr/local/sbin/"), "has no directory /usr/local/sbin"},
		{"char device", with(std, ent{name: "./dev/null", flag: tar.TypeChar, mode: 0o666}), "device nodes"},
		{"block device", with(std, ent{name: "./dev/sda", flag: tar.TypeBlock, mode: 0o660}), "device nodes"},
		{"fifo", with(std, ent{name: "./run/initctl", flag: tar.TypeFifo, mode: 0o600}), "device nodes or fifos"},
		{"no fetch tool", without(without(std, "./usr/bin/wget"), "./usr/bin/wget-link"), "none of /usr/bin/curl"},
		{"empty", nil, "no entries"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plain, _ := mkTar(t, c.es)
			base := zst(t, plain)
			_, err := pack(t, base, opts(base))
			refuseErr(t, err, c.want)
		})
	}
	// A parent that is a dir again after a symlink is fine: the last
	// entry decides.
	plain, _ := mkTar(t, with(std, ent{name: "./etc/systemd/system", flag: tar.TypeSymlink, link: "/tmp"}, dir("./etc/systemd/system/")))
	base := zst(t, plain)
	if _, err := pack(t, base, opts(base)); err != nil {
		t.Errorf("last entry a dir: %v", err)
	}
	// curl or python3 alone is enough.
	for _, tool := range []string{"./usr/bin/curl", "./usr/bin/python3"} {
		plain, _ := mkTar(t, with(without(without(std, "./usr/bin/wget"), "./usr/bin/wget-link"), file(tool)))
		base := zst(t, plain)
		if _, err := pack(t, base, opts(base)); err != nil {
			t.Errorf("%s: %v", tool, err)
		}
	}
}

func TestRefuseStream(t *testing.T) {
	plain, end := mkTar(t, stdBase())
	cases := []struct {
		name string
		base []byte
		want string
	}{
		{"cut in data", zst(t, plain[:end-700]), "truncated"},
		{"cut at a block, no end marker", zst(t, plain[:end]), "truncated (no end marker)"},
		{"one zero block", zst(t, plain[:end+512]), "truncated"},
		{"cut zstd", zst(t, plain)[:200], "zstd"},
		{"cut gzip", gz(t, plain)[:200], "truncated"},
		{"xz", append([]byte{0xfd, '7', 'z', 'X', 'Z', 0}, make([]byte, 600)...), "is xz"},
		{"bzip2", []byte("BZh91AY&SY" + strings.Repeat("x", 300)), "is bzip2"},
		{"plain tar", plain, "an uncompressed tar"},
		{"garbage", []byte("hello"), "unknown format"},
		{"not tar inside", zst(t, bytes.Repeat([]byte("plumb"), 400)), "not a valid tar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pack(t, c.base, opts(c.base))
			refuseErr(t, err, c.want)
		})
	}
}

func TestPin(t *testing.T) {
	plain, _ := mkTar(t, stdBase())
	base := zst(t, plain)
	o := opts(base)
	o.SHA512 = strings.Repeat("0", 128)
	var out bytes.Buffer
	if err := Pack(context.Background(), bytes.NewReader(base), &out, o); !errors.Is(err, ErrPin) {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Error("output before the pin check")
	}
	// Nothing is parsed before the check: a hostile file with a wrong
	// pin gives ErrPin, not a parse error.
	if _, err := pack(t, []byte("hello"), o); !errors.Is(err, ErrPin) {
		t.Errorf("err = %v", err)
	}
}

func TestOptions(t *testing.T) {
	plain, _ := mkTar(t, stdBase())
	base := zst(t, plain)
	good := opts(base)
	for name, mut := range map[string]func(*Options){
		"pin upper case":  func(o *Options) { o.SHA512 = strings.ToUpper(o.SHA512) },
		"pin short":       func(o *Options) { o.SHA512 = o.SHA512[:64] },
		"url http":        func(o *Options) { o.AgentURL = "http://10.0.0.5:8444" },
		"url quote":       func(o *Options) { o.AgentURL = "https://x'; rm -rf /; '" },
		"url path":        func(o *Options) { o.AgentURL = "https://x/path" },
		"url newline":     func(o *Options) { o.AgentURL = "https://x\nevil" },
		"ca empty":        func(o *Options) { o.CA = nil },
		"ca with a quote": func(o *Options) { o.CA = []byte(testCA + "\"}\n") },
		"ca with a key": func(o *Options) {
			o.CA = []byte(testCA + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")
		},
	} {
		o := good
		mut(&o)
		if _, err := pack(t, base, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := Name("debian", o); err == nil {
			t.Errorf("%s: Name accepted", name)
		}
	}
	// CRLF in the CA is accepted and written as LF.
	o := good
	o.CA = []byte(strings.ReplaceAll(testCA, "\n", "\r\n"))
	out, err := pack(t, base, o)
	if err != nil || !bytes.Contains(gunzip(t, out), []byte(testCA)) {
		t.Errorf("CRLF CA: %v", err)
	}
}

func TestName(t *testing.T) {
	o := Options{SHA512: strings.Repeat("a", 128), CA: []byte(testCA), AgentURL: "https://10.0.0.5:8444"}
	n, err := Name("debian-13", o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n, "plumb-ct-debian-13-") || !strings.HasSuffix(n, ".tar.gz") || len(n) != len("plumb-ct-debian-13-")+12+len(".tar.gz") {
		t.Errorf("name %q", n)
	}
	if again, _ := Name("debian-13", o); again != n {
		t.Error("not stable")
	}
	seen := map[string]bool{n: true}
	for _, mut := range []func(*Options){
		func(o *Options) { o.SHA512 = strings.Repeat("b", 128) },
		func(o *Options) { o.CA = []byte(strings.Replace(testCA, "line1", "line9", 1)) },
		func(o *Options) { o.AgentURL = "https://10.0.0.6:8444" },
	} {
		m := o
		mut(&m)
		x, err := Name("debian-13", m)
		if err != nil || seen[x] {
			t.Errorf("name %q %v: not new", x, err)
		}
		seen[x] = true
	}
	for _, bad := range []string{"", "Debian", "a/b", "-a", "a b", "../x"} {
		if _, err := Name(bad, o); err == nil {
			t.Errorf("image %q accepted", bad)
		}
	}
}

func TestLimits(t *testing.T) {
	plain, _ := mkTar(t, stdBase())
	base := zst(t, plain)

	old := maxEntries
	maxEntries = len(stdBase()) - 1
	_, err := pack(t, base, opts(base))
	maxEntries = old
	refuseErr(t, err, "more than 15 entries")

	oldU := maxUnpacked
	maxUnpacked = int64(len(plain)) - recordSize
	_, err = pack(t, base, opts(base))
	maxUnpacked = oldU
	refuseErr(t, err, "unpacked template is larger than")

	// Exactly at the limit is fine (the padding is read too).
	maxUnpacked = int64(len(plain))
	_, err = pack(t, base, opts(base))
	maxUnpacked = oldU
	if err != nil {
		t.Errorf("at the limit: %v", err)
	}
}

// A zstd bomb: a small file that unpacks to more than 4 GiB. The decoder
// output is counted, not the tar header size.
func TestZstdBomb(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 4 GiB through the decoder")
	}
	var plain bytes.Buffer
	tw := tar.NewWriter(&plain)
	tw.WriteHeader(&tar.Header{Name: "./big", Typeflag: tar.TypeReg, Mode: 0o644, Size: MaxUnpacked + 1<<20, Format: tar.FormatGNU})
	var comp bytes.Buffer
	enc, _ := zstd.NewWriter(&comp, zstd.WithEncoderLevel(zstd.SpeedFastest))
	enc.Write(plain.Bytes())
	zero := make([]byte, 1<<20)
	for i := 0; i < MaxUnpacked>>20+2; i++ {
		enc.Write(zero)
	}
	enc.Close()
	base := comp.Bytes()
	if len(base) > 1<<20 {
		t.Fatalf("bomb is %d bytes", len(base))
	}
	_, err := pack(t, base, opts(base))
	refuseErr(t, err, "larger than 4 GiB")
}

// rawFrame is a zstd frame with one raw block and a window of 2^log
// bytes in its header.
func rawFrame(t *testing.T, p []byte, log int) []byte {
	t.Helper()
	if len(p) >= 128<<10 {
		t.Fatal("too large for one raw block")
	}
	b := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, byte((log - 10) << 3)}
	h := uint32(len(p))<<3 | 1 // last block, raw
	b = append(b, byte(h), byte(h>>8), byte(h>>16))
	return append(b, p...)
}

// A zstd frame that asks for a window over 128 MiB is refused (D-225).
func TestZstdWindow(t *testing.T) {
	plain, _ := mkTar(t, stdBase())
	ok := rawFrame(t, plain, 27) // 128 MiB
	if _, err := pack(t, ok, opts(ok)); err != nil {
		t.Fatalf("128 MiB window: %v", err)
	}
	big := rawFrame(t, plain, 28)
	_, err := pack(t, big, opts(big))
	refuseErr(t, err, "zstd")
}

// cancelReader cancels ctx after n bytes.
type cancelReader struct {
	r      io.ReadSeeker
	n      int
	cancel func()
	hashed bool
}

func (c *cancelReader) Read(p []byte) (int, error) {
	if c.hashed {
		c.n -= len(p)
		if c.n <= 0 {
			c.cancel()
		}
	}
	return c.r.Read(p)
}

func (c *cancelReader) Seek(off int64, wh int) (int64, error) {
	c.hashed = true
	return c.r.Seek(off, wh)
}

func TestCancel(t *testing.T) {
	es := stdBase()
	for i := 0; i < 2000; i++ {
		es = append(es, ent{name: "./var/f" + strings.Repeat("x", i%50) + string(rune('a'+i%26)), flag: tar.TypeReg, mode: 0o644, body: strings.Repeat("d", 4000)})
	}
	plain, _ := mkTar(t, es)
	for _, c := range []struct {
		name string
		base []byte
	}{{"zstd", zst(t, plain)}, {"gzip", gz(t, plain)}} {
		t.Run(c.name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			src := &cancelReader{r: bytes.NewReader(c.base), n: len(c.base) / 3, cancel: cancel}
			err := Pack(ctx, src, io.Discard, opts(c.base))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v", err)
			}
			// No goroutine left behind (PR #144).
			deadline := time.Now().Add(2 * time.Second)
			for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > before {
				t.Errorf("%d goroutines after cancel, %d before", n, before)
			}
		})
	}
	// A cancel before the start stops the hash.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := zst(t, plain)
	if err := Pack(ctx, bytes.NewReader(base), io.Discard, opts(base)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// failWriter fails after n bytes.
type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n -= len(p); f.n < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestWriteError(t *testing.T) {
	plain, _ := mkTar(t, stdBase())
	base := zst(t, plain)
	for _, n := range []int{0, 100, 2000} {
		err := Pack(context.Background(), bytes.NewReader(base), &failWriter{n: n}, opts(base))
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("n=%d: err = %v", n, err)
		}
	}
}

// Optional: the real Debian template (host A1), when PLUMB_CT_FIXTURE
// names it.
func TestRealTemplate(t *testing.T) {
	p := os.Getenv("PLUMB_CT_FIXTURE")
	if p == "" {
		t.Skip("PLUMB_CT_FIXTURE not set")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha512.New()
	io.Copy(h, f)
	f.Seek(0, io.SeekStart)
	o := Options{SHA512: hex.EncodeToString(h.Sum(nil)), CA: []byte(testCA), AgentURL: "https://10.0.0.5:8444"}
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	w, _ := os.Create(out)
	start := time.Now()
	if err := Pack(context.Background(), f, w, o); err != nil {
		t.Fatal(err)
	}
	w.Close()
	st, _ := os.Stat(out)
	t.Logf("repacked to %d bytes in %v", st.Size(), time.Since(start))
	cmd := exec.Command("tar", "-tvzf", out)
	b, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "./usr/local/sbin/plumb-agent-install") {
		t.Error("no Plumb entries")
	}
	// Byte-exact base: the streams agree up to the end of the last
	// non-zero block of the base.
	f.Seek(0, io.SeekStart)
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	g, _ := os.Open(out)
	defer g.Close()
	zr, err := gzip.NewReader(g)
	if err != nil {
		t.Fatal(err)
	}
	var off, baseEnd, diff int64 = 0, 0, -1
	x, y := make([]byte, blockSize), make([]byte, blockSize)
	zero := make([]byte, blockSize)
	for {
		if _, err := io.ReadFull(dec, x); err != nil {
			break
		}
		if !bytes.Equal(x, zero) {
			baseEnd = off + blockSize
		}
		if diff < 0 {
			if _, err := io.ReadFull(zr, y); err != nil || !bytes.Equal(x, y) {
				diff = off
			}
		}
		off += blockSize
	}
	if diff < baseEnd {
		t.Errorf("output differs at %d, base ends at %d", diff, baseEnd)
	}
}

func FuzzPack(f *testing.F) {
	plain, end := mkTar(f, stdBase())
	f.Add(zst(f, plain))
	f.Add(gz(f, plain))
	f.Add(zst(f, plain[:end]))
	f.Add(gz(f, plain[:end-600]))
	f.Fuzz(func(t *testing.T, base []byte) {
		oldU, oldE := maxUnpacked, maxEntries
		maxUnpacked, maxEntries = 64<<20, 10_000
		defer func() { maxUnpacked, maxEntries = oldU, oldE }()
		var out bytes.Buffer
		err := Pack(context.Background(), bytes.NewReader(base), &out, opts(base))
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("not a refusal: %v (%T)", err, err)
			}
			return
		}
		// Whatever passed is a valid tar with the Plumb entries at the end.
		got := gunzip(t, out.Bytes())
		if len(got)%recordSize != 0 {
			t.Fatal("not whole records")
		}
		tr := tar.NewReader(bytes.NewReader(got))
		var last string
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("output: %v", err)
			}
			last = h.Name
		}
		if !strings.HasSuffix(last, "multi-user.target.wants/plumb-agent.service") {
			t.Fatalf("last entry %q", last)
		}
	})
}
