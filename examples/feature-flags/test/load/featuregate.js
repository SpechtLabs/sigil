// The k6 suite for featuregate. `mise run loadtest` runs it in the compose
// stack's k6 container (test/load/run.sh); TEST_MODE picks the shape and
// lib/config.js lists every knob.
//
// The named cases from requests/cases.json carry the outcome featuregate must
// answer; generated contexts are checked for what has to hold whatever the
// policies say (lib/checks.js). A run passes only if featuregate is fast
// enough and right, evaluation by evaluation, while policy reloads run
// underneath it.

import exec from "k6/execution";
import http from "k6/http";
import { cases, flagsOf } from "./lib/cases.js";
import { checkBulk, checkBulkGenerated, checkGenerated, checkReload, checkServing, checkSingle } from "./lib/checks.js";
import * as config from "./lib/config.js";
import { Generator } from "./lib/generate.js";
import { build, thresholds } from "./lib/scenarios.js";
import { report } from "./lib/summary.js";

const scenarios = build(config.mode, cases.length);
const headers = { "Content-Type": "application/json" };

export const options = {
  scenarios,
  tags: { service: "featuregate", test_mode: config.mode, testid: config.runID },
  summaryTrendStats: ["avg", "min", "med", "max", "p(95)", "p(99)"],
  thresholds: thresholds(config.mode),
};

// setup checks featuregate is ready: compose starts k6 once it is healthy, but
// run.sh doesn't restart a stack that is already up. It then checks the
// flags the named cases evaluate are the ones featuregate serves.
export function setup() {
  const ready = http.get(`${config.baseURL}/readyz`, { tags: { name: "readiness" } });
  if (ready.status !== 200) throw new Error(`featuregate is not ready: ${ready.status}`);

  const flags = flagsOf();
  const served = http.get(`${config.baseURL}/api/v1/flags`, { tags: { name: "readiness" } });
  if (served.status !== 200) throw new Error(`GET /api/v1/flags answered ${served.status}`);
  const keys = new Set(listed(served.json()));
  const missing = flags.filter((flag) => !keys.has(flag));
  if (missing.length > 0) {
    throw new Error(`featuregate doesn't serve ${missing.join(", ")}, which requests/cases.json evaluates`);
  }
  console.log(`featuregate serves ${[...keys].sort().join(", ")}; generated contexts draw from ${flags.join(", ")}`);

  return { flags };
}

// smokeCase sends one named case from requests/cases.json.
export function smokeCase() {
  const item = cases[exec.scenario.iterationInTest % cases.length];
  if (item.kind === "single") {
    const response = post(`/ofrep/v1/evaluate/flags/${item.flag}`, item.body, "single", item.status, { case: item.name });
    checkSingle(response, item.expect, item.status, item.name);
  } else {
    const response = post("/ofrep/v1/evaluate/flags", item.body, "bulk", item.status, { case: item.name });
    checkBulk(response, item.expect, item.status, item.name);
  }
}

// single evaluates one flag for a generated context, then the same context
// in bulk, and checks the two agree.
export function single(data) {
  const source = generator(data);
  const flag = source.flag();
  const { context, body } = source.request();
  const one = post(`/ofrep/v1/evaluate/flags/${flag}`, body, "single", 200);
  const all = post("/ofrep/v1/evaluate/flags", body, "bulk-check", 200);
  checkGenerated(one, all, flag, `${flag} for ${context.targetingKey}`, context);
}

// bulk evaluates every flag for a generated context. Half the requests repeat
// with the ETag they got, which must answer 304 or a fresh 200, never an error.
export function bulk(data) {
  const { context, body } = generator(data).request();
  const first = post("/ofrep/v1/evaluate/flags", body, "bulk", 200);
  const etag = first.headers.Etag || first.headers.ETag;
  checkBulkGenerated(first, data.flags, `bulk for ${context.targetingKey}`, context);
  if (etag && exec.scenario.iterationInTest % 2 === 0) {
    post("/ofrep/v1/evaluate/flags", body, "bulk-etag", [200, 304], {}, { "If-None-Match": etag });
  }
}

// reload asks featuregate to reload its policies RELOAD_CONCURRENCY times at
// once. Each reload recompiles every flag's policy; the evaluation scenarios
// running meanwhile check that no flag is answered from a half-swapped set.
// While run.sh alternates the mounted directory between good and broken
// (BAD_BUNDLE_EVERY), a reload may be rejected, and afterwards every flag's
// last good policy must still serve.
export function reload(data) {
  const alternating = config.badBundleEvery > 0;
  const request = [
    "POST",
    `${config.baseURL}/api/v1/policies/reload`,
    null,
    {
      tags: { name: "reload" },
      responseCallback: alternating ? http.expectedStatuses(200, 422) : http.expectedStatuses(200),
    },
  ];
  const responses = http.batch(Array.from({ length: config.reloadConcurrency }, () => request));
  for (const response of responses) checkReload(response, data.flags, alternating ? config.badBundleFile : undefined);

  const served = http.get(`${config.baseURL}/api/v1/flags`, { tags: { name: "flags" } });
  checkServing(served, data.flags);
}

export function handleSummary(data) {
  return report(data, scenarios);
}

let source;

// generator returns this VU's context generator. Each VU draws from SEED plus
// its id, so the same SEED sends every VU the same contexts again.
function generator(data) {
  if (!source) source = new Generator(config.seed + exec.vu.idInTest, data.flags);
  return source;
}

// post sends a JSON body and counts only the expected statuses as a success,
// so a correct 404 or 304 from a named case isn't an http_req_failed.
function post(path, body, name, status, tags = {}, extra = {}) {
  return http.post(`${config.baseURL}${path}`, body, {
    headers: { ...headers, ...extra },
    tags: { name, ...tags },
    responseCallback: http.expectedStatuses(...[status].flat()),
  });
}

// listed returns the flag keys of a GET /api/v1/flags response.
function listed(body) {
  const list = Array.isArray(body) ? body : body.flags || [];
  return list.map((flag) => (typeof flag === "string" ? flag : flag.key));
}
