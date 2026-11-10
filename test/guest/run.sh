#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
containers=()
cleanup() {
  for name in "${containers[@]}"; do docker rm -f "$name" >/dev/null 2>&1 || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
CGO_ENABLED=0 GOOS=linux go test -c -tags=guest -o "$tmp/guest.test" ./test/guest
for distro in debian:13 ubuntu:26.04; do
  image="plumb-guest-${distro//:/-}-$$"
  name="$image"
  docker build --build-arg "DISTRO=$distro" -t "$image" test/guest
  containers+=("$name")
  # Privileged mode gives systemd a writable private cgroup on cgroup v2.
  # Only the read-only test binary is mounted; host system paths need review.
  docker run -d --name "$name" --privileged --cgroupns=private \
    --tmpfs /run --tmpfs /run/lock \
    -v "$tmp/guest.test:/guest.test:ro" "$image"
  ready=false
  for ((attempt=0; attempt<60; attempt++)); do
    state=$(docker exec "$name" systemctl is-system-running 2>/dev/null || true)
    if [[ "$state" == running || "$state" == degraded ]]; then ready=true; break; fi
    sleep 1
  done
  if [[ "$ready" != true ]]; then
    docker logs "$name"
    echo "systemd did not boot in $distro" >&2
    exit 1
  fi
  docker exec "$name" /guest.test -test.v -test.timeout=5m "$@"
  docker rm -f "$name" >/dev/null
done
