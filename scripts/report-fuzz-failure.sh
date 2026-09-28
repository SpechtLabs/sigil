#!/usr/bin/env bash
# Called only by the extended campaign's reporting job, which can write issues.
set -euo pipefail

[ "${GITHUB_REF:-}" = refs/heads/main ] || exit 0
case "${GITHUB_EVENT_NAME:-}" in
  schedule|workflow_dispatch) ;;
  *) exit 0 ;;
esac
if [ "${TARGETS_RESULT:-}" != failure ] && [ "${FUZZ_RESULT:-}" != failure ]; then
  exit 0
fi

repository=${GITHUB_REPOSITORY:?}
repository_url="${GITHUB_SERVER_URL:?}/$repository"
run_url="$repository_url/actions/runs/${GITHUB_RUN_ID:?}/attempts/${GITHUB_RUN_ATTEMPT:?}"

# Use a marker and the paginated issues API, avoiding search-index delays and
# duplicate issues when a title changes. A failed lookup must stop publication.
issues=$(gh api --paginate "repos/$repository/issues?state=open&per_page=100" \
  --jq '.[] | select(.pull_request == null) | select((.body // "") | contains("<!-- sigil:extended-fuzzing -->")) | .number')
issue=$(printf '%s\n' "$issues" | awk 'NR == 1 {print}')

body=$(mktemp)
trap 'rm -f "$body"' EXIT
cat > "$body" <<EOF
<!-- sigil:extended-fuzzing -->
The extended fuzz campaign on main failed.

- [Workflow run and job logs]($run_url)
- [Tested commit]($repository_url/commit/${GITHUB_SHA:?})
- Trigger: ${GITHUB_EVENT_NAME}
- Target discovery: ${TARGETS_RESULT:?}
- Fuzz jobs: ${FUZZ_RESULT:?}
- Requested duration per target: ${FUZZTIME:?}, with two workers
- [Campaign artifacts]($repository_url/actions/runs/${GITHUB_RUN_ID}#artifacts)

Inspect the failed jobs to distinguish a fuzz finding from a setup or timeout failure.
Download their fuzz artifacts for logs and any minimized inputs. Setup failures and
job timeouts may leave no artifact; the job logs remain linked above.

For a minimized input, copy it into the corresponding package's testdata/fuzz
directory and run the reproduction command printed in fuzz.log. Keep the input
with its fix so ordinary tests replay it.
EOF

if [ -n "$issue" ]; then
  gh issue comment "$issue" --repo "$repository" --body-file "$body"
else
  gh issue create --repo "$repository" --title 'Extended fuzzing failed on main' --body-file "$body"
fi
