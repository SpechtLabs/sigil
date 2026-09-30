// The k6 suite for alertrouter. `mise run loadtest` runs it in the compose
// stack's k6 container (test/load/run.sh); TEST_MODE picks the shape and
// lib/config.js lists every knob.
//
// Each alert k6 sends carries the outcome alertrouter must answer: the named
// cases from requests/cases.json, and generated alerts whose outcome
// lib/rules.js derives from the same rules as the policies. A run passes only
// if alertrouter is fast enough and right, alert by alert, while policy
// reloads run underneath it.

import http from 'k6/http';
import exec from 'k6/execution';

import * as config from './lib/config.js';
import { cases, verifyModel } from './lib/cases.js';
import { checkReload, checkRoute, checkWebhook } from './lib/checks.js';
import { Generator } from './lib/generate.js';
import { build, thresholds } from './lib/scenarios.js';
import { report } from './lib/summary.js';

const scenarios = build(config.mode, cases.length);
const headers = { 'Content-Type': 'application/json' };

export const options = {
  scenarios,
  tags: { service: 'alertrouter', test_mode: config.mode, testid: config.runID },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)'],
  thresholds: thresholds(config.mode),
};

// setup checks alertrouter is ready: compose starts k6 once it is healthy, but
// run.sh doesn't restart a stack that is already up. It then reads the team
// directory, the on-call targets and channels the generated alerts' outcomes
// depend on, and checks lib/rules.js against it.
export function setup() {
  const ready = http.get(`${config.baseURL}/readyz`, { tags: { name: 'readiness' } });
  if (ready.status !== 200) throw new Error(`alertrouter is not ready: ${ready.status}`);

  const response = http.get(`${config.baseURL}/api/v1/teams`, { tags: { name: 'readiness' } });
  if (response.status !== 200) throw new Error(`GET /api/v1/teams answered ${response.status}`);
  const directory = {};
  for (const team of response.json().teams || []) directory[team.name] = team;

  const verified = verifyModel(directory);
  console.log(`lib/rules.js agrees with ${verified} cases in requests/cases.json for teams ${Object.keys(directory).join(', ')}`);

  return { directory };
}

// smokeCase sends one named case from requests/cases.json.
export function smokeCase() {
  const item = cases[exec.scenario.iterationInTest % cases.length];
  if (item.kind === 'route') {
    const response = post(`/api/v1/teams/${item.team}/route`, item.body, 'route', item.status, { case: item.name });
    checkRoute(response, item.expect, item.status, item.name);
  } else {
    const response = post('/api/v1/alerts', item.body, 'webhook', 200, { case: item.name });
    checkWebhook(response, item.expect, item.name);
  }
}

// route sends one generated alert to its team's route endpoint.
export function route(data) {
  const { team, body, expect } = generator(data).route();
  const response = post(`/api/v1/teams/${team}/route`, JSON.stringify(body), 'route', 200);
  checkRoute(response, expect, 200, `${team} ${body.alert.name}`);
}

// webhook sends a generated Alertmanager batch of 1 to MAX_BATCH alerts.
export function webhook(data) {
  const { body, expects } = generator(data).webhook(config.maxBatch, Date.now());
  const response = post('/api/v1/alerts', JSON.stringify(body), 'webhook', 200);
  checkWebhook(response, { results: expects }, `webhook of ${expects.length}`);
}

// reload asks alertrouter to reload its policies RELOAD_CONCURRENCY times at
// once. Each reload recompiles every team's bundle; the routing scenarios
// running meanwhile check that no alert is answered from a half-swapped one.
export function reload(data) {
  const request = ['POST', `${config.baseURL}/api/v1/policies/reload`, null, {
    tags: { name: 'reload' },
    responseCallback: http.expectedStatuses(200),
  }];
  const responses = http.batch(Array.from({ length: config.reloadConcurrency }, () => request));
  const teams = Object.keys(data.directory);
  for (const response of responses) checkReload(response, teams);
}

export function handleSummary(data) {
  return report(data, scenarios);
}

let source;

// generator returns this VU's alert generator. Each VU draws from SEED plus
// its id, so the same SEED sends every VU the same alerts again.
function generator(data) {
  if (!source) source = new Generator(config.seed + exec.vu.idInTest, data.directory);
  return source;
}

// post sends a JSON body and counts only the expected status as a success,
// so a correct 404 or 422 from a named case isn't an http_req_failed.
function post(path, body, name, status, tags = {}) {
  return http.post(`${config.baseURL}${path}`, body, {
    headers,
    tags: { name, ...tags },
    responseCallback: http.expectedStatuses(status),
  });
}
