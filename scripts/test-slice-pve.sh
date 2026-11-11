#!/usr/bin/env bash
# HTTP slice acceptance. Run after driver tests, never concurrently.
# All settings and image pins stay in external files. Never enable tracing.
set +x
set -euo pipefail
env_file=${PLUMB_TEST_ENV:-$HOME/.config/plump/testhost.env}
set -a
if [[ -r $env_file ]]; then
	# shellcheck disable=SC1090
	source "$env_file"
fi
if [[ -n ${PLUMB_TEST_IMAGES_FILE:-} && -r $PLUMB_TEST_IMAGES_FILE ]]; then
	# shellcheck disable=SC1090
	source "$PLUMB_TEST_IMAGES_FILE"
fi
set +a
go test -tags pve ./test/pve/... -count=1 -v -timeout 60m
