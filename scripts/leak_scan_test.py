#!/usr/bin/env python3
"""Tests of scripts/leak-scan.py (T-256 review of #389). FAKE values only.

Run: python3 -I scripts/leak_scan_test.py (make test-scripts).
"""
import base64
import gzip
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
import urllib.parse
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "leak-scan.py")

FAKE_BEARER = "fake-bearer-Zq7Wx3Lp9Rt2"
FAKE_PW = "Fake-Admin-pw-4711"


class LeakScan(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        self.values = os.path.join(self.root, "values")
        self.data = os.path.join(self.root, "data")
        os.mkdir(self.values)
        os.mkdir(self.data)
        self.n = 0

    def tearDown(self):
        self.tmp.cleanup()

    def value(self, name, content):
        with open(os.path.join(self.values, name), "w") as f:
            f.write(content)

    def put(self, name, content):
        path = os.path.join(self.data, name)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(content if isinstance(content, bytes) else content.encode())

    def scan(self, *extra, out=None):
        self.n += 1
        out = out or os.path.join(self.root, "hits%d.tsv" % self.n)
        p = subprocess.run([sys.executable, "-I", TOOL, "--out", out, "--values", self.values,
                            "--dir", self.data] + list(extra), capture_output=True, text=True)
        hits = ""
        if os.path.exists(out) and not os.path.islink(out):
            with open(out) as f:
                hits = f.read()
        return p, hits, out

    def assertNoValue(self, *texts, values=(FAKE_BEARER, FAKE_PW)):
        for t in texts:
            for v in values:
                self.assertNotIn(v, t)

    # M1: t*-secrets.json
    def test_secrets_json_strings_are_needles(self):
        self.value("t999-secrets.json", json.dumps({"admin": {"bearer": FAKE_BEARER, "user": "qa"}}))
        self.put("host/log.txt", "Authorization: Bearer %s\n" % FAKE_BEARER)
        p, hits, _ = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("file host/log.txt line 1\ttest token (t999-secrets.json: admin.bearer)", hits)
        self.assertIn("1 too short to search (test token: 1)", p.stdout)  # "qa"
        self.assertNoValue(p.stdout, p.stderr, hits)

    # L1: no value on any error path
    def test_ipv4_lookalike_secret_does_not_crash(self):
        self.value("testhost-admin.pw", "300.400.500.600\n")
        self.put("x.txt", "pw 300.400.500.600")
        p, hits, _ = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("test host admin password", hits)
        self.assertNoValue(p.stdout, p.stderr, hits, values=("300.400.500.600",))

    def test_error_text_never_printed(self):
        self.value("t1-secrets.json", '{"bearer": "%s", broken' % FAKE_BEARER)
        p, _, _ = self.scan()
        self.assertEqual(p.returncode, 2)
        self.assertIn("RuntimeError while reading the value file t1-secrets.json", p.stderr)
        self.assertNoValue(p.stdout, p.stderr)

    # L2: short values are counted; a short whole-file secret fails
    def test_short_whole_file_secret_fails(self):
        self.value("testhost-admin.pw", "abcde\n")
        p, _, _ = self.scan()
        self.assertEqual(p.returncode, 3)
        self.assertIn("test host admin password (testhost-admin.pw) is shorter than 7", p.stderr)
        self.assertNoValue(p.stdout, p.stderr, values=("abcde",))

    # L3: the hit file
    def test_existing_or_symlinked_out_is_refused(self):
        self.value("testhost-admin.pw", FAKE_PW)
        target = os.path.join(self.root, "target")
        with open(target, "w") as f:
            f.write("keep")
        os.chmod(target, 0o644)
        link = os.path.join(self.root, "link.tsv")
        os.symlink(target, link)
        for out in (target, link):
            p, _, _ = self.scan(out=out)
            self.assertEqual(p.returncode, 2, out)
            self.assertIn("creating the hit file", p.stderr)
        with open(target) as f:
            self.assertEqual(f.read(), "keep")
        self.assertEqual(os.stat(target).st_mode & 0o777, 0o644)
        p, _, out = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(os.stat(out).st_mode & 0o777, 0o600)

    # L4: encoded forms
    def test_base64_every_alignment_hex_and_url(self):
        self.value("testhost-admin.pw", FAKE_PW + "/+&")
        v = (FAKE_PW + "/+&").encode()
        for k in range(3):
            self.put("b64-%d.txt" % k, base64.b64encode(b"x" * k + v + b"tail-bytes"))
        self.put("b64url.txt", base64.urlsafe_b64encode(b"yy" + v + b"z"))
        self.put("hex.txt", v.hex())
        self.put("HEX.txt", v.hex().upper())
        self.put("url.txt", "pw=" + urllib.parse.quote(v.decode(), safe=""))
        p, hits, _ = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        for name in ("b64-0.txt", "b64-1.txt", "b64-2.txt", "b64url.txt"):
            self.assertIn("file %s line 1\ttest host admin password (testhost-admin.pw) [base64]" % name, hits)
        for name in ("hex.txt", "HEX.txt"):
            self.assertIn("file %s line 1\ttest host admin password (testhost-admin.pw) [hex]" % name, hits)
        self.assertIn("file url.txt line 1\ttest host admin password (testhost-admin.pw) [URL-encoded]", hits)
        self.assertNoValue(p.stdout, p.stderr, hits)

    # L4: archives (the RC tarball of C5b), also nested
    def test_archive_members(self):
        self.value("t2-secrets.json", json.dumps({"bearer": FAKE_BEARER}))
        inner = io.BytesIO()
        with tarfile.open(fileobj=inner, mode="w:gz") as t:
            body = ("token %s\n" % FAKE_BEARER).encode()
            info = tarfile.TarInfo("plumb-v0/notes.txt")
            info.size = len(body)
            t.addfile(info, io.BytesIO(body))
        self.put("rc.tar.gz", inner.getvalue())
        z = io.BytesIO()
        with zipfile.ZipFile(z, "w") as zf:
            zf.writestr("logs/1_job.txt", "x\n" + FAKE_BEARER)
            zf.writestr("nested.tar.gz", inner.getvalue())
        self.put("run.zip", z.getvalue())
        self.put("one.gz", gzip.compress(FAKE_BEARER.encode()))
        p, hits, _ = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        for place in ("file rc.tar.gz member (gzip) member plumb-v0/notes.txt line 1",
                      "file run.zip member logs/1_job.txt line 2",
                      "file run.zip member nested.tar.gz member (gzip) member plumb-v0/notes.txt line 1",
                      "file one.gz member (gzip) line 1"):
            self.assertIn(place + "\ttest token (t2-secrets.json: bearer)", hits)
        self.assertNoValue(p.stdout, p.stderr, hits)

    def test_broken_archive_counts_unreadable(self):
        self.value("testhost-admin.pw", FAKE_PW)
        self.put("bad.gz", b"\x1f\x8b" + FAKE_PW.encode())
        p, hits, _ = self.scan()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("1 unreadable", p.stdout)
        self.assertIn("file bad.gz line 1\t", hits)  # the plain bytes still match
        self.assertNoValue(p.stdout, p.stderr, hits)

    def test_git_objects(self):
        self.value("testhost-admin.pw", FAKE_PW)
        repo = os.path.join(self.root, "repo")
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.invalid",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@example.invalid")
        subprocess.run(["git", "init", "-q", repo], check=True, env=env)
        with open(os.path.join(repo, "a.gz"), "wb") as f:
            f.write(gzip.compress(FAKE_PW.encode()))
        subprocess.run(["git", "-C", repo, "add", "a.gz"], check=True, env=env)
        subprocess.run(["git", "-C", repo, "commit", "-q", "-m", "msg " + FAKE_PW], check=True, env=env)
        p, hits, _ = self.scan("--git", repo)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertRegex(hits, r"git blob [0-9a-f]+ member \(gzip\) line 1\ttest host admin password")
        self.assertRegex(hits, r"git commit [0-9a-f]+ line \d+\ttest host admin password")
        self.assertNoValue(p.stdout, p.stderr, hits)


if __name__ == "__main__":
    unittest.main(verbosity=1)
