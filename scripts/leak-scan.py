#!/usr/bin/env python3
"""Exact-value leak scan (T-256, release slice C5a/C5b).

Reads the secret and host-identifying values from the dev VM's files under
a values directory (default ~/.config/plump) INSIDE this process, and looks
for them in git objects and in directory trees. It prints and writes only
places and kinds, never a value. No value is ever an argument of this tool.

  leak-scan.py --out HITS [--values DIR] [--git MIRROR]... [--dir DIR]...

The hit file is created with mode 0600. Exit 0 always; the summary line
says how many places were found.
"""
import argparse
import base64
import ipaddress
import os
import re
import subprocess
import sys

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

RE_IPV4 = re.compile(r"(?<![\d.])(\d{1,3}(?:\.\d{1,3}){3})(?![\d])")
RE_MAC = re.compile(r"(?<![0-9A-Fa-f:])((?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2})(?![0-9A-Fa-f:])")
RE_FPR = re.compile(r"SHA256:[A-Za-z0-9+/]{43}")
RE_TOKEN = re.compile(r"[A-Za-z0-9_-]{24,}")
DOC_NETS = [ipaddress.ip_network(n) for n in (
    "0.0.0.0/8", "127.0.0.0/8", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
    "255.0.0.0/8", "224.0.0.0/4")]


def openssh_header(line):
    """The first body line of an OpenSSH private key is the same for every
    unencrypted key of a type (magic, cipher "none", kdf "none"): no secret."""
    try:
        return base64.b64decode(line + "=" * (-len(line) % 4)).startswith(b"openssh-key-v1")
    except ValueError:
        return False


def public_ip(s):
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

    def add(self, value, kind):
        value = value.strip()
        if len(value) < 7:
            return
        if value in self.items:  # one value from several sources: name them all
            base, _, src = kind.partition(" (")
            if src and src.rstrip(")") not in self.items[value]:
                self.items[value] = self.items[value].replace(")", ", " + src.rstrip(")") + ")", 1)
            return
        if RE_IPV4.fullmatch(value):
            kind += " [private]" if ipaddress.ip_address(value).is_private else " [public internet]"
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


def load(values_dir):
    n = Needles()
    for name in sorted(os.listdir(values_dir)):
        path = os.path.join(values_dir, name)
        if not os.path.isfile(path):
            continue
        with open(path, "r", errors="replace") as f:
            text = f.read()
        if name.endswith(".env"):
            for line in text.splitlines():
                m = re.match(r"^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$", line)
                if not m or SKIP_KEYS.search(m.group(1)) or not ADDRESS_KEYS.search(m.group(1)):
                    continue
                ip = ip_needle(m.group(2).strip().strip("'\""))
                if ip:
                    n.add(ip, "test host address (%s:%s)" % (name, m.group(1)))
        elif name in WHOLE_FILES:
            value = text.strip()
            n.add(value, WHOLE_FILES[name])
            if "=" in value:  # user@realm!name=secret: the secret part too
                n.add(value.split("=", 1)[1], WHOLE_FILES[name] + " (secret part)")
        elif name in PEM_FILES:
            for line in text.splitlines():
                if line.startswith("-----") or len(line.strip()) < 16 or openssh_header(line.strip()):
                    continue
                n.add(line.strip(), PEM_FILES[name])
        elif name in PUB_FILES:
            parts = text.split()
            if len(parts) >= 2:
                n.add(parts[1], PUB_FILES[name])
        else:
            # Install command and host logs: addresses, MACs, SSH host key
            # fingerprints, and (install command only) long tokens.
            for ip in RE_IPV4.findall(text):
                ip = public_ip(ip)
                if ip:
                    n.add(ip, "test host address (from %s)" % name)
            for mac in RE_MAC.findall(text):
                n.add(mac.lower().replace("-", ":"), "MAC (from %s)" % name)
            for fpr in RE_FPR.findall(text):
                n.add(fpr, "SSH host key fingerprint (from %s)" % name)
            if name == ".install-cmd":
                for tok in RE_TOKEN.findall(text):
                    if not tok.startswith(("plumb", "PLUMB")) and re.search(r"\d", tok) and re.search(r"[A-Za-z]", tok):
                        n.add(tok, "token in the install command")
    return n


class Matcher:
    def __init__(self, needles):
        self.byte = []  # (pattern, kind)
        for value, kind in needles.items.items():
            if kind.startswith("MAC"):
                pat = re.escape(value).replace(":", "[:-]")
                self.byte.append((re.compile(pat.encode(), re.I), kind))
            elif RE_IPV4.fullmatch(value):
                self.byte.append((re.compile(rb"(?<![\d.])" + re.escape(value.encode()) + rb"(?![\d])"), kind))
            else:
                self.byte.append((re.compile(re.escape(value.encode())), kind))

    def scan(self, data):
        for pat, kind in self.byte:
            for m in pat.finditer(data):
                yield m.start(), kind


def line_of(data, pos):
    return data.count(b"\n", 0, pos) + 1


def scan_git(mirror, matcher, emit):
    p = subprocess.Popen(["git", "-C", mirror, "cat-file", "--batch-all-objects", "--batch", "--unordered"],
                         stdout=subprocess.PIPE)
    out, objects = p.stdout, 0
    while True:
        head = out.readline()
        if not head:
            break
        sha, typ, size = head.split()
        data = out.read(int(size))
        out.read(1)
        objects += 1
        if typ == b"tree":
            continue
        for pos, kind in matcher.scan(data):
            emit("git %s %s line %d" % (typ.decode(), sha.decode(), line_of(data, pos)), kind)
    p.wait()
    return objects


def scan_dir(root, matcher, emit):
    files = 0
    for dirpath, _, names in os.walk(root):
        for name in names:
            path = os.path.join(dirpath, name)
            try:
                with open(path, "rb") as f:
                    data = f.read()
            except OSError:
                continue
            files += 1
            for pos, kind in matcher.scan(data):
                emit("file %s line %d" % (os.path.relpath(path, root), line_of(data, pos)), kind)
    return files


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--values", default=os.path.expanduser("~/.config/plump"))
    ap.add_argument("--git", action="append", default=[])
    ap.add_argument("--dir", action="append", default=[])
    ap.add_argument("--label", default="")
    ap.add_argument("--public", action="append", default=[], help="a file that names values on purpose")
    a = ap.parse_args()
    needles = load(a.values)
    needles.mark_public(a.public)
    matcher = Matcher(needles)
    kinds = {}
    for k in needles.items.values():
        base = k.split(" (")[0]
        kinds[base] = kinds.get(base, 0) + 1
    fd = os.open(a.out, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    os.fchmod(fd, 0o600)
    hits = 0
    with os.fdopen(fd, "a") as out:
        def emit(place, kind):
            nonlocal hits
            hits += 1
            out.write("%s\t%s\t%s\n" % (a.label, place, kind))
        counts = []
        for g in a.git:
            counts.append("%d git objects" % scan_git(g, matcher, emit))
        for d in a.dir:
            counts.append("%d files" % scan_dir(d, matcher, emit))
    print("leak-scan%s: %d values (%s); scanned %s; %d places" % (
        " " + a.label if a.label else "", len(needles.items),
        ", ".join("%s: %d" % kv for kv in sorted(kinds.items())), ", ".join(counts) or "nothing", hits))


if __name__ == "__main__":
    sys.exit(main())
