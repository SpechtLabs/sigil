#!/usr/bin/env bash
set -euo pipefail

# The mise task starts the stack first. Keep the generator from recreating
# dependencies so another run can use an already running service unchanged.
#
# Every knob lib/config.js reads is passed through; unset ones keep the
# suite's defaults. SEED is picked here, once, so every VU shares it and the
# report can name it for a replay.
mkdir -p results
knobs=(RATE DURATION BREAKPOINT_MAX_RATE WEBHOOK_SHARE MAX_BATCH RELOADS_PER_MINUTE RELOAD_CONCURRENCY DISPATCH_FAILURES
  VUS MAX_VUS P95_MS P99_MS WEBHOOK_P95_MS WEBHOOK_P99_MS)
env_args=()
for knob in "${knobs[@]}"; do
  if [[ -n "${!knob:-}" ]]; then
    env_args+=(-e "$knob=${!knob}")
  fi
done

mode="${TEST_MODE:-load}"

# Collected before the bundle alternation below adds a file of its own.
git_commit="$(git rev-parse HEAD)"
git_changed_files="$(git status --porcelain | wc -l | tr -d ' ')"

# Every mode but smoke alternates the team bundle alertrouter serves between
# good and broken while k6 reloads it: every BAD_BUNDLE_EVERY seconds a
# document that doesn't parse appears in the mounted policies/teams, and as
# long again later it's gone. alertrouter must reject each broken bundle and
# keep serving the last good one, reloads and routing under load included.
# The document is removed however the run ends.
bad_bundle_every="${BAD_BUNDLE_EVERY:-15}"
if [[ "$mode" == smoke ]]; then
  bad_bundle_every=0
fi
# Keep the name in lockstep with badBundleFile in lib/config.js.
broken="policies/teams/loadtest-broken.sigil"
rm -f "$broken"
if ((bad_bundle_every > 0)); then
  (
    while :; do
      sleep "$bad_bundle_every"
      printf 'policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n' >"$broken"
      sleep "$bad_bundle_every"
      rm -f "$broken"
    done
  ) &
  alternating=$!
  trap 'kill "$alternating" 2>/dev/null || true; wait "$alternating" 2>/dev/null || true; rm -f "$broken"' EXIT
fi

docker compose run --rm --no-deps --user "$(id -u):$(id -g)" \
  -e TEST_MODE="$mode" \
  -e SEED="${SEED:-$((RANDOM * 32768 + RANDOM + 1))}" \
  -e RUN_ID="${RUN_ID:-$mode-$(date -u +%Y%m%dT%H%M%SZ)}" \
  -e BAD_BUNDLE_EVERY="$bad_bundle_every" \
  ${env_args[@]+"${env_args[@]}"} \
  -e GIT_COMMIT="$git_commit" \
  -e GIT_CHANGED_FILES="$git_changed_files" \
  -e DOCKER_INFO="$(docker info --format '{"architecture":{{json .Architecture}},"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"server_version":{{json .ServerVersion}}}')" k6
