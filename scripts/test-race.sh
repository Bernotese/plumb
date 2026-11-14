#!/usr/bin/env bash
# The race job (T-211): every package under -race, with internal/api (the
# longest one, close to the 10 min package timeout on a slow runner) split
# into shards that run beside the other packages. The shards are made at
# run time from `go test -list`, so a new test lands in one of them; the
# script then checks that the shards ran exactly the listed tests.
# The logs stay in a mktemp dir (a CI runner is thrown away; locally they
# are a few small files under /tmp).
set -euo pipefail
cd "$(dirname "$0")/.."
shards=${RACE_API_SHARDS:-2}
api=./internal/api
logs=$(mktemp -d)
# RACE_FLAG= (empty) runs the same shards without the detector, to check
# the script on a machine without a C compiler.
race=${RACE_FLAG--race}
flags=(-count=1)
if [[ -n $race ]]; then
  flags+=("$race")
  export CGO_ENABLED=1
fi

mapfile -t others < <(go list ./... | grep -v '/install$' | grep -v '/internal/api$')
# Test, Fuzz (its seed corpus) and Example functions run in `go test`.
mapfile -t names < <(go test -list '.*' "$api" | grep -E '^(Test|Fuzz|Example)' | sort)
if [[ ${#names[@]} -eq 0 ]]; then
  echo "test-race: no tests listed in $api" >&2
  exit 1
fi

pids=()
labels=()
start=$SECONDS
(
  rc=0
  go test "${flags[@]}" "${others[@]}" >"$logs/others.log" 2>&1 || rc=$?
  echo "$rc" >"$logs/others.rc"
  echo $((SECONDS - start)) >"$logs/others.s"
) &
pids+=($!)
labels+=(others)
for ((i = 0; i < shards; i++)); do
  part=()
  for ((j = i; j < ${#names[@]}; j += shards)); do part+=("${names[j]}"); done
  re="^($(
    IFS='|'
    echo "${part[*]}"
  ))\$"
  echo "${#part[@]}" >"$logs/api$i.n"
  (
    rc=0
    go test "${flags[@]}" -v -run "$re" "$api" >"$logs/api$i.log" 2>&1 || rc=$?
    echo "$rc" >"$logs/api$i.rc"
    echo $((SECONDS - start)) >"$logs/api$i.s"
  ) &
  pids+=($!)
  labels+=("api$i")
done
for p in "${pids[@]}"; do wait "$p" || true; done

fail=0
ran=0
grep -E '^(ok|FAIL|---)' "$logs/others.log" | grep -v -- '--- PASS' || true
for l in "${labels[@]}"; do
  rc=$(<"$logs/$l.rc")
  echo "test-race: $l exit $rc after $(<"$logs/$l.s") s"
  if [[ $rc != 0 ]]; then
    fail=1
    grep -E -- '--- FAIL|^FAIL|panic:|timed out' "$logs/$l.log" | head -50 || true
    tail -n 40 "$logs/$l.log"
  fi
  if [[ $l == api* ]]; then
    n=$(grep -cE '^--- (PASS|SKIP): (Test|Fuzz|Example)' "$logs/$l.log" || true)
    echo "test-race: $l ran $n of $(<"$logs/$l.n") tests"
    ran=$((ran + n))
  fi
done
echo "test-race: $api ran $ran of ${#names[@]} listed tests in $shards shards"
if [[ $fail == 0 && $ran != "${#names[@]}" ]]; then
  echo "test-race: the shards did not run every test of $api" >&2
  exit 1
fi
exit "$fail"
