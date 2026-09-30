#!/usr/bin/env bash
set -euo pipefail

# The mise task starts the stack first. Keep the generator from recreating
# dependencies so another run can use an already running service unchanged.
#
# Every knob lib/config.js reads is passed through; unset ones keep the
# suite's defaults. SEED is picked here, once, so every VU shares it and the
# report can name it for a replay.
mkdir -p results
knobs=(RATE DURATION BREAKPOINT_MAX_RATE WEBHOOK_SHARE MAX_BATCH RELOADS_PER_MINUTE RELOAD_CONCURRENCY
  VUS MAX_VUS P95_MS P99_MS WEBHOOK_P95_MS WEBHOOK_P99_MS)
env_args=()
for knob in "${knobs[@]}"; do
  if [[ -n "${!knob:-}" ]]; then
    env_args+=(-e "$knob=${!knob}")
  fi
done

mode="${TEST_MODE:-load}"
docker compose run --rm --no-deps --user "$(id -u):$(id -g)" \
  -e TEST_MODE="$mode" \
  -e SEED="${SEED:-$((RANDOM * 32768 + RANDOM + 1))}" \
  -e RUN_ID="${RUN_ID:-$mode-$(date -u +%Y%m%dT%H%M%SZ)}" \
  ${env_args[@]+"${env_args[@]}"} \
  -e GIT_COMMIT="$(git rev-parse HEAD)" \
  -e GIT_CHANGED_FILES="$(git status --porcelain | wc -l | tr -d ' ')" \
  -e DOCKER_INFO="$(docker info --format '{"architecture":{{json .Architecture}},"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"server_version":{{json .ServerVersion}}}')" k6
