#!/usr/bin/env bash
set -euo pipefail

# The mise task starts the stack first. Keep the generator from recreating
# dependencies so another run can use an already running service unchanged.
mkdir -p results
docker compose run --rm --no-deps --user "$(id -u):$(id -g)" \
  -e TEST_MODE="${TEST_MODE:-load}" -e RATE="${RATE:-100}" -e DURATION="${DURATION:-2m}" \
  -e VUS="${VUS:-20}" -e MAX_VUS="${MAX_VUS:-100}" \
  -e P95_MS="${P95_MS:-250}" -e P99_MS="${P99_MS:-500}" \
  -e RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}" \
  -e GIT_COMMIT="$(git rev-parse HEAD)" \
  -e GIT_CHANGED_FILES="$(git status --porcelain | wc -l | tr -d ' ')" \
  -e DOCKER_INFO="$(docker info --format '{"architecture":{{json .Architecture}},"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"server_version":{{json .ServerVersion}}}')" k6
