#!/usr/bin/env python3
"""Exact-value leak scan (T-256, release slice C5a/C5b).

Reads the secret and host-identifying values from the dev VM's files under
a values directory (default ~/.config/plump) INSIDE this process, and looks
for them in git objects and in directory trees. It prints and writes only
places and kinds, never a value. No value is ever an argument of this tool,
and no error path prints one: an error prints its type and the file only.

  leak-scan.py --out HITS [--values DIR] [--git MIRROR]... [--dir DIR]...
               [--label L] [--public FILE]...

Values: the address keys of *.env; the whole content of the admin
password, API token (and its secret part) and recovery key files; each
line of the guest SSH key and the Plumb CA certificate; the guest public
key; every string of a t*-secrets.json; and the addresses, MACs, SSH host
key fingerprints (and install command tokens) of the other files; the
IPv6 prefixes of the test host's network, one per line in the file
testhost-ipv6.prefixes (written on the host by the person or agent that
reads them; never typed into a command).

IPv6 (pl-lead, T-277): every IPv6 literal in the content is parsed. One
inside a prefix of testhost-ipv6.prefixes is the kind "test host IPv6
prefix". Any other global-unicast address (2000::/3, without the
documentation ranges 2001:db8::/32 and 3fff::/20) is the kind "global
IPv6 address (any)": a place to look at, not always a leak. Both kinds
need no values file to name the address; only places are written.

Forms searched for each value: the plain bytes; base64 (standard and
URL-safe, at each of the three alignments inside a longer base64 text);
hex (lower and upper case); URL encoding. Content searched: every git
object (blobs, commits, tags), every file under --dir, and the members of
gzip, bzip2, xz, tar and zip content inside them (nested up to 3 levels,
each member at most 512 MiB).

NOT covered (say so in a report): text in images (PNG screenshots), an
IPv6 address in base64, hex or URL encoding, other
encodings (base32, UTF-16, double URL encoding, JSON \\u escapes, quoted
printable), compression other than gzip/bzip2/xz/zip, encrypted content,
and a value split over two lines or two git objects.

The hit file must not exist (it is created 0600 with O_EXCL|O_NOFOLLOW).
Values shorter than 7 characters cannot be searched without noise: the
summary counts them per kind, and a short whole-file secret is an error.
Exit 0 on a finished scan (the summary says how many places), 2 on an
error, 3 when a whole-file secret is too short to search.
"""
import argparse
import base64
import bz2
import gzip
import io
import ipaddress
import json
import lzma
import os
import re
import subprocess
import sys
import tarfile
import urllib.parse
import zipfile

# Keys of the env files whose values identify the test host (addresses,
# the SSH target, the URL). Other keys (node, pool, storage, VMID, file
# paths, public image URLs and checksums) are public in the repo.
ADDRESS_KEYS = re.compile(r"(_SSH|_HOST|_IP(_\d+)?|_GW|_URL)$")
SKIP_KEYS = re.compile(r"^PLUMB_TEST_IMAGE_")
WHOLE_FILES = {  # file → kind; the whole content (stripped) is the value
    "testhost-admin.pw": "test host admin password",
    "testhost-api.token": "test host API token",
    "testhost-recovery.key": "test host recovery key",
}
PEM_FILES = {
    "testhost-guest-key": "test guest SSH private key (line)",
    "testhost-plumb-ca.crt": "test host Plumb CA certificate (line; identifying)",
}
PUB_FILES = {"testhost-guest-key.pub": "test guest SSH public key (identifying)"}
IPV6_PREFIX_FILE = "testhost-ipv6.prefixes"
IPV6_GLOBAL = ipaddress.ip_network("2000::/3")
IPV6_DOC = [ipaddress.ip_network(n) for n in ("2001:db8::/32", "3fff::/20")]
# An IPv6 literal: hex groups with at least two colons, not part of a
# longer hex/colon run (a MAC has five colons but parses as no address).
RE_IPV6 = re.compile(rb"(?<![0-9A-Fa-f:])((?:[0-9A-Fa-f]{1,4})?(?::[0-9A-Fa-f]{0,4}){2,7})(?![0-9A-Fa-f:])")
SECRETS_JSON = re.compile(r"^t\d*-secrets\.json$")

MIN_LEN = 7
MAX_MEMBER = 512 << 20
MAX_DEPTH = 3

RE_IPV4 = re.compile(r"(?<![\d.])(\d{1,3}(?:\.\d{1,3}){3})(?![\d])")
RE_MAC = re.compile(r"(?<![0-9A-Fa-f:])((?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2})(?![0-9A-Fa-f:])")
RE_FPR = re.compile(r"SHA256:[A-Za-z0-9+/]{43}")
RE_TOKEN = re.compile(r"[A-Za-z0-9_-]{24,}")
DOC_NETS = [ipaddress.ip_network(n) for n in (
    "0.0.0.0/8", "127.0.0.0/8", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
    "255.0.0.0/8", "224.0.0.0/4")]

# What the tool is doing, for an error message without a value.
STEP = ["start"]


class TooShort(Exception):
    pass


def openssh_header(line):
    """The first body line of an OpenSSH private key is the same for every
    unencrypted key of a type (magic, cipher "none", kdf "none"): no secret."""
    try:
        return base64.b64decode(line + "=" * (-len(line) % 4)).startswith(b"openssh-key-v1")
    except ValueError:
        return False


def public_ip(s):
    """The address, or None when it is no valid IPv4 or a doc/loopback range."""
    try:
        ip = ipaddress.ip_address(s)
    except ValueError:
        return None
    if any(ip in n for n in DOC_NETS):
        return None
    return str(ip)


def ip_needle(v):
    """The address part of 'root@1.2.3.4', 'https://1.2.3.4:8443/', '1.2.3.4/24'."""
    m = RE_IPV4.search(v)
    return public_ip(m.group(1)) if m else None


class Needles:
    def __init__(self):
        self.items = {}  # value -> kind
        self.addresses = set()  # values that are addresses (matched with digit bounds)
        self.macs = set()
        self.short = {}  # kind -> count of values too short to search
        self.v6nets = []  # (network, kind): IPv6 prefixes of the test host

    def add(self, value, kind, address=False, mac=False, must=False):
        value = value.strip()
        if len(value) < MIN_LEN:
            if must:
                raise TooShort(kind)
            base = kind.split(" (")[0]
            self.short[base] = self.short.get(base, 0) + 1
            return
        if value in self.items:  # one value from several sources: name them all
            _, _, src = kind.partition(" (")
            if src and src.rstrip(")") not in self.items[value]:
                self.items[value] = self.items[value].replace(")", ", " + src.rstrip(")") + ")", 1)
            return
        if address:
            # Only values from an address source are classified, and only
            # after public_ip accepted them, so this cannot raise.
            kind += " [private]" if ipaddress.ip_address(value).is_private else " [public internet]"
            self.addresses.add(value)
        if mac:
            self.macs.add(value)
        self.items[value] = kind

    def mark_public(self, paths):
        """Marks values that a public file (for example AGENTS.md) names on
        purpose, so the report can tell them from a leak."""
        for path in paths:
            try:
                with open(path, "rb") as f:
                    data = f.read()
            except OSError:
                continue
            for value, kind in list(self.items.items()):
                if value.encode() in data:
                    self.items[value] = kind + " [also in %s]" % os.path.basename(path)


def json_strings(doc, path=""):
    """Every string in a JSON document, with its key path (keys are not values)."""
    if isinstance(doc, str):
        yield path or "$", doc
    elif isinstance(doc, dict):
        for k, v in doc.items():
            yield from json_strings(v, "%s.%s" % (path, k) if path else str(k))
    elif isinstance(doc, list):
        for i, v in enumerate(doc):
            yield from json_strings(v, "%s[%d]" % (path, i))


def load(values_dir):
    n = Needles()
    for name in sorted(os.listdir(values_dir)):
        path = os.path.join(values_dir, name)
        if not os.path.isfile(path):
            continue
        STEP[0] = "reading the value file %s" % name
        with open(path, "r", errors="replace") as f:
            text = f.read()
        if name.endswith(".env"):
            for line in text.splitlines():
                m = re.match(r"^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$", line)
                if not m or SKIP_KEYS.search(m.group(1)) or not ADDRESS_KEYS.search(m.group(1)):
                    continue
                ip = ip_needle(m.group(2).strip().strip("'\""))
                if ip:
                    n.add(ip, "test host address (%s:%s)" % (name, m.group(1)), address=True)
        elif name == IPV6_PREFIX_FILE:
            for i, line in enumerate(text.splitlines(), 1):
                line = line.split("#", 1)[0].strip()
                if not line:
                    continue
                try:
                    net = ipaddress.ip_network(line, strict=False)
                except ValueError:
                    raise RuntimeError("line %d of %s is not an IPv6 prefix" % (i, name)) from None
                if net.version != 6 or net.prefixlen < 16:
                    raise RuntimeError("line %d of %s is not an IPv6 prefix of /16 or longer" % (i, name))
                n.v6nets.append((net, "test host IPv6 prefix (%s line %d)" % (name, i)))
        elif name in WHOLE_FILES:
            value = text.strip()
            n.add(value, WHOLE_FILES[name] + " (%s)" % name, must=True)
            if "=" in value:  # user@realm!name=secret: the secret part too
                n.add(value.split("=", 1)[1], WHOLE_FILES[name] + " (secret part, %s)" % name, must=True)
        elif name in PEM_FILES:
            for line in text.splitlines():
                if line.startswith("-----") or len(line.strip()) < 16 or openssh_header(line.strip()):
                    continue
                n.add(line.strip(), PEM_FILES[name])
        elif name in PUB_FILES:
            parts = text.split()
            if len(parts) >= 2:
                n.add(parts[1], PUB_FILES[name])
        elif SECRETS_JSON.match(name):
            # A test secrets file of an acceptance (C7: test tokens): every
            # string value is a needle; the key path names it.
            try:
                doc = json.loads(text)
            except ValueError:
                raise RuntimeError("%s is not valid JSON" % name) from None
            for keypath, value in json_strings(doc):
                n.add(value, "test token (%s: %s)" % (name, keypath))
        else:
            # Install command and host logs: addresses, MACs, SSH host key
            # fingerprints, and (install command only) long tokens.
            for ip in RE_IPV4.findall(text):
                ip = public_ip(ip)
                if ip:
                    n.add(ip, "test host address (from %s)" % name, address=True)
            for mac in RE_MAC.findall(text):
                n.add(mac.lower().replace("-", ":"), "MAC (from %s)" % name, mac=True)
            for fpr in RE_FPR.findall(text):
                n.add(fpr, "SSH host key fingerprint (from %s)" % name)
            if name == ".install-cmd":
                for tok in RE_TOKEN.findall(text):
                    if not tok.startswith(("plumb", "PLUMB")) and re.search(r"\d", tok) and re.search(r"[A-Za-z]", tok):
                        n.add(tok, "token in the install command")
    STEP[0] = "start"
    return n


def b64_forms(v):
    """The base64 text of v at each of the three byte alignments inside a
    longer base64 stream (the characters that only v's bits make)."""
    out = set()
    for k in range(3):
        enc = base64.b64encode(b"\0" * k + v).rstrip(b"=")
        skip = (k * 8 + 5) // 6  # characters that hold a pad bit
        full = ((k + len(v)) * 8) // 6  # characters made only of whole bits
        part = enc[skip:full]
        if len(part) >= 8:
            out.add(part)
            out.add(part.translate(bytes.maketrans(b"+/", b"-_")))
    return out


def ipv6_kind(text, nets):
    """The kind of an IPv6 literal, or None: a test host prefix first,
    then any other global-unicast address outside the documentation
    ranges."""
    try:
        ip = ipaddress.IPv6Address(text.split("%", 1)[0])
    except ValueError:
        return None
    for net, kind in nets:
        if ip in net:
            return kind
    if ip in IPV6_GLOBAL and not any(ip in d for d in IPV6_DOC):
        return "global IPv6 address (any)"
    return None


class Matcher:
    def __init__(self, needles):
        self.byte = []  # (pattern, kind)
        self.v6nets = needles.v6nets
        for value, kind in needles.items.items():
            raw = value.encode()
            if value in needles.macs:
                pat = re.escape(value).replace(":", "[:-]")
                self.byte.append((re.compile(pat.encode(), re.I), kind))
                continue
            if value in needles.addresses:
                self.byte.append((re.compile(rb"(?<![\d.])" + re.escape(raw) + rb"(?![\d])"), kind))
            else:
                self.byte.append((re.compile(re.escape(raw)), kind))
            forms = {f: "base64" for f in b64_forms(raw)}
            forms[raw.hex().encode()] = "hex"
            forms[raw.hex().upper().encode()] = "hex"
            q = urllib.parse.quote(value, safe="").encode()
            if q != raw:
                forms[q] = "URL-encoded"
            for f, how in forms.items():
                self.byte.append((re.compile(re.escape(f)), "%s [%s]" % (kind, how)))

    def scan(self, data):
        for pat, kind in self.byte:
            for m in pat.finditer(data):
                yield m.start(), kind
        for m in RE_IPV6.finditer(data):
            kind = ipv6_kind(m.group(1).decode("ascii"), self.v6nets)
            if kind:
                yield m.start(), kind


def line_of(data, pos):
    return data.count(b"\n", 0, pos) + 1


def unpack(data):
    """Members of compressed or archived content: [(name, bytes)], or None
    when data is no archive. Errors are raised without their text."""
    def capped(f):
        b = f.read(MAX_MEMBER + 1)
        if len(b) > MAX_MEMBER:
            raise RuntimeError("an archive member is larger than %d bytes" % MAX_MEMBER)
        return b
    if data[:2] == b"\x1f\x8b":
        return [("(gzip)", capped(gzip.GzipFile(fileobj=io.BytesIO(data))))]
    if data[:3] == b"BZh":
        return [("(bzip2)", capped(bz2.BZ2File(io.BytesIO(data))))]
    if data[:6] == b"\xfd7zXZ\x00":
        return [("(xz)", capped(lzma.LZMAFile(io.BytesIO(data))))]
    if data[:4] == b"PK\x03\x04":
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            return [(i.filename, capped(z.open(i))) for i in z.infolist() if not i.is_dir()]
    if len(data) > 262 and data[257:262] == b"ustar":
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:") as t:
            return [(m.name, capped(t.extractfile(m))) for m in t.getmembers() if m.isfile()]
    return None


class Scanner:
    def __init__(self, matcher, emit):
        self.matcher, self.emit = matcher, emit
        self.members = 0
        self.unreadable = 0

    def data(self, place, data, depth=0):
        for pos, kind in self.matcher.scan(data):
            self.emit("%s line %d" % (place, line_of(data, pos)), kind)
        if depth >= MAX_DEPTH:
            return
        try:
            members = unpack(data)
        except Exception:  # no text: a broken archive may hold a value
            self.unreadable += 1
            return
        for name, body in members or []:
            self.members += 1
            self.data("%s member %s" % (place, name), body, depth + 1)

    def git(self, mirror):
        STEP[0] = "scanning the git mirror %s" % mirror
        p = subprocess.Popen(["git", "-C", mirror, "cat-file", "--batch-all-objects", "--batch", "--unordered"],
                             stdout=subprocess.PIPE)
        out, objects = p.stdout, 0
        while True:
            head = out.readline()
            if not head:
                break
            sha, typ, size = head.split()
            body = out.read(int(size))
            out.read(1)
            objects += 1
            if typ == b"tree":
                continue
            self.data("git %s %s" % (typ.decode(), sha.decode()), body)
        if p.wait() != 0:
            raise RuntimeError("git cat-file failed")
        return objects

    def dir(self, root):
        files = 0
        for dirpath, _, names in os.walk(root):
            for name in names:
                path = os.path.join(dirpath, name)
                STEP[0] = "scanning %s" % os.path.relpath(path, root)
                try:
                    with open(path, "rb") as f:
                        body = f.read()
                except OSError:
                    self.unreadable += 1
                    continue
                files += 1
                self.data("file %s" % os.path.relpath(path, root), body)
        return files


def run(argv):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True, help="the hit file; must not exist")
    ap.add_argument("--values", default=os.path.expanduser("~/.config/plump"))
    ap.add_argument("--git", action="append", default=[])
    ap.add_argument("--dir", action="append", default=[])
    ap.add_argument("--label", default="")
    ap.add_argument("--public", action="append", default=[], help="a file that names values on purpose")
    a = ap.parse_args(argv)
    needles = load(a.values)
    needles.mark_public(a.public)
    matcher = Matcher(needles)
    kinds = {}
    for k in needles.items.values():
        base = k.split(" (")[0]
        kinds[base] = kinds.get(base, 0) + 1
    for _, k in needles.v6nets:
        base = k.split(" (")[0]
        kinds[base] = kinds.get(base, 0) + 1
    STEP[0] = "creating the hit file"
    fd = os.open(a.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    hits = 0
    with os.fdopen(fd, "w") as out:
        def emit(place, kind):
            nonlocal hits
            hits += 1
            out.write("%s\t%s\t%s\n" % (a.label, place, kind))
        s = Scanner(matcher, emit)
        counts = []
        for g in a.git:
            counts.append("%d git objects" % s.git(g))
        for d in a.dir:
            counts.append("%d files" % s.dir(d))
    short = ", ".join("%s: %d" % kv for kv in sorted(needles.short.items())) or "none"
    print("leak-scan%s: %d values (%s); %d too short to search (%s); scanned %s, %d archive members, "
          "%d unreadable; %d places" % (
              " " + a.label if a.label else "", len(needles.items) + len(needles.v6nets),
              ", ".join("%s: %d" % kv for kv in sorted(kinds.items())), sum(needles.short.values()), short,
              ", ".join(counts) or "nothing", s.members, s.unreadable, hits))
    return 0


def main(argv=None):
    try:
        return run(sys.argv[1:] if argv is None else argv)
    except SystemExit:
        raise
    except TooShort as e:
        # The kind names the file; the value is never printed.
        print("leak-scan: %s is shorter than %d characters and cannot be searched" % (e.args[0], MIN_LEN),
              file=sys.stderr)
        return 3
    except Exception as e:  # noqa: BLE001 - the text of an error may hold a value
        print("leak-scan: %s while %s (the error text is not printed: it may hold a value)" % (
            type(e).__name__, STEP[0]), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
