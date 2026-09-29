import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Rate } from 'k6/metrics';

const baseURL = __ENV.BASE_URL || 'http://deploygate:8080';
const mode = __ENV.TEST_MODE || 'load';
const rate = Number(__ENV.RATE || 100);
const duration = __ENV.DURATION || '2m';
const p95 = Number(__ENV.P95_MS || 250);
const p99 = Number(__ENV.P99_MS || 500);
const runID = __ENV.RUN_ID || `${mode}-${Date.now()}`;
if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(runID)) {
  throw new Error('RUN_ID must start with a letter or digit and contain only letters, digits, dots, underscores or hyphens');
}
const outcomeFailures = new Rate('outcome_failures');

// These are the same requests the walkthrough posts. Every iteration checks
// the response body as well as the status, including expected policy failures.
const deploy = '/api/v1/teams/payments/deployments';
const access = '/api/v1/access/grants';
const cases = [
  { name: 'review', file: 'owner.json', path: deploy, status: 202, decision: 'review', reason: 'service_owner' },
  { name: 'approve', file: 'sre.json', path: deploy, status: 200, decision: 'approve', reason: 'payments_sre' },
  { name: 'deny', file: 'short-soak.json', path: deploy, status: 403, decision: 'deny', reason: 'soak_too_short' },
  { name: 'assert', file: 'unnamed-actor.json', path: deploy, status: 422, decision: 'deny', assert: 'named_actor' },
  { name: 'grants', file: 'access-member.json', path: access, status: 200, roles: ['reader', 'deployer'] },
  { name: 'no-grants', file: 'access-outsider.json', path: access, status: 403, roles: [] },
  { name: 'exclusive', file: 'access-break-glass-platform.json', path: access, status: 500, conflict: true },
  { name: 'separation-of-duties', file: 'access-compliance-member.json', path: access, status: 500, assert: 'sod_auditor_deployer' },
].map((item) => ({ ...item, body: open(`${__ENV.REQUESTS_DIR || '/requests'}/${item.file}`) }));

const scenarios = {
  smoke: { executor: 'shared-iterations', vus: 1, iterations: cases.length, maxDuration: '30s' },
  load: {
    executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration,
    preAllocatedVUs: Number(__ENV.VUS || 20), maxVUs: Number(__ENV.MAX_VUS || 100),
    gracefulStop: '10s',
  },
  stress: {
    executor: 'ramping-arrival-rate', startRate: rate, timeUnit: '1s',
    preAllocatedVUs: Number(__ENV.VUS || 50), maxVUs: Number(__ENV.MAX_VUS || 200),
    stages: [
      { target: rate, duration: '30s' },
      { target: rate * 2, duration: '1m' },
      { target: rate * 5, duration: '1m' },
      { target: rate, duration: '30s' },
    ],
    gracefulStop: '10s',
  },
};
if (!scenarios[mode]) throw new Error(`Unknown TEST_MODE ${mode}; use smoke, load or stress`);

export const options = {
  scenarios: { [mode]: scenarios[mode] },
  tags: { service: 'deploygate', test_mode: mode, testid: runID },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)'],
  thresholds: {
    checks: ['rate==1'],
    outcome_failures: ['rate==0'],
    http_req_failed: ['rate<0.01'],
    http_req_duration: [`p(95)<${p95}`, `p(99)<${p99}`],
    ...(mode === 'smoke' ? {} : { dropped_iterations: ['count==0'] }),
  },
};

export function setup() {
  const response = http.get(`${baseURL}/readyz`, { tags: { name: 'readiness' } });
  if (response.status !== 200) throw new Error(`deploygate is not ready: ${response.status}`);
}

export default function () {
  const item = cases[exec.scenario.iterationInTest % cases.length];
  const response = http.post(`${baseURL}${item.path}`, item.body, {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: item.name },
    responseCallback: http.expectedStatuses(item.status),
  });
  let body;
  try { body = response.json(); } catch (_) { body = {}; }
  const valid = check(response, {
    'expected status': (r) => r.status === item.status,
    'expected policy outcome': () =>
      (!item.decision || body.decision === item.decision) &&
      (!item.reason || body.reason === item.reason) &&
      (!item.assert || (body.asserts || []).some((a) => a.reason === item.assert)) &&
      (!item.conflict || (body.conflict?.candidates || []).length >= 2) &&
      (!item.roles || JSON.stringify((body.grants || []).map((g) => g.role).sort()) === JSON.stringify([...item.roles].sort())),
  });
  outcomeFailures.add(!valid);
}

export function handleSummary(data) {
  const report = {
    run_id: runID, mode, target: baseURL,
    git_commit: __ENV.GIT_COMMIT || 'unknown',
    git_changed_files: __ENV.GIT_CHANGED_FILES === undefined ? null : Number(__ENV.GIT_CHANGED_FILES),
    docker: __ENV.DOCKER_INFO ? JSON.parse(__ENV.DOCKER_INFO) : null,
    configured_scenario: scenarios[mode],
    latency_budget_ms: { p95, p99 },
    note: 'Full HTTP service with access and deploy evaluation plus telemetry; not a Sigil engine microbenchmark.',
    ...data,
  };
  const m = data.metrics;
  const latency = m.http_req_duration?.values || {};
  const failed = Object.values(m).some((metric) => Object.values(metric.thresholds || {}).some((t) => !t.ok));
  return {
    [`/results/${runID}.json`]: JSON.stringify(report, null, 2),
    stdout: `\n${runID}: ${failed ? 'FAIL' : 'PASS'}\n` +
      `${m.http_reqs?.values.count || 0} requests, ${(m.http_reqs?.values.rate || 0).toFixed(1)} req/s\n` +
      `p95=${(latency['p(95)'] || 0).toFixed(2)} ms, p99=${(latency['p(99)'] || 0).toFixed(2)} ms\n` +
      `failed request rate=${m.http_req_failed?.values.rate || 0}, incorrect outcomes=${m.outcome_failures?.values.rate || 0}, dropped iterations=${m.dropped_iterations?.values.count || 0}\n` +
      `Report: results/${runID}.json\n`,
  };
}
