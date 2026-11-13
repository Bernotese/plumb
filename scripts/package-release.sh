#!/usr/bin/env bash
# Build a release bundle locally. This script never calls GitHub.
set -euo pipefail
version=${1:?usage: package-release.sh vMAJOR.MINOR.PATCH [output-directory]}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+([.-][a-zA-Z0-9]+)*)?$ ]] || {
  echo 'Expected vMAJOR.MINOR.PATCH with an optional prerelease suffix' >&2
  exit 1
}
output=$(realpath -m "${2:-dist-release}")
mkdir -p "$output"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
# No checkout path, VCS dirty state, random build ID or wall clock in binaries.
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
make build VERSION="$version" \
  GO_BUILD="go build -trimpath -buildvcs=false -ldflags '-s -w -buildid= -X main.version=$version'"
[[ $(bin/plumb --version) == "plumb $version" ]]
[[ $(bin/plumb-agent version) == "plumb-agent $version" ]]
install -m 0755 bin/plumb bin/plumb-agent install/install.sh "$stage/"
(cd "$stage" && sha256sum plumb plumb-agent install.sh > SHA256SUMS && sha256sum -c SHA256SUMS)
# Fix all archive metadata; gzip -n omits its timestamp and original filename.
archive="plumb-${version}-linux-amd64.tar.gz"
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
  -C "$stage" -cf - plumb plumb-agent install.sh SHA256SUMS | gzip -n > "$output/$archive"
(cd "$output" && sha256sum "$archive" > SHA256SUMS && sha256sum -c SHA256SUMS)
