// The executors behind each TEST_MODE, and the thresholds that judge a run.
//
// Every mode except smoke runs three scenarios side by side: single-alert
// routes and webhook batches, splitting RATE by WEBHOOK_SHARE, and a slow,
// steady stream of concurrent policy reloads. The reloads are the point of
// running them together: alertrouter swaps compiled policies in while it
// routes, and every alert still has to come out right.

import * as config from "./config.js";
import { formatDuration, parseDuration } from "./rules.js";

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
  // A sudden 10x burst, as when one incident fires every alert at once, then
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
      // A few generated alerts through each endpoint, so a broken generator
      // shows up in the smoke run and not an hour into a soak.
      routes: { executor: "shared-iterations", exec: "route", vus: 1, iterations: 5, maxDuration: "1m" },
      webhooks: { executor: "shared-iterations", exec: "webhook", vus: 1, iterations: 5, maxDuration: "1m" },
      reloads: { executor: "shared-iterations", exec: "reload", vus: 1, iterations: 1, maxDuration: "1m" },
    };
  }

  const split = (rate) => ({
    routes: Math.max(1, Math.round(rate * (1 - config.webhookShare))),
    webhooks: Math.max(1, Math.round(rate * config.webhookShare)),
  });
  const vus = { preAllocatedVUs: config.vus, maxVUs: config.maxVUs, gracefulStop: "10s" };

  let routes;
  let webhooks;
  let length;
  if (mode === "load" || mode === "soak") {
    const rates = split(config.rate);
    routes = {
      executor: "constant-arrival-rate",
      rate: rates.routes,
      timeUnit: "1s",
      duration: config.duration,
      ...vus,
    };
    webhooks = {
      executor: "constant-arrival-rate",
      rate: rates.webhooks,
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
    routes = ramp("routes");
    webhooks = ramp("webhooks");
    length = stages.reduce((sum, [, duration]) => sum + parseDuration(duration), 0);
  }

  return {
    routes: { ...routes, exec: "route" },
    webhooks: { ...webhooks, exec: "webhook" },
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
// right; the load modes allow one wrong alert in a thousand before failing,
// which is still loud on the dashboard. breakpoint aborts on the first
// threshold that fails: that is the capacity it's looking for.
export function thresholds(mode) {
  const exact = mode === "smoke";
  const abort = mode === "breakpoint";
  const rule = (threshold) => (abort ? { threshold, abortOnFail: true, delayAbortEval: "30s" } : threshold);
  const { route, webhook } = config.budget;

  return {
    checks: [rule(exact ? "rate==1" : "rate>0.999")],
    routing_correct: [rule(exact ? "rate==1" : "rate>0.999")],
    http_req_failed: [rule(exact ? "rate==0" : "rate<0.01")],
    "http_req_duration{name:route}": [rule(`p(95)<${route.p95}`), rule(`p(99)<${route.p99}`)],
    "http_req_duration{name:webhook}": [rule(`p(95)<${webhook.p95}`), rule(`p(99)<${webhook.p99}`)],
    // A dropped iteration is an alert k6 meant to send and couldn't, because
    // every VU was still waiting on alertrouter. Smoke has no arrival rate.
    ...(exact ? {} : { dropped_iterations: [rule("count==0")] }),
    // While run.sh alternates the bundle, both kinds of reload must happen, or
    // the run never tested keeping the last good bundle. Counts are only
    // known at the end, so these never abort a run.
    ...(config.badBundleEvery > 0 ? { reloads_accepted: ["count>0"], reloads_rejected: ["count>0"] } : {}),
  };
}
