// Checks of alertrouter's answers against the expected outcomes, and the
// custom metrics they feed. A status code alone says the service answered;
// these say it answered right, alert by alert.

import { check } from 'k6';
import { Counter, Rate } from 'k6/metrics';

// routing_correct is the fraction of alerts whose decision, reason, target and
// channel matched the expectation. Mimir stores it as k6_routing_correct_rate.
export const routingCorrect = new Rate('routing_correct');

// alerts_routed counts the alerts alertrouter answered with a decision,
// tagged with the decision it answered; the per-decision counters are the
// same count split for the end-of-run summary, which doesn't show tags.
export const alertsRouted = new Counter('alerts_routed');
const decisionCounters = {
  page: new Counter('decisions_page'),
  drop: new Counter('decisions_drop'),
  notify: new Counter('decisions_notify'),
};

// The payload fields a result carries only for some decisions: a page has a
// target and no channel, a notify a channel and no target, a drop neither.
const payloadFields = ['target', 'channel'];

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
    'route answers the expected status': (r) => r.status === status,
    'route decides as expected': () => correct,
  });
  routingCorrect.add(correct);
  if (correct && decided) count(body.decision);

  return correct;
}

// checkWebhook checks a webhook response: 200, every alert received, and each
// result as expected, matched by fingerprint so the check doesn't depend on
// the order alertrouter answers in.
export function checkWebhook(response, want, label) {
  const body = json(response);
  const results = {};
  for (const result of body.results || []) results[result.fingerprint] = result;

  let correct = 0;
  for (const alert of want.results) {
    const result = results[alert.fingerprint];
    const ok = result !== undefined && matches(result, alert, `${label} ${alert.alertname}`);
    routingCorrect.add(ok);
    if (ok) correct++;
    if (ok && result.decision) count(result.decision);
  }

  check(response, {
    'webhook answers 200': (r) => r.status === 200,
    'webhook receives every alert': () => body.received === want.results.length,
    'webhook counts the alerts its policies routed': () =>
      body.routed === want.results.filter((alert) => alert.status === 'routed').length,
    'webhook routes every alert as expected': () => correct === want.results.length,
  });

  return correct === want.results.length;
}

// checkReload checks a reload response: 200 and every team's policy loaded.
export function checkReload(response, teams) {
  const body = json(response);
  const loaded = JSON.stringify(policies(body));
  const want = JSON.stringify(teams.map((team) => `${team}.alerts`).sort());

  return check(response, {
    'reload answers 200': (r) => r.status === 200,
    'reload keeps every team policy': () => loaded === want,
  });
}

// matches reports whether an answer has every field the expectation names,
// and, for a decision, no payload field the expectation doesn't name.
function matches(got, want, label) {
  const fields = Object.keys(want).filter((k) => k !== 'fingerprint');
  const wrong = fields.filter((k) => got[k] !== want[k]);
  if (want.decision) {
    for (const k of payloadFields) {
      if (want[k] === undefined && got[k] !== undefined && got[k] !== '') wrong.push(k);
    }
  }
  if (wrong.length === 0) return true;

  if (logged < maxLogged) {
    logged++;
    const actual = {};
    for (const k of new Set([...fields, ...payloadFields])) actual[k] = got[k];
    const reason = got.error ? ` (${got.error})` : '';
    console.warn(`${label}: ${wrong.join(', ')} wrong; want ${JSON.stringify(want)}, got ${JSON.stringify(actual)}${reason}`);
  }

  return false;
}

// policies lists the root policies a PoliciesResponse says are serving.
function policies(body) {
  return (body.kinds || []).flatMap((kind) => kind.policies || []).map((p) => p.policy).sort();
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
