#!/usr/bin/env bash
# check-release-bundle.sh TARBALL WEB_DIST – checks a release tarball of
# scripts/package-release.sh (T-261, release plan C4). It never calls
# GitHub. Exit 0 only when every check holds:
#   - the tarball holds exactly plumb, plumb-agent, install.sh, SHA256SUMS,
#     all regular files at the top level;
#   - SHA256SUMS is strict (LF lines "<sha256>  <name>" for exactly the three
#     files) and matches;
#   - install.sh is stamped with a release version and otherwise equal to
#     install/install.sh, so the hook and environment tests of the repo
#     file (install/hooks_test.go) cover the bundle's copy;
#   - neither binary holds the D-369 test key (a body line of its armor);
#   - no source map: no sourceMappingURL in the binaries, no .map file in
#     WEB_DIST (the built web/dist; it must exist, L1 of #391).
set -euo pipefail
tarball=${1:?usage: check-release-bundle.sh TARBALL WEB_DIST}
web_dist=${2:?usage: check-release-bundle.sh TARBALL WEB_DIST}
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
fail() {
  echo "check-release-bundle: $*" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The members: exactly four regular files, no folder, no link, no path.
listing=$(tar -tvzf "$tarball") || fail "cannot read $tarball"
# One name per line (L2 of #391): a member "install.sh plumb" is one
# member, not two.
mapfile -t names < <(tar -tzf "$tarball")
seen_names=" "
for name in "${names[@]}"; do
  case $name in
  SHA256SUMS | install.sh | plumb | plumb-agent) ;;
  *) fail "the tarball holds the member \"$name\"; it must hold exactly plumb, plumb-agent, install.sh, SHA256SUMS" ;;
  esac
  [[ $seen_names != *" $name "* ]] || fail "the tarball holds the member $name twice"
  seen_names="$seen_names$name "
done
[[ ${#names[@]} == 4 ]] || fail "the tarball holds ${#names[@]} members, not exactly plumb, plumb-agent, install.sh, SHA256SUMS"
while IFS= read -r line; do
  [[ ${line:0:1} == "-" ]] || fail "a member is not a regular file: $line"
done <<<"$listing"
tar -xzf "$tarball" -C "$work"

# SHA256SUMS: strict lines, exactly the three files, and they match.
sums=$work/SHA256SUMS
if LC_ALL=C grep -q $'\r' "$sums"; then fail "SHA256SUMS has a carriage return"; fi
mapfile -t lines <"$sums"
seen=""
for line in "${lines[@]}"; do
  [[ $line =~ ^[0-9a-f]{64}\ \ (plumb|plumb-agent|install\.sh)$ ]] ||
    fail "SHA256SUMS line is not \"<sha256>  <file>\" for plumb, plumb-agent or install.sh: $line"
  [[ " $seen " != *" ${BASH_REMATCH[1]} "* ]] || fail "SHA256SUMS lists ${BASH_REMATCH[1]} twice"
  seen="$seen ${BASH_REMATCH[1]}"
done
[[ ${#lines[@]} == 3 ]] || fail "SHA256SUMS has ${#lines[@]} lines, not 3"
(cd "$work" && sha256sum --quiet -c SHA256SUMS) || fail "SHA256SUMS does not match the files"

# install.sh: stamped, and the repo's installer apart from the stamp line.
stamp=$(grep -E '^RELEASE_VERSION="v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?"$' "$work/install.sh") ||
  fail "install.sh is not stamped with a release version (RELEASE_VERSION)"
[[ $(grep -c '^RELEASE_VERSION=' "$work/install.sh") == 1 ]] || fail "install.sh has more than one RELEASE_VERSION line"
sed 's/^RELEASE_VERSION="[^"]*"$/RELEASE_VERSION=""/' "$work/install.sh" >"$work/install.unstamped"
cmp -s "$work/install.unstamped" "$repo/install/install.sh" ||
  fail "install.sh of the bundle differs from install/install.sh beyond the stamp ($stamp)"

# The D-369 test key: no body line of its armor in a binary.
key=$repo/internal/trust/testdata/f5/test-key.asc
[[ -s $key ]] || fail "the test key $key is missing; the check cannot run"
n=0
while IFS= read -r line; do
  [[ $line =~ ^[A-Za-z0-9+/=]{40,}$ ]] || continue
  n=$((n + 1))
  for bin in plumb plumb-agent; do
    if LC_ALL=C grep -qaF -- "$line" "$work/$bin"; then
      fail "$bin holds the D-369 test key (built with the plumbtest tag?)"
    fi
  done
done <"$key"
((n > 0)) || fail "the test key $key has no body line to look for"

# No source map.
for bin in plumb plumb-agent; do
  if LC_ALL=C grep -qa 'sourceMappingURL' "$work/$bin"; then fail "$bin holds a sourceMappingURL"; fi
done
[[ -d $web_dist ]] || fail "$web_dist is not a folder: the .map check cannot run"
maps=$(find "$web_dist" -name '*.map' | wc -l)
[[ $maps == 0 ]] || fail "$web_dist holds $maps .map file(s)"

echo "check-release-bundle: ok ($stamp; ${#lines[@]} sums; no test key ($n key lines); no source map)"
