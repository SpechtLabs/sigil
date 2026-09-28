#!/usr/bin/env bash
# Discover every Go fuzz target. FUZZTIME is per target, not per package.
set -euo pipefail

cd "$(dirname "$0")/.."
list=false
if [ "${1:-}" = "--list" ]; then
  list=true
  shift
fi
if [ "$#" -eq 0 ]; then
  set -- ./...
fi

targets=$(mktemp)
trap 'rm -f "$targets"' EXIT
packages=$(go list -f '{{.ImportPath}} {{.Dir}}' "$@")
while read -r package directory; do
  for file in "$directory"/*_test.go; do
    [ -f "$file" ] || continue
    while read -r target; do
      printf '%s %s\n' "$package" "$target" >> "$targets"
    done < <(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' "$file")
  done
done <<< "$packages"

if [ ! -s "$targets" ]; then
  echo "No fuzz targets found" >&2
  exit 1
fi

if "$list"; then
  cat "$targets"
  exit 0
fi

while read -r package target; do
  echo "Fuzzing $package/$target for ${FUZZTIME:-10s}"
  go test "$package" -run '^$' -fuzz "^${target}$" \
    -fuzztime "${FUZZTIME:-10s}" -parallel "${FUZZPARALLEL:-2}" \
    -timeout "${FUZZTIMEOUT:-30m}"
done < "$targets"
