// The executors behind each TEST_MODE, and the thresholds that judge a run.
//
// Every mode except smoke runs three scenarios side by side: single-flag
// evaluations and bulk evaluations, splitting RATE by BULK_SHARE, and a slow,
// steady stream of concurrent policy reloads. The reloads are the point of
// running them together: featuregate swaps compiled policies in while it
// evaluates, and every flag still has to come out right.

import * as config from "./config.js";
import { formatDuration, parseDuration } from "./duration.js";

// The shape of each ramping mode, as multiples of RATE over time.
const shapes = {
  // Past the rate the stack is sized for, and back: does it recover?
  stress: [
    [1, "1m"],
    [2, "1m"],
    [5, "1m"],
    [5, "1m"],
    [1, "1m"],
  ],
  // A sudden 10x burst, as when a mobile release pulls every flag at once, then
  // two minutes at RATE to show latency and correctness come back.
  spike: [
    [1, "1m"],
    [10, "10s"],
    [10, "1m"],
    [1, "10s"],
    [1, "2m"],
  ],
};

// build returns the scenarios for a mode and the number of smoke iterations
// each smoke scenario runs.
export function build(mode, caseCount) {
  if (mode === "smoke") {
    return {
      cases: { executor: "shared-iterations", exec: "smokeCase", vus: 1, iterations: caseCount, maxDuration: "1m" },
      // A few generated contexts through each endpoint, so a broken generator
      // shows up in the smoke run and not an hour into a soak.
      singles: { executor: "shared-iterations", exec: "single", vus: 1, iterations: 5, maxDuration: "1m" },
      bulks: { executor: "shared-iterations", exec: "bulk", vus: 1, iterations: 5, maxDuration: "1m" },
      reloads: { executor: "shared-iterations", exec: "reload", vus: 1, iterations: 1, maxDuration: "1m" },
    };
  }

  const split = (rate) => ({
    singles: Math.max(1, Math.round(rate * (1 - config.bulkShare))),
    bulks: Math.max(1, Math.round(rate * config.bulkShare)),
  });
  const vus = { preAllocatedVUs: config.vus, maxVUs: config.maxVUs, gracefulStop: "10s" };

  let singles;
  let bulks;
  let length;
  if (mode === "load" || mode === "soak") {
    const rates = split(config.rate);
    singles = {
      executor: "constant-arrival-rate",
      rate: rates.singles,
      timeUnit: "1s",
      duration: config.duration,
      ...vus,
    };
    bulks = {
      executor: "constant-arrival-rate",
      rate: rates.bulks,
      timeUnit: "1s",
      duration: config.duration,
      ...vus,
    };
    length = parseDuration(config.duration);
  } else {
    const stages = mode === "breakpoint" ? [[config.breakpointMaxRate / config.rate, config.duration]] : shapes[mode];
    const ramp = (share) => ({
      executor: "ramping-arrival-rate",
      startRate: split(config.rate)[share],
      timeUnit: "1s",
      stages: stages.map(([times, duration]) => ({ target: split(config.rate * times)[share], duration })),
      ...vus,
    });
    singles = ramp("singles");
    bulks = ramp("bulks");
    length = stages.reduce((sum, [, duration]) => sum + parseDuration(duration), 0);
  }

  return {
    singles: { ...singles, exec: "single" },
    bulks: { ...bulks, exec: "bulk" },
    reloads: {
      executor: "constant-arrival-rate",
      exec: "reload",
      rate: config.reloadsPerMinute,
      timeUnit: "1m",
      duration: formatDuration(length),
      preAllocatedVUs: 2,
      maxVUs: 10,
      gracefulStop: "30s",
    },
  };
}

// thresholds judges a run. Smoke checks named cases, so it expects every one
// right; the load modes allow one wrong answer in a thousand before failing,
// which is still loud on the dashboard. breakpoint aborts on the first
// threshold that fails: that is the capacity it's looking for.
export function thresholds(mode) {
  const exact = mode === "smoke";
  const abort = mode === "breakpoint";
  const rule = (threshold) => (abort ? { threshold, abortOnFail: true, delayAbortEval: "30s" } : threshold);
  const { single, bulk } = config.budget;

  return {
    checks: [rule(exact ? "rate==1" : "rate>0.999")],
    evaluation_correct: [rule(exact ? "rate==1" : "rate>0.999")],
    http_req_failed: [rule(exact ? "rate==0" : "rate<0.01")],
    "http_req_duration{name:single}": [rule(`p(95)<${single.p95}`), rule(`p(99)<${single.p99}`)],
    "http_req_duration{name:bulk}": [rule(`p(95)<${bulk.p95}`), rule(`p(99)<${bulk.p99}`)],
    // A dropped iteration is a request k6 meant to send and couldn't, because
    // every VU was still waiting on featuregate. Smoke has no arrival rate.
    ...(exact ? {} : { dropped_iterations: [rule("count==0")] }),
    // While run.sh alternates the bundle, both kinds of reload must happen, or
    // the run never tested keeping the last good bundle. Counts are only
    // known at the end, so these never abort a run.
    ...(config.badBundleEvery > 0 ? { reloads_accepted: ["count>0"], reloads_rejected: ["count>0"] } : {}),
  };
}
