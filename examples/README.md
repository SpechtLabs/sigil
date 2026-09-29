# deploygate: a Go service built on Sigil

deploygate is a small deploy approval API. A client posts the release it wants to ship, and deploygate works out which roles the requestor holds, evaluates the team's Sigil policy with those roles and answers with a decision: approve, review or deny, each with a reason, a typed payload and a trace of how the policy got there. It's the running example from the [documentation](../docs/getting-started/tour.md), turned into a service you can start with one command.

The example is a separate Go module, `github.com/spechtlabs/sigil/examples`, that builds against the Sigil checkout it lives in. Its dependencies never reach the library's `go.mod`. Start with [Run it](#run-it); [Layout](#layout) and [How it is wired](#how-it-is-wired) near the end map the directories and packages.

## What the example shows

- **A kind defined in Go.** `internal/deploy` declares the `DeployApproval` kind with `policy.NewKind`: the input structs, the decisions and their reasons, the precedence, the default and the `split` host function. See [Define the kind](../docs/guides/embed-go.md#define-the-kind).
- **An exported kind file.** The service's own `sigilc` binary writes the kind to `policies/deploy_approval.sigil`, and a test fails when that copy is stale. See [Export the kind](../docs/guides/host-binary.md#export-the-kind).
- **Guardrails no team can remove.** The platform's documents are embedded in the binary and passed to `policy.Require("deploy.guardrails", policy.From(platformFS))`, so a team policy has to invoke the guardrails unconditionally and can't redefine them. See [Trusted sources](../docs/reference/bundles.md#trusted-sources).
- **Policies loaded from directories.** The team policies and the access policy each come from a directory, a mounted ConfigMap in a cluster, and reload in place. A bundle that doesn't compile never replaces the one that serves. See [Policies in a ConfigMap](../docs/guides/configmaps.md) and [Reload without an outage](../docs/guides/configmaps.md#reload-without-an-outage).
- **A second, collecting kind.** `internal/access` declares `AccessGrant`, a `collect all` kind that grants roles. Its outcome feeds the deploy policy's `actor.roles`, so a client can't claim a role, and it shows both ways a collecting kind says no: a host-declared `exclusive` line and a separation-of-duties assert in a required policy.
- **Typed matching.** The handler matches deploy results with `deploy.Review.Match` and `deploy.Approve.Match`, and access results with `access.Deployer.MatchAll` and friends, and gets `ReviewData`, `ApproveData` and `GrantData` back, not maps. See [Typed matching](../docs/reference/go-api.md#typed-matching).
- **A complete local observability stack.** Alloy sends metrics to Mimir, traces to Tempo and JSON logs to Loki. Pyroscope collects every profile type supported by its Go SDK. Grafana connects decisions, logs, traces and profiles in one provisioned dashboard. k6 exercises both policy stages and records throughput, latency and correctness.

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
cd examples
mise install
mise run up
```

The tasks in [`.mise.toml`](./.mise.toml) run from `examples/` and inherit tool versions from the root config. All commands below run from `examples/`. To run a task from the repository root, use `mise -C examples run <task>`.

`up` builds the image and runs `docker compose up --build --wait`, which returns once deploygate and all telemetry backends are ready. deploygate counts as healthy once both bundles have loaded: the image has no shell, so compose runs `/deploygate healthcheck`, which asks the server's own `/readyz` and exits 0 on `200`. The stack publishes these ports:

| Service | Image | URL | What's there |
| --- | --- | --- | --- |
| deploygate | built from `Dockerfile` | <http://localhost:8080> | The API, `/healthz`, `/readyz` and `/metrics` |
| Alloy | `grafana/alloy:v1.20.0` | <http://localhost:12345>, OTLP `4317`/`4318` | Scrapes metrics, forwards traces and reads deploygate's Docker logs |
| Tempo | `grafana/tempo:3.0.3` | <http://localhost:3200> | Trace API; explore traces through Grafana |
| Loki | `grafana/loki:3.7.8` | <http://localhost:3100> | Log API; explore JSON decision logs through Grafana |
| Mimir | `grafana/mimir:3.2.1` | <http://localhost:9009/prometheus> | Prometheus-compatible query API for service and k6 metrics |
| Pyroscope | `grafana/pyroscope:2.3.1` | <http://localhost:4040> | CPU, memory, goroutine, mutex, blocking and leak profiles |
| Grafana | `grafana/grafana:13.2.2` | <http://localhost:3000/d/deploygate> | The provisioned dashboard, no login needed |
| k6 | `grafana/k6:2.3.0` | No listening port | Optional load generator; runs with `mise run loadtest` |

Each backend runs as one process with filesystem storage in named volumes. Mimir uses its classic ingestion path, so the stack needs neither Kafka nor object storage. Alloy's Docker discovery is restricted to this Compose project's deploygate container; it reads logs through the mounted Docker socket. `mise run down` stops the stack and drops its volumes. [Observe it](#observe-it) covers the dashboard and the load tests.

The compose image reports the version `compose`:

```bash
docker compose exec deploygate /deploygate version
```

```text
deploygate compose
```

### Without Docker

deploygate runs on its own too. With no `--policies` and no `--access-policies` it serves the bundles embedded in the binary; pointing the flags at `policies/teams` and `policies/access` serves the files in your checkout and reloads them when they change. Spans are exported only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, so no collector is needed:

```bash
go run ./cmd/deploygate serve \
  --policies policies/teams --access-policies policies/access --log-format console
```

`go run ./cmd/deploygate version` prints `deploygate dev`; release builds get their version from GoReleaser.

### Use the deployment CLI

`demo-cli` stands in for the platform tooling around deploygate. In a deployment system, that tooling would look up the actor's identity, service metadata and release history. Here, named scenarios supply those inputs and send them to the running service for a decision.

```bash
mise run demo deploy owner
```

```text
HTTP 202 Accepted
REVIEW: service_owner
Team: payments
Policy: payments.production
Approvers: ["payments-leads","security-leads"]

Access policy: access.main
Roles:
  reader: team_member
  deployer: team_member, expires in 8h
```

The CLI shows the decision and reason, approvers or bake time, and the roles granted by the access policy. Add `--explain` to see the candidates, winning rules, conditions and source locations. Conflicts, failed asserts and error advice appear even without it.

| Command | What it does |
| --- | --- |
| `mise run demo scenarios` | List the built-in scenarios |
| `mise run demo deploy sre --explain` | Ask for the on-call SRE's deployment and explain its approval |
| `mise run demo deploy short-soak --explain` | Show why a two-hour soak is denied |
| `mise run demo access member` | Check a team member's roles |
| `mise run demo access outsider` | Show an empty access outcome |
| `mise run demo policies` | List both loaded bundles and their load times |
| `mise run demo policies reload` | Reload both bundles |
| `mise run demo status` | Check readiness |
| `mise run demo metrics` | Print Prometheus metrics |

`deploy` defaults to `owner`, and `access` to `member`. Run `mise run demo -- --help` or `mise run demo -- deploy --help` for flags. `--url` overrides `DEPLOYGATE_URL`, which defaults to `http://localhost:8080`; `--timeout` defaults to `10s`.

The scenarios embed the files under [`requests/`](./requests), so a built CLI works from any directory. To try your own input, copy a request, edit it and pass `--file`. Deployments also need `--team`; access requests carry their team in the JSON. `--file -` reads stdin.

```bash
mise run demo deploy --team payments --file requests/owner.json
mise run demo access --file requests/access-member.json
```

Add `--json` for the full API response, including error responses, on stdout. The detailed examples below use it to show the wire format; omit it for the readable summary. No JSON formatting tool is needed.

The binary exits `0` for approvals, review requests, access grants and successful operations; `2` for denials, empty grants and failed evaluations, conflicts and timeouts included, whether they answer `422`, `500` or `503`; and `1` for usage, connection and other HTTP errors, a `500` or `503` without a decision among them. A review still requires approval from the named approvers. For scripts that need those exact exit codes, build with `mise run build` and use `./bin/demo-cli`. The `mise run demo` task uses `go run`, which reports any nonzero child exit as its own exit code `1`.

The integration suite checks the request files against the service, and the CLI tests run every scenario through the real server in process.

### A review

A payments engineer ships a PCI-scoped service their team owns:

```bash
mise run demo deploy owner --json
```

deploygate answers `202 Accepted`. The body, formatted:

```json
{
  "team": "payments",
  "policy": "payments.production",
  "decision": "review",
  "reason": "service_owner",
  "payload": {"approvers": ["payments-leads", "security-leads"]},
  "trace": [
    {
      "decision": "review",
      "reason": "service_owner",
      "policy": "deploy.production",
      "location": "payments/production.sigil:10:3 → deploy/production.sigil:16:5",
      "conditions": [
        "service.labels[\"compliance\"] == \"pci\"",
        "cleared",
        "service.tier in [\"standard\", \"internal\"] and owns_service"
      ],
      "payload": {"approvers": ["payments-leads", "security-leads"]},
      "winner": true
    }
  ],
  "access": {
    "policy": "access.main",
    "grants": [
      {"role": "reader", "reason": "team_member", "policy": "access.main", "location": "main.sigil:9:3 (access.main)"},
      {"role": "deployer", "reason": "team_member", "ttl": "8h", "policy": "access.main", "location": "main.sigil:10:3 (access.main)"}
    ]
  }
}
```

The review comes from the platform's `deploy.production`, reached through the call on line 10 of the payments policy, and `conditions` lists every `when` that held on the way. The `access` block shows the roles the deploy policy was given: the engineer is in the `payments` group, so `access.main` made them a reader and, for eight hours, a deployer. Durations on the wire are strings in Sigil syntax, such as `"6h"` or `"15m"`, in requests and payloads alike.

The HTTP status encodes the decision, so a client can act on the status alone:

| Status | Meaning |
| --- | --- |
| `200 OK` | `approve`, with the `bake` time in the payload |
| `202 Accepted` | `review`, with the `approvers` in the payload |
| `403 Forbidden` | `deny` |
| `422 Unprocessable Entity` | The request failed an input assert, in either stage: the policy declared it invalid before any rule ran, and the caller has to fix it. The decision fields hold the kind's default, `deny` / `no_rule_matched`, `asserts` lists what failed and `error` says what went wrong |
| `500 Internal Server Error` | The policy failed on a valid request, in either stage: a conflict, such as the access policy granting two roles the kind declares exclusive, a failed outcome assert, or a runtime error, a panicking host function included. The body is the same as for `422`, with `conflict` naming both sides of a conflict; the policy's owners have to fix it, not the caller |
| `503 Service Unavailable` | An evaluation ran past the evaluation timeout, one second by default, in either stage: deploygate didn't decide in time. The body is the same as for `500`. Also the answer, with only an `error`, while the two bundles aren't both loaded yet |
| `400 Bad Request` | The body isn't valid JSON, has an unknown field such as `roles`, or has a duration that doesn't parse, is too long for a Go duration or, for `release.soak`, is negative |
| `404 Not Found` | deploygate doesn't serve that team |
| `499` | The client closed the request before the answer. Nothing is written, since no one reads it; the status is for the access log and the request metrics |

A failed evaluation is `422`, `500` or `503` by whose fault it is, not by which stage it happened in. A `4xx` tells the client to change its request, and most SLOs leave `4xx` out of the error budget, so a conflict in the policy answered with a `4xx` would fail every affected request without ever showing up as deploygate's error. Whether a failed assert is the caller's or the policy's comes from the error's assert phase, not from the trace: an outcome assert that fails when no rule fired leaves the trace as empty as a failed input assert does, and it's still a `500`. When a `500` or a `503` comes from a failed evaluation, its body carries the fallback decision and the details; one without a `policy` field in the body, such as a failed reload, isn't an evaluation.

The last two rows are about time. deploygate evaluates under a deadline, `--evaluation-timeout`, because an input can make a policy slow: nested quantifiers over long lists multiply, and nothing in the language bounds the work. Sigil checks the context as it goes and stops at the deadline with the fallback, so a slow input costs at most the timeout. That's deploygate failing to answer, not the request being wrong, so it's a `5xx` that counts against the error budget, and `503` rather than `504`, which would say an upstream server didn't answer when deploygate evaluates in process. A client that gives up first cancels the request; Sigil stops the same way, and deploygate answers `499`, nginx's "client closed request". That isn't anyone's failure, so it's neither a `4xx` the client caused nor a `5xx` that burns the error budget, and it isn't counted as an evaluation error.

### A deny

The same engineer ships a release that soaked for two hours. The payments policy lowers the platform's minimum soak to four hours, so the guardrail denies it:

```bash
mise run demo deploy short-soak --json
```

The status is `403 Forbidden`. Excerpt from the response:

```json
{
  "decision": "deny",
  "reason": "soak_too_short",
  "trace": [
    {"decision": "deny", "reason": "soak_too_short", "policy": "deploy.guardrails", "winner": true},
    {"decision": "review", "reason": "service_owner", "policy": "deploy.production", "winner": false}
  ]
}
```

The review still fired, and the trace shows it, but the kind ranks `deny` above `review`, so the guardrail wins.

### A failed assert

The platform's access guardrails assert that every request names its actor, since every grant is recorded against a name. A request with an empty `actor.name` fails that assert in the access stage, and the deploy policy never runs:

```bash
mise run demo deploy unnamed-actor --json
```

The status is `422 Unprocessable Entity`, and the decision is the deploy kind's default, which is what a host that fails closed acts on. Excerpt from the response:

```json
{
  "decision": "deny",
  "reason": "no_rule_matched",
  "access": {"policy": "access.main", "grants": []},
  "asserts": [
    {"reason": "named_actor", "policy": "access.guardrails", "location": "main.sigil:6:1 (access.main) → access/guardrails.sigil:6:1"}
  ]
}
```

The location is the call chain: the assert is written in `access.guardrails` and runs because `access.main` invokes the guardrails on line 6. The full body also carries an `error` object, in the same shape as every other error the API returns:

```json
{
  "message": "the request fails access.main's asserts: named_actor",
  "advice": [
    "the deploy policy didn't run; the decision fields hold the fallback, deny",
    "fix the request so the asserts listed in asserts hold, then ask again"
  ],
  "cause": {"message": "assertion \"named_actor\" failed at main.sigil:6:1 (access.main) → access/guardrails.sigil:6:1"}
}
```

The checkout policy asserts `named_actor` too, for the host that evaluates it without an access stage; in deploygate the access stage always gets there first.

## An authorization layer with collect all

A deploy policy decides whether this actor may ship this release. It shouldn't also have to work out what the actor is allowed to do, and it certainly shouldn't take the client's word for it, which is what a request carrying `"roles": ["release_manager"]` amounts to. deploygate splits the two questions. A second kind, `AccessGrant`, decides which roles the actor holds for one team's services in one environment, and the deploy policy reads those roles as `actor.roles`.

### Why roles need a collecting kind

`DeployApproval` collects one decision: a deny beats a review beats an approval, and the host acts on the winner. Roles don't compete like that. A payments engineer on the platform team is a reader, a deployer and a release manager at once, and none of those outranks another. `AccessGrant` declares `collect all`, so every role whose rule fires is part of the outcome:

```sigil
decision reader {
  team_member
  everyone_in_staging
}

decision deployer(ttl: duration = 8h) {
  team_member
  oncall
}

collect all
exclusive admin, release_manager
```

The outcome comes back in the kind's declaration order, then by source position, and grants aren't merged: a team member in staging is a reader for two reasons, and the host gets both. The kind is defined in `internal/access` and exported to [`policies/access_grant.sigil`](./policies/access_grant.sigil); the reference covers [collecting kinds](../docs/reference/kind-files.md#collecting-kinds) and [how their outcome resolves](../docs/reference/evaluation.md#collecting-kinds).

### Two ways to say no

A collecting kind has no deny that outranks the rest, so the combinations of roles that must never happen need another guard. The example has one in each of the two places the docs describe, on purpose.

- **The host's `exclusive` line.** `admin` already covers everything a release manager can do, so a policy that grants both contradicts itself. The Go kind declares `policy.WithExclusive(access.Admin, access.ReleaseManager)`, and an evaluation where both fire fails with a `*policy.ConflictError` instead of handing out two grants. `access.main` respects it by construction, since its platform rule leaves admins out, but a break-glass member who is also in `platform` still trips it: the two groups aren't meant to overlap, and a conflict is how the host finds out that they do.
- **The platform's separation-of-duties assert.** `access.guardrails`, which the host requires of every access policy, asserts `[auditor, deployer] exclusive in outcome`: whoever audits a team's deploys can't also deploy them. It's an outcome assert, so it runs once every role is collected and catches a deployer grant from any rule.

What differs is who owns the rule. The [`exclusive` line](../docs/reference/kind-files.md#exclusive) is part of the contract compiled into the host, so changing it takes a host release. The assert is platform policy that ships and reloads like any other document, and holds because the host [requires](../docs/reference/evaluation.md#required-policies) the guardrails.

### Two stages per deploy

A deployment request describes the actor the way an identity provider would, with `groups` and a `clearance`, and without roles:

```json
"actor": {"name": "ada", "groups": ["payments"], "clearance": "", "regions": ["eu", "us"]}
```

deploygate evaluates `access.main` for the actor, the team and the environment first, and turns the grants into deploy roles: `deployer` becomes `deployer`, `release_manager` becomes `release_manager`, `admin` becomes both, and `reader` and `auditor` add nothing. Then it evaluates the team's deploy policy with those roles, and with the actor's groups as `actor.teams`. The handler reads the access result with [`MatchAll`](../docs/reference/go-api.md#typed-matching), one call per role, the way a host reads a collecting kind, since a role can be granted more than once. From `internal/server/render.go`:

```go
for _, m := range access.Deployer.MatchAll(res) {
	out = append(out, grant(res, access.Deployer.Name(), m.Reason, m.Policy, m.Position, ttl(m.Payload.TTL)))
}
```

`m.Payload` is a typed `GrantData`, so the time to live arrives as a `time.Duration`, not as a map entry to cast.

A client that still sends `roles` or `teams` gets a `400`: unknown fields are rejected, and a client that believes it picked its own roles is the bug this layer exists to prevent.

The on-call SRE shows both stages at work. `linus` is in `payments-sre` only, so the access policy grants no team membership but makes them a deployer for two hours, and the deploy policy's own SRE rule approves:

```bash
mise run demo deploy sre --json
```

Excerpt from the response:

```json
{
  "decision": "approve",
  "reason": "payments_sre",
  "payload": {"bake": "15m"},
  "access": {"grants": [{"role": "deployer", "reason": "oncall", "ttl": "2h"}]}
}
```

An actor the access policy grants nothing isn't an error. The deploy policy runs without roles and its guardrail denies the deploy as `not_eligible`, and the empty `access.grants` shows why.

### Asking for access directly

`POST /api/v1/access/grants` runs the access stage on its own, for a UI that shows someone what they may do, or a CLI that checks before it asks for a deploy. The body names the actor, the team and the environment; a body without a team, or with a field it doesn't know, gets a `400`:

```bash
mise run demo access member --json
```

The status is `200 OK`, because at least one role was granted. Excerpt from `grants`:

```json
[
  {"role": "reader", "reason": "team_member"},
  {"role": "deployer", "reason": "team_member", "ttl": "8h"}
]
```

`ttl` is left out of a grant whose role doesn't expire. The full body also has `policy`, `team`, `environment` and a `trace` in which every candidate is marked as a winner: a collecting kind's outcome is everything that survived.

The same body with an empty `grants` list comes back as `403 Forbidden` when nothing fired, as for `mise run demo access outsider`, a member of `marketing` asking for payments in production:

```json
{"policy": "access.main", "team": "payments", "environment": "production", "grants": [], "trace": []}
```

A break-glass member who is also in `platform` trips the kind's `exclusive` line, and the answer is `500 Internal Server Error` naming both sides:

```bash
mise run demo access break-glass-platform --json
```

Excerpt from the response:

```json
{
  "grants": [],
  "conflict": {"candidates": [
    {"decision": "admin", "reason": "break_glass", "location": "main.sigil:36:3 (access.main)"},
    {"decision": "release_manager", "reason": "platform_member", "location": "main.sigil:25:3 (access.main)"}
  ]}
}
```

No role is granted, and the error's advice says so: act as if `grants` were empty, and tell the policy's owners, because a conflict is a defect in the policy rather than the request. That's also why it's a `500`: the request was fine. A payments engineer who is also in `compliance` fails the separation-of-duties assert instead. It's an outcome assert, which judges the roles the policy granted rather than the request, so it's a `500` too:

```bash
mise run demo access compliance-member --json
```

Excerpt from the response:

```json
{
  "grants": [],
  "asserts": [
    {"reason": "sod_auditor_deployer", "policy": "access.guardrails", "location": "main.sigil:6:1 (access.main) → access/guardrails.sigil:11:1"}
  ],
  "trace": [{"decision": "reader"}, {"decision": "deployer"}, {"decision": "auditor"}]
}
```

The trace still shows the three roles that fired, so it's clear which two couldn't stand together. Both failures end a deployment the same way, before the deploy policy runs: the deployments endpoint answers `500` with the fallback `deny` and an empty `access.grants`. An actor without a name fails the access guardrails' input assert instead, and that's the caller's to fix, so both endpoints answer it with `422`.

## Watch it reload

The compose stack bind-mounts `policies/teams` into the container as `/etc/deploygate/policies` and `policies/access` as `/etc/deploygate/access`, so the policies deploygate serves are the files in your checkout. It reloads both bundles when you ask and on `SIGHUP`, and each one whenever a poll finds that its content changed. Compose sets `DEPLOYGATE_RELOAD_INTERVAL` to 5 seconds so you can watch it happen; the default is 30.

Change the payments SRE bake from 15 minutes to 30 in `policies/teams/payments/production.sigil`:

```sigil
when cleared and "payments-sre" in actor.teams {
  approve(payments_sre, bake: 30m)
}
```

Wait for the next poll or reload right away, then ask for an SRE deploy:

```bash
mise run demo policies reload
mise run demo deploy sre
```

The CLI now reports `Bake: 30m`.

The access bundle reloads the same way. Give the on-call SRE three hours instead of two in `policies/access/main.sigil`, `deployer(oncall, ttl: 3h)`, reload, and the same request's `access` block shows `"ttl": "3h"`.

Now break the file. Delete the closing `}` of the last `when`, and reload again:

```bash
mise run demo policies reload
```

The reload answers `500 Internal Server Error`. The error's message says what happened, and its cause holds the compiler's diagnostics, pointing at the file, line and column:

```text
the DeployApproval policies from /etc/deploygate/policies don't load: payments.production failed to compile, so the previous bundle keeps serving
```

deploygate keeps serving the bundle it loaded last, so the SRE deploy above still reports `Bake: 30m`, and `mise run demo policies` still reports the team bundle's earlier `loaded_at`. The failure shows up in the metrics, and on the dashboard's "Reload health", "Failed reloads" and "Policy reloads" panels:

```bash
mise run demo metrics | grep deploygate_policy_
```

`deploygate_policy_last_reload_successful{kind="DeployApproval"}` is now `0`, while `deploygate_policy_last_reload_timestamp_seconds{kind="DeployApproval"}` still holds the time of the bundle that serves. The access bundle's series haven't moved. `deploygate_policy_reloads_total{kind="DeployApproval",result="failure"}` went up too; the poller may have tried before you did, so it can be one more than the reloads you asked for.

Alert on the gauge, `deploygate_policy_last_reload_successful == 0` for five minutes, and not on the counter. The poller reports a broken bundle once, not at every poll, so the counter goes up once and then stays flat: an alert on its rate fires, then resolves while the broken bundle is still there and the old one keeps serving. The gauge stays `0` from the failed attempt until a load of that bundle succeeds. The age of the last successful load can't replace it, because a bundle only reloads when its directory changes, on `SIGHUP` or when someone asks: a week nobody touched the policies and a week of rejected edits both end with a week-old timestamp.

Put the `}` back, set the bake to `15m` again and reload, and the gauge is back at `1` and the success counter moves. The same thing happens when a whole broken document lands in the directory, because a bundle loads as a whole: one team's typo stops every team's reload, never every team's deploys.

## Observe it

The compose stack records every request deploygate answers. Run a few of the demo commands above, or a load test, and look at the results in Grafana.

### Explore the dashboard

Open [deploygate · LGTM](http://localhost:3000/d/deploygate). The dashboard is provisioned from [`deploy/grafana/dashboards/deploygate.json`](deploy/grafana/dashboards/deploygate.json), with four datasources ready to use:

- **Mimir:** service health, grants, decisions, evaluation latency, HTTP status codes, reloads and k6 measurements. **Team** filters the policy panels; **Load run** filters k6 panels independently.
- **Tempo:** a table of decision traces. Open a trace to inspect access and deploy spans, candidate events and the roles used for the decision.
- **Loki:** JSON service logs. Expand a line and follow **View trace** to its Tempo trace. A span's logs link searches Loki for the same trace ID.
- **Pyroscope:** a CPU flame graph and a selectable runtime flame graph. Use **Runtime profile** to switch between allocations, live heap, goroutines, mutex contention, blocking and detected leaks. The selector lists types that have reached Pyroscope. Click a frame to inspect its callers and callees, or use **Explore profiles**. A Tempo span's profiles link opens the service profile for the surrounding time window; profiles describe the whole process, not just that request.

Alloy scrapes every five seconds; profiles upload every fifteen seconds. Start a load test below to populate the charts and stacks. Expected policy conflicts and asserts appear in the evaluation-error panel; k6 checks that their responses are correct.

The **Sigil evaluation performance** section measures the timed `p.Eval` calls. It shows completed evaluations per second, the evaluation count over the selected range, mean access and deploy latency, p50/p95/p99, and the share finishing within 100 µs. These panels follow **Team** and include failed evaluations. A deployment can evaluate both policies, while an access request evaluates one. Throughput comes from histogram counts, so granting several roles counts as one evaluation. Mean latency uses the histogram sum and count; percentiles are bucket estimates, with a lowest boundary of 50 µs. The throughput shown is what the service processed under the offered load, not its maximum capacity.

### Generate load with k6

From `examples/`:

```bash
mise run loadtest-smoke
mise run loadtest
DURATION=15m RATE=100 mise run loadtest
RATE=200 mise run loadtest-stress
```

The smoke test sends each of eight request cases once. The default load test schedules 100 requests per second for two minutes. Stress mode ramps from `RATE` to twice that rate, then five times it, and back over three minutes. Requests rotate evenly through deployment approval, review, denial and a failed input assert, then access grants, no grants, an exclusive-role conflict and a separation-of-duties assert. Each iteration checks both the HTTP status and the policy result. Expected `403`, `422` and `500` responses count as successful test cases. The conflict and the separation-of-duties case are policy failures on purpose, so a quarter of every run answers `500`; a real service would alert on that rate, and in this example it only shows that the service classifies them.

The tasks start the stack, run the pinned k6 image, send metrics to Mimir and save a JSON report under `results/<run-id>.json`. Reports include the scenario, revision, working-tree status, Docker resource allocation and threshold results; `results/` is ignored by Git. Set a unique `RUN_ID` to make a run easy to select in Grafana. A finished run remains visible in its time range, although its live series become stale.

To generate traffic against the stack that's already running, use `DURATION=15m RATE=100 mise run --skip-deps loadtest`. This skips the build and startup task, keeping the running application in place.

| Setting | Default | Meaning |
| --- | --- | --- |
| `RATE` | `100` | Iterations per second; each iteration makes one policy request |
| `DURATION` | `2m` | Duration of constant-rate load mode |
| `VUS` / `MAX_VUS` | `20` / `100` | Preallocated and maximum virtual users; stress mode uses `50` / `200` |
| `P95_MS` / `P99_MS` | `250` / `500` | Full-request latency budgets in milliseconds |
| `RUN_ID` | UTC timestamp | Report filename and Mimir `testid` label; use a unique value per run |

Any incorrect outcome, a failed-request rate of 1% or more, a latency-budget breach or a dropped iteration fails the run. Dropped iterations mean k6 could not start the requested work on schedule; inspect generator resources and concurrency before treating a run as a capacity measurement. The dashboard shows p95 and p99 separately for each request series. The JSON report provides the overall percentiles for the completed run.

These measurements cover HTTP, JSON handling, the applicable policy stages and telemetry with profiling enabled. They depend on your machine, Docker's CPU and memory allocation, and competing workloads. They measure this example service under the configured workload; they do not establish Sigil's maximum throughput or compare it with another policy engine. For repeatable measurements, leave the application and policies unchanged during the run and run the hot-reload e2e tests separately.

## Your own sigil binary: sigilc

Run `mise run sigilc <command>` from `examples/`, or `mise -C examples run sigilc <command>` from the repository root. The task passes command arguments and flags to `sigilc`.

The stock `sigil` CLI checks policies against the exported kind file, but it can't run Go code, so it can't evaluate a rule that calls `split`. A host builds its own binary with the [`cli` package](../docs/guides/host-binary.md) and links its kinds in. `cmd/sigilc` is one line of real code:

```go
cli.Main(cli.WithKind(deploy.Kind), cli.WithKind(access.Kind), cli.WithVersion(version))
```

`sigilc` then decodes test inputs into `deploy.Input` or `access.Input` and calls the real `strings.Split`, so its answers are the service's answers. It also writes both kind files. `cmd/sigilc/main.go` carries a `go generate` directive per kind, and `mise run generate` runs them:

```bash
go generate ./cmd/sigilc
```

With two kinds linked in, every policy command needs `--kind` with the exported kind file, so `sigilc` knows which contract to check against; without it, it stops and lists the kinds it links. Run these from `examples/`. `mise run policies` runs the checks and the tests for both kinds, the way the examples CI job does.

Check every team policy against its kind, with the platform's deploy documents as the trusted source, the same `Require` the service makes, and the lint levels from `policies/sigil.yaml`. The trusted source is `policies/platform/deploy`, not `policies/platform`: a bundle that mixes documents of two kinds doesn't load.

```bash
mise run sigilc check --kind policies/deploy_approval.sigil --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform/deploy \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams
```

The access policy gets the same check against the other kind:

```bash
mise run sigilc check --kind policies/access_grant.sigil --config policies/sigil.yaml \
  --require access.guardrails --trusted policies/platform/access \
  --policy access.main policies/access
```

Run each kind's test files:

```bash
mise run sigilc test --kind policies/deploy_approval.sigil policies/platform/deploy policies/teams
mise run sigilc test --kind policies/access_grant.sigil policies/platform/access policies/access
```

```text
ok    policies/teams/checkout/production_test.yaml  9 cases
ok    policies/teams/payments/production_test.yaml  16 cases
✓ 25 cases passed in 2 files
ok    policies/access/main_test.yaml  17 cases
✓ 17 cases passed in 1 file
```

Flatten a team policy into the rules it adds up to, with each invocation's conditions pushed into the rules and every param replaced by its value. `--policy` takes the name the document declares, `payments.production`, not a file path:

```bash
mise run sigilc explain --kind policies/deploy_approval.sigil \
  --policy payments.production -R policies/platform/deploy policies/teams
```

```text
payments.production: 7 rules from 3 policies and 1 module

  deny(not_eligible)        payments.production:7 → deploy.guardrails:8
    when not eligible

  deny(soak_too_short)      payments.production:7 → deploy.guardrails:12
    when release.soak < 4h and not release.hotfix

  approve(release_manager)  payments.production:10 → deploy.production:11
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier == "critical" and "release_manager" in actor.roles

  review(service_owner)     payments.production:10 → deploy.production:16
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier in ["standard", "internal"] and owns_service
    with approvers = ["payments-leads", "security-leads"]

  approve(release_manager)  payments.production:14 → deploy.production:11
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier == "critical" and "release_manager" in actor.roles

  review(service_owner)     payments.production:14 → deploy.production:16
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier in ["standard", "internal"] and owns_service
    with approvers = ["payments-leads"]

  approve(payments_sre)     payments.production:18
    when cleared and "payments-sre" in actor.teams
    with bake = 15m
```

`explain` on the access policy lists every grant it can make, and the guardrails' asserts, with the phase each runs in:

```bash
mise run sigilc explain --kind policies/access_grant.sigil \
  --policy access.main -R policies/platform/access policies/access
```

```text
access.main: 10 rules from 2 policies and 1 module

  reader(team_member)                    access.main:9
    when team_member

  deployer(team_member)                  access.main:10
    when team_member

  reader(everyone_in_staging)            access.main:14
    when environment == "staging"

  deployer(oncall)                       access.main:19
    when on_call
    with ttl = 2h

  release_manager(platform_member)       access.main:25
    when platform_member and not admin_cleared
    with ttl = 4h

  admin(clearance)                       access.main:29
    when admin_cleared

  admin(break_glass)                     access.main:36
    when break_glass
    with ttl = 15m

  auditor(compliance_member)             access.main:40
    when compliance_member

  assert named_actor (input)             access.main:6 → access.guardrails:6
    check actor.name != ""

  assert sod_auditor_deployer (outcome)  access.main:6 → access.guardrails:11
    check [auditor, deployer] exclusive in outcome
```

Fail when a checked-in kind file no longer matches the Go definition. `export` takes the kind's name, since it writes the file rather than reading it:

```bash
mise run sigilc export DeployApproval --check --out policies/deploy_approval.sigil
mise run sigilc export AccessGrant --check --out policies/access_grant.sigil
```

The flags are described in the [CLI reference](../docs/reference/cli.md).

## Tests

There are four layers, from fastest to slowest. `mise run test` runs the first three with `go test -race ./...`, and vets the fourth; none of them needs Docker.

- **Unit tests** are table-driven `testing` tests next to the code in `internal/` and `cmd/demo-cli`. One more sits next to the Grafana dashboard and checks that its JSON parses and that no two panels overlap.
- **Policy tests** run the same `*_test.yaml` files `sigilc test` runs, from `go test`, with `policytest.Run` and the real kinds. `internal/deploy/kind_test.go` and `internal/access/kind_test.go` load them the way the service does, with the guardrails required from `policies/platform/deploy` and `policies/platform/access`, and `policytest.Schema` fails when an exported kind file is stale. A test file can't expect a conflict, so the break-glass conflict is covered by the integration suite's `500`; [Test a conflict](../docs/guides/test-policies.md#test-a-conflict) shows how to test it against `Eval` directly, with plain `testing` and with Ginkgo.
- **Integration tests** in `test/integration` are a Ginkgo suite that wires the real store and server the way `cmd/deploygate` does and serves them with `httptest`. Spans go to an in-memory exporter and metrics to a fresh registry per spec, so the specs assert exact values: every span attribute, candidate and grant event, each counter after a known set of requests. It covers every grant of the access endpoint, the `403` of an empty outcome, the conflict with both candidates named and the separation-of-duties assert, each a `500`, the unnamed actor's `422`, on both endpoints, and an outcome assert that fails with nothing fired, a `500` too. With a policy made slow by its input, it checks the `503` of an evaluation that ran out of time and the `499` of a client that left during one, with the metrics and span status each leaves. Fake clocks pin `loaded_at` and fire polls by hand, so the reload specs cover polling, `SIGHUP` and last-known-good for both bundles without waiting, on private copies of `policies/teams` and `policies/access`. The suite also checks that the embedded bundles decide every case the same way as the directories they were built from, and it posts every file under `requests/` and checks that each answers with the status this walkthrough shows.
- **End-to-end tests** in `test/e2e` are a Ginkgo suite behind the `e2e` build tag that talks to the compose stack over HTTP only, and checks Mimir metrics, Tempo spans, Loki logs with resolvable trace IDs, Pyroscope profiles and all four Grafana datasources. A spec that needs the trace of its own request sends a `traceparent` header and reads that trace from Tempo by ID; that is how the suite checks that a request whose access stage failed has no `deploygate.evaluate` span. It also runs a deploy policy past the stack's one-second evaluation timeout, and abandons a slow request, checking the `503` and the `499` in the metrics.

Both suites run the same tables of requests and expected decisions, grants and failures from `test/internal/fixture`, each built with one `fixture.Entries` call, so the in-process server and the container can't drift apart. Run the end-to-end suite with:

```bash
mise run e2e
```

It brings the stack up first. To point the suite at a stack that's already running elsewhere, set `DEPLOYGATE_URL`, `MIMIR_URL`, `TEMPO_URL`, `LOKI_URL`, `PYROSCOPE_URL` and `GRAFANA_URL`, and run this from `examples/`:

```bash
go test -count=1 -tags e2e ./test/e2e/...
```

`-count=1` matters: the suite checks the stack, not the code, and without it `go test` would replay a cached pass from an earlier run. The hot reload specs edit the team policies in `DEPLOYGATE_POLICIES_DIR`, which defaults to `policies/teams`, and the access policy in `DEPLOYGATE_ACCESS_POLICIES_DIR`, which defaults to `policies/access`, and put every byte back afterwards, even when a spec fails. They skip themselves when a directory isn't writable or deploygate serves that kind's embedded bundle.

### Benchmarks

`internal/store/benchmark_test.go` measures the compiled policies the way the handlers use them, through the store, without HTTP or telemetry: an access grant, an access conflict, a failed input assert and the owner's deploy, each serial and in parallel. It is the number to watch when a policy or the library changes, since the HTTP layer adds a fixed cost on top.

```bash
mise run bench
```

## Layout

```text
examples/
  README.md                  this walkthrough
  go.mod, go.sum             the examples module
  .goreleaser.yaml           builds deploygate, demo-cli and sigilc; separate from the root release
  Dockerfile                 builds the deploygate image
  docker-compose.yaml        deploygate, Alloy, Grafana, Loki, Tempo, Mimir, Pyroscope and k6
  deploy/                    configuration for the compose services
  cmd/
    demo-cli/                the deployment platform CLI, run with `mise run demo`
    deploygate/              the service: `serve`, `healthcheck` and `version`
    sigilc/                  the host's sigil binary, with both kinds linked in
  internal/
    access/                  the AccessGrant kind
    config/                  flags, environment variables and their defaults
    deploy/                  the DeployApproval kind
    server/                  HTTP routes, handlers and the JSON error model
    store/                   the loaded policies and hot reload
    telemetry/               tracing, logging, metrics and continuous profiles
  policies/
    deploy_approval.sigil    the exported DeployApproval kind file, generated
    access_grant.sigil       the exported AccessGrant kind file, generated
    embed.go                 embeds platform/, teams/ and access/ into the binary
    platform/deploy/         the platform's trusted deploy documents
    platform/access/         the platform's trusted access documents
    teams/<team>/            each team's policy, its test cases and their inputs
    access/                  access.main, its test cases and their inputs
  requests/                  example requests embedded in demo-cli and checked by tests
  test/
    integration/             Ginkgo suite against the server in-process
    e2e/                     Ginkgo suite against the running compose stack
    load/                    k6 smoke, constant-rate and stress scenarios
    internal/fixture/        requests, expected decisions and helpers both suites share
```

[`policies/README.md`](./policies/README.md) describes the policy tree in detail: who owns which file, how the two kinds share the platform directory, and how to add a team.

## How it is wired

### demo-cli

`cmd/demo-cli` follows the Sigil CLI's layout. `main.go` runs `command.NewCommand` through `command.Execute`; each subcommand has its own package and takes dependencies through `With*` options in `options.go`. Shared HTTP transport, response rendering and scenario input live under `cmd/demo-cli/internal/`. The CLI uses the server's response types and sends requests over HTTP.

### deploy

`internal/deploy` is the contract, and nothing else. The `Input` struct and its nested `Release`, `Service` and `Actor` types carry `policy:` tags that name the inputs policies read. `Deny`, `Review` and `Approve` are the decision handles, and `Kind` ties them together with the precedence, the default `deny(no_rule_matched)` and the `split` function, and `policy.WithRecoverHostPanics()`: a host function that panics fails the evaluation closed instead of unwinding into gin; see [server](#server). Every other package imports this one; the kind file in `policies/` is generated from it.

### access

`internal/access` is the second contract, in the same shape. `Input` holds the actor as the identity provider describes them, `name`, `groups` and `clearance`, plus the team and the environment. The roles are decision handles: `Reader` and `Auditor` carry only a reason, `Deployer` and `ReleaseManager` a `GrantData` time to live that defaults to eight hours, and `Admin` an `AdminData` one that defaults to one. `Kind` collects them all and declares `admin` and `release_manager` exclusive. It has no host functions, and recovers their panics anyway, like the deploy kind, so the first one added fails closed too.

### store

`internal/store` holds the compiled policies. `Store[In]` is generic over the kind's input, so the service runs two: `store.NewDeploy` compiles every served team's root, `<team>.production`, from the team bundle, and `store.NewAccess` compiles the single root `access.main` from the access bundle. For a team, a load comes down to:

```go
deploy.Kind.Load(teamsFS, team+".production",
	policy.Require("deploy.guardrails", policy.From(policies.PlatformDeploy)))
```

It swaps the new set in with one atomic pointer store only when every root compiles, so in-flight requests finish on the bundle they started with, and a broken bundle never serves. The platform documents always come from the binary, `policies.PlatformDeploy` and `policies.PlatformAccess`, one view per kind because a bundle that mixes kinds doesn't load; there's no flag to point them elsewhere, because a guardrail an operator can swap isn't a guardrail.

At startup `cmd/deploygate` calls `InitialLoad`, which loads the same way but records the trigger `startup` and leaves the failure to its caller. There's no last good bundle yet, so `main` prints the diagnostics once and exits; on Kubernetes that holds a rollout at the old pods instead of serving without a policy.

`Watch` runs the reload loop. `SIGHUP` always reloads. A poll fingerprints the directory's content and reloads only when it changed since the last load, successful or not, so a broken bundle is reported once rather than every few seconds. Loads are serialized, and each runs in a span that records what triggered it. The stores are built with options, `store.NewDeploy(store.WithTeams(...), store.WithBundleDir(dir), store.WithMetrics(m), ...)` and `store.NewAccess(store.WithBundleDir(dir), ...)`, and `store.WithClock` lets the tests control time.

### server

`internal/server` is a gin router with recovery, OpenTelemetry, access log and request metrics middleware. It's built with functional options, `server.New(server.WithStore(deploy), server.WithAccessStore(access), server.WithMetrics(m), server.WithTracerProvider(tp))`, which returns the server or a humane error, and `Handler()` hands out the router for tests. Probes and scrapes are neither traced nor logged, so they don't bury the requests worth reading. The deployments handler decodes the request with unknown fields disallowed, evaluates `access.main`, turns the grants into roles, evaluates the team's policy with them, matches the result with the typed decision handles, and maps the decision to the HTTP status. Every error the service creates is a humane error, rendered as:

```json
{"error": {"message": "...", "advice": ["..."], "cause": {"message": "..."}}}
```

| Route | Does |
| --- | --- |
| `POST /api/v1/teams/{team}/deployments` | Evaluates `access.main`, then `<team>.production` with the granted roles |
| `POST /api/v1/access/grants` | Evaluates `access.main` alone |
| `GET /api/v1/policies` | Per kind: its version, `loaded_at`, the source and the served policies |
| `POST /api/v1/policies/reload` | Reloads both bundles now; `500` with the diagnostics when one doesn't compile |
| `GET /healthz` | `200` once the process serves |
| `GET /readyz` | `200` once both bundles are loaded, `503` before |
| `GET /metrics` | Prometheus text format |

Any other path answers `404` with the same error model. The server shuts down gracefully on `SIGINT` and `SIGTERM`, within the shutdown timeout.

Each evaluation runs under its own deadline, `--evaluation-timeout`, derived from the request's context, so it ends at whichever comes first: the timeout, `503`, or the client closing the connection, `499`. Both kinds are declared with `policy.WithRecoverHostPanics()`. Without it, a panicking host function would unwind out of `Eval` into gin's recovery middleware, which logs the stack and answers an empty `500`, but the panic also skips everything that runs after the handler returns: the request isn't counted in `deploygate_requests_total`, the server span ends without a status, and no evaluation error is counted. With it, the panic is a runtime error like any other: the policy fails closed, the answer is the usual `500` with the fallback decision and the error, `deploygate_evaluation_errors_total{kind="runtime"}` counts it, and the log line of the failure carries the stack. The library leaves the option off by default because a plain `net/http` server already recovers a handler's panic; a reference service that promises every failed evaluation a decision and a count is the case it's for. The recovery middleware stays, for panics outside an evaluation.

### telemetry

`internal/telemetry` sets up the tracer provider from the standard OpenTelemetry environment variables, a zap logger that adds the trace and span IDs to each line, and the Prometheus metrics on a registry the service owns, next to the Go runtime and process collectors. `/metrics` serves that registry and nothing else:

| Metric | Type | Labels |
| --- | --- | --- |
| `deploygate_decisions_total` | counter | `team`, `policy`, `decision`, `reason` |
| `deploygate_evaluation_duration_seconds` | histogram | `team` |
| `deploygate_evaluation_errors_total` | counter | `team`, `kind`: `assertion`, `runtime`, `conflict` or `timeout`, `stage`: `access` or `deploy` |
| `deploygate_access_grants_total` | counter | `team`, `role`, `reason` |
| `deploygate_access_evaluation_duration_seconds` | histogram | `team` |
| `deploygate_policy_reloads_total` | counter | `kind`, `result`: `success` or `failure` |
| `deploygate_policy_last_reload_timestamp_seconds` | gauge | `kind`; the time of that bundle's last successful load |
| `deploygate_policy_last_reload_successful` | gauge | `kind`; `1` when that bundle's latest load attempt succeeded, `0` when it failed |
| `deploygate_policy_loaded_info` | gauge, always 1 | `kind`, `team`, `policy`, `source`; `team` is empty for the access root |

The server's middleware adds the HTTP request metrics to the same registry:

| Metric | Type | Labels |
| --- | --- | --- |
| `deploygate_requests_total` | counter | `code`, `method`, `url` |
| `deploygate_request_duration_seconds` | histogram | `code`, `method`, `url` |

Every label is bounded. `url` is the route template, `/api/v1/teams/:team/deployments`, or `unmatched` for a path without a route, so made-up team names and scanned paths can't create new series, and a method outside the standard set counts as `other`. Scrapes of `/metrics` aren't counted, because they would dominate the request rate.

`deploygate_decisions_total` counts the decisions a deploy policy made, and nothing else. A failed evaluation answers with the kind's default, `deny` / `no_rule_matched`, but the policy didn't decide it, so it counts only in `deploygate_evaluation_errors_total`, in either stage: with `stage="access"` when the access stage failed and the deploy policy never ran, with `stage="deploy"` when the deploy policy itself failed. A `deny` / `no_rule_matched` in `deploygate_decisions_total` is always a request no rule matched. A request the client canceled during an evaluation counts in neither: the policy decided nothing and nothing failed. It shows up only as `deploygate_requests_total{code="499"}`, which says how often clients give up without adding a series of its own or putting a client's choice into an error rate an alert reads.

Each deployment request gets the gin server span and, below it, two siblings. The `deploygate.access` span comes first, with `sigil.kind`, `sigil.policy`, `sigil.team`, `sigil.environment` and `sigil.grants`, plus one `sigil.grant` event per grant with its `role`, `reason` and `ttl`. The `deploygate.evaluate` span follows, with `sigil.kind`, `sigil.policy`, `sigil.team`, `sigil.roles`, `sigil.decision`, `sigil.reason` and `sigil.candidates`, plus one `sigil.candidate` event per trace entry. A failed evaluation sets its span's status to error, except when the client canceled the request: that span records the error and leaves the status unset, as OpenTelemetry's gRPC conventions do for a server call the client canceled, and the request span of a `499` is unset too. When the access stage fails, there is no `deploygate.evaluate` span at all. The access endpoint records the same `deploygate.access` span on its own. Reloads run in a `deploygate.policies.reload` span with `sigil.source`, `sigil.kind`, `deploygate.reload.trigger` (`startup`, `manual`, `sighup` or `poll`) and, on success, `sigil.policies`; a rejected bundle sets the status to error and records the diagnostics. The first loads sit under a `deploygate.startup` span, and a graceful shutdown runs in a `server.shutdown` span.

### config

`internal/config` builds the command tree with cobra: `deploygate serve`, `deploygate healthcheck`, which probes `/readyz` on the loopback interface and reads `--addr` the same way, and `deploygate version`. Each flag of `serve` can also be set through a `DEPLOYGATE_` environment variable, read with viper:

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--addr` | `DEPLOYGATE_ADDR` | `:8080` | Listen address for the API, health and metrics |
| `--policies` | `DEPLOYGATE_POLICIES` | empty | Directory with the team policies; empty serves the teams embedded in the binary |
| `--access-policies` | `DEPLOYGATE_ACCESS_POLICIES` | empty | Directory with the access policies, root `access.main`; empty serves the access bundle embedded in the binary |
| `--team` | `DEPLOYGATE_TEAMS` | `payments,checkout` | Teams to serve, repeatable or comma-separated; team `t` evaluates `t.production` |
| `--reload-interval` | `DEPLOYGATE_RELOAD_INTERVAL` | `30s` | How often to check both policies directories for changes; `0` turns polling off |
| `--shutdown-timeout` | `DEPLOYGATE_SHUTDOWN_TIMEOUT` | `15s` | How long a graceful shutdown may take |
| `--evaluation-timeout` | `DEPLOYGATE_EVALUATION_TIMEOUT` | `1s` | How long one policy evaluation may take, each stage on its own; past it the request answers `503` with the fallback decision. Must be positive |
| `--debug` | `DEPLOYGATE_DEBUG` | `false` | Debug logging and gin's debug mode |
| `--log-format` | `DEPLOYGATE_LOG_FORMAT` | `json` | `json` or `console` |

Tracing takes the standard variables. Spans are exported over OTLP only when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` points at Alloy or another collector; without one, spans are still created, so trace IDs appear in the logs. `OTEL_EXPORTER_OTLP_INSECURE=true` turns off TLS the way an `http://` endpoint does, `OTEL_SERVICE_NAME` defaults to `deploygate`, and `OTEL_TRACES_EXPORTER=none` turns export off even when an endpoint is set.

`PYROSCOPE_SERVER_ADDRESS` enables continuous profiling; Compose sets it to `http://pyroscope:4040`. Leave it unset to disable profiling when running the service alone. All eleven profile types exposed by the pinned Go SDK are enabled:

| Profile | What it records |
| --- | --- |
| CPU | Sampled CPU time |
| Allocated objects / bytes | Allocations during the profiling interval |
| In-use objects / bytes | Live heap allocations |
| Goroutines | Stacks of current goroutines |
| Mutex count / duration | Contended mutex events and accumulated waiting time |
| Block count / duration | Synchronization blocking events and accumulated blocked time |
| Goroutine leaks | Stacks that Go's leak detector identifies as leaked |

Profiles use the service name and version as labels. Mutex profiling samples one in five contention events. Blocking profiling samples roughly one event per millisecond spent blocked. Sampling starts only after the profiler starts successfully; shutdown disables block sampling and restores the previous mutex fraction.

The heap collector does not request extra GC cycles. Goroutine-leak capture does trigger GC every fifteen seconds, as required by the Go runtime, so that cost is part of the load measurements. A healthy service can have no leak samples, and contention profiles can be empty when there is no contention. The SDK upload test checks every configured type, including empty leak profiles; Grafana lists a type once Pyroscope stores samples for it.

## Building it separately

The examples have their own [`.goreleaser.yaml`](./.goreleaser.yaml), which builds `deploygate`, `demo-cli` and `sigilc`. The root release doesn't use it, so nothing in `examples/` ships with Sigil itself. To build snapshot binaries locally:

```bash
mise run snapshot
```

`mise run build` builds plain binaries into `examples/bin/` instead. `mise run release-check` validates the GoReleaser configuration, and `mise run check` runs every gate the examples CI job runs: lint, tests, the policy checks and the release check.

## Further reading

- [A tour of the language](../docs/getting-started/tour.md) walks through the same policies by hand.
- [Per-team policies](../docs/guides/team-policies.md) explains how the team policies compose the platform's.
- [Policies in a ConfigMap](../docs/guides/configmaps.md) shows how to ship the team bundle to a cluster.
- [Kind files](../docs/reference/kind-files.md#collecting-kinds) and [Evaluation semantics](../docs/reference/evaluation.md#collecting-kinds) define collecting kinds, `exclusive` and outcome asserts.
- [Go API](../docs/reference/go-api.md) is the reference for everything `internal/store` and `internal/server` call.
