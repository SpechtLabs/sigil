// Checks of alertrouter's answers against the expected outcomes, and the
// custom metrics they feed. A status code alone says the service answered;
// these say it answered right, alert by alert.

import { check } from "k6";
import { Counter, Rate } from "k6/metrics";

// routing_correct is the fraction of alerts whose decision, reason, target and
// channel matched the expectation. Mimir stores it as k6_routing_correct_rate.
export const routingCorrect = new Rate("routing_correct");

// alerts_routed counts the alerts alertrouter answered with a decision,
// tagged with the decision it answered; the per-decision counters are the
// same count split for the end-of-run summary, which doesn't show tags.
export const alertsRouted = new Counter("alerts_routed");
const decisionCounters = {
  page: new Counter("decisions_page"),
  drop: new Counter("decisions_drop"),
  notify: new Counter("decisions_notify"),
};

// The payload fields a result carries only for some decisions: a page has a
// target and no channel, a notify a channel and no target, a drop neither.
const payloadFields = ["target", "channel"];

// Mismatches logged per VU. Past this, the counts in routing_correct say
// enough, and a flood of identical lines would bury the first one.
const maxLogged = 10;
let logged = 0;

// checkRoute checks a single-alert route response: the status, and for a 200
// or a 503 with its fallback, the decision.
export function checkRoute(response, want, status, label) {
  const body = json(response);
  const decided = response.status === 200 || response.status === 503;
  const correct = response.status === status && (!decided || matches(body, want, label));

  check(response, {
    "route answers the expected status": (r) => r.status === status,
    "route decides as expected": () => correct,
  });
  routingCorrect.add(correct);
  if (correct && decided) count(body.decision);

  return correct;
}

// dispatch_failed counts the alerts alertrouter routed but couldn't deliver,
// which only happens when the run injects notifier failures
// (DISPATCH_FAILURES); the log notifier alertrouter ships never fails.
export const dispatchFailed = new Counter("dispatch_failed");

// checkWebhook checks a webhook response: 200, every alert received, and each
// result as expected, matched by fingerprint so the check doesn't depend on
// the order alertrouter answers in. With injected notifier failures, a page
// or notify that failed to dispatch is right if its decision is, and the
// webhook answers 503 for it, so Alertmanager retries; without them, either
// is wrong.
export function checkWebhook(response, want, label, failuresInjected = false) {
  const body = json(response);
  const results = {};
  for (const result of body.results || []) results[result.fingerprint] = result;

  let correct = 0;
  let undelivered = 0;
  for (const expected of want.results) {
    const result = results[expected.fingerprint];
    const failed =
      failuresInjected &&
      result?.status === "dispatch_failed" &&
      expected.status === "routed" &&
      expected.decision !== "drop";
    if (failed) {
      undelivered++;
      dispatchFailed.add(1);
    }
    const alert = failed ? { ...expected, status: "dispatch_failed" } : expected;
    const ok = result !== undefined && matches(result, alert, `${label} ${alert.alertname}`);
    routingCorrect.add(ok);
    if (ok) correct++;
    if (ok && result.decision) count(result.decision);
  }

  check(response, {
    "webhook answers 200, or 503 for an undelivered alert": (r) => r.status === (undelivered > 0 ? 503 : 200),
    "webhook receives every alert": () => body.received === want.results.length,
    "webhook counts the alerts its policies routed": () =>
      body.routed === want.results.filter((alert) => alert.status === "routed").length - undelivered,
    "webhook routes every alert as expected": () => correct === want.results.length,
  });

  return correct === want.results.length;
}

// reloads_accepted and reloads_rejected count the reloads alertrouter took,
// and the ones it refused because run.sh had broken the bundle. While run.sh
// alternates the bundle, a run that saw only one of the two didn't test
// keeping the last good bundle under load, and the thresholds say so.
export const reloadsAccepted = new Counter("reloads_accepted");
export const reloadsRejected = new Counter("reloads_rejected");

// checkReload checks a reload response: 200 with every team's policy
// loaded, or, when badBundleFile names the document run.sh breaks the bundle
// with, a 500 whose error names that document. A reload rejected for any
// other reason is wrong.
export function checkReload(response, teams, badBundleFile) {
  const body = json(response);
  if (response.status === 200) {
    reloadsAccepted.add(1);
    return check(response, {
      "reload answers 200, or rejects the broken bundle": () => true,
      "reload keeps every team policy": () => serves(body, teams),
    });
  }

  const rejected = Boolean(badBundleFile) && response.status === 500 && JSON.stringify(body).includes(badBundleFile);
  if (rejected) reloadsRejected.add(1);
  return check(response, { "reload answers 200, or rejects the broken bundle": () => rejected });
}

// checkServing checks the policies alertrouter serves after a round of
// reloads: whether they were taken or rejected, every team's policy serves.
export function checkServing(response, teams) {
  return check(response, {
    "policies answers 200": (r) => r.status === 200,
    "every team policy still serves": () => serves(json(response), teams),
  });
}

// serves reports whether a PoliciesResponse lists exactly one root policy
// per team.
function serves(body, teams) {
  return JSON.stringify(policies(body)) === JSON.stringify(teams.map((team) => `${team}.alerts`).sort());
}

// matches reports whether an answer has every field the expectation names,
// and, for a decision, no payload field the expectation doesn't name.
function matches(got, want, label) {
  const fields = Object.keys(want).filter((k) => k !== "fingerprint");
  const wrong = fields.filter((k) => got[k] !== want[k]);
  if (want.decision) {
    for (const k of payloadFields) {
      if (want[k] === undefined && got[k] !== undefined && got[k] !== "") wrong.push(k);
    }
  }
  if (wrong.length === 0) return true;

  if (logged < maxLogged) {
    logged++;
    const actual = {};
    for (const k of new Set([...fields, ...payloadFields])) actual[k] = got[k];
    const reason = got.error ? ` (${got.error})` : "";
    console.warn(
      `${label}: ${wrong.join(", ")} wrong; want ${JSON.stringify(want)}, got ${JSON.stringify(actual)}${reason}`,
    );
  }

  return false;
}

// policies lists the root policies a PoliciesResponse says are serving.
function policies(body) {
  return (body.kinds || [])
    .flatMap((kind) => kind.policies || [])
    .map((p) => p.policy)
    .sort();
}

function count(decision) {
  alertsRouted.add(1, { decision });
  if (decisionCounters[decision]) decisionCounters[decision].add(1);
}

function json(response) {
  try {
    return response.json() || {};
  } catch (_) {
    return {};
  }
}
