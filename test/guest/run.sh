#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
containers=()
images=()
test_pid=''
cleanup() {
  if [[ -n "$test_pid" ]]; then kill "$test_pid" >/dev/null 2>&1 || true; fi
  for name in "${containers[@]}"; do docker rm -f "$name" >/dev/null 2>&1 || true; done
  for image in "${images[@]}"; do docker rmi "$image" >/dev/null 2>&1 || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
run_id=${GUEST_RUN_ID:-$$}
CGO_ENABLED=0 GOOS=linux go test -c -tags=guest -o "$tmp/guest.test" ./test/guest
for distro in \
  debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6 \
  ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c; do
  image="plumb-guest-${distro%%@*}-$run_id"
  image="${image//:/-}"
  name="$image"
  images+=("$image")
  docker build --build-arg "DISTRO=$distro" -t "$image" test/guest
  containers+=("$name")
  # Privileged mode gives systemd a writable private cgroup on cgroup v2.
  # Only the read-only test binary is mounted; host system paths need review.
  docker run -d --name "$name" --privileged --cgroupns=private \
    --tmpfs /run --tmpfs /run/lock \
    -v "$tmp/guest.test:/guest.test:ro" "$image"
  settings=$(docker inspect -f '{{.HostConfig.CgroupnsMode}} {{len .Mounts}}' "$name")
  if [[ "$settings" != "private 1" ]]; then echo "unexpected container settings: $settings" >&2; exit 1; fi
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
  echo "$distro systemd state: $state"
  if [[ "$state" == degraded ]]; then docker exec "$name" systemctl --failed --no-legend; fi
  docker exec "$name" /guest.test -test.v -test.timeout=5m "$@" &
  test_pid=$!
  test_status=0
  wait "$test_pid" || test_status=$?
  test_pid=''
  if ((test_status != 0)); then exit "$test_status"; fi
  docker rm -f "$name" >/dev/null
done
cleanup
if [[ -n $(docker ps -aq --filter name=plumb-guest-) || -n $(docker images -q "plumb-guest-*-$run_id") ]]; then
  echo "guest containers or images remain after cleanup" >&2
  exit 1
fi
