// Checks of featuregate's answers against the expected outcomes, and the
// custom metrics they feed. A status code alone says the service answered;
// these say it answered right, flag by flag.

import { check } from "k6";
import { Counter, Rate } from "k6/metrics";

// evaluation_correct is the fraction of flag evaluations whose fields matched
// the expectation. Mimir stores it as k6_evaluation_correct_rate.
export const evaluationCorrect = new Rate("evaluation_correct");

// flags_evaluated counts the flags featuregate answered with a value, tagged
// enabled=true or false.
export const flagsEvaluated = new Counter("flags_evaluated");

// The reasons OFREP defines for a successful evaluation. ERROR is one of them
// but a fail-closed answer is never right under load: the policies are good.
const reasons = ["STATIC", "DEFAULT", "TARGETING_MATCH", "SPLIT", "CACHED", "DISABLED", "UNKNOWN"];

// The regions platform.residency opens; a context from anywhere else must come
// back disabled.
const readyRegions = ["eu-1", "eu-2", "us-1", "us-2"];

// Mismatches logged per VU. Past this, the counts in evaluation_correct say
// enough, and a flood of identical lines would bury the first one.
const maxLogged = 10;
let logged = 0;

// checkSingle checks a single-flag response against a named case: the status,
// and for a 200 every field the case expects.
export function checkSingle(response, want, status, label) {
  const body = json(response);
  const correct = response.status === status && matches(body, want, label);

  check(response, {
    "single answers the expected status": (r) => r.status === status,
    "single evaluates as expected": () => correct,
  });
  evaluationCorrect.add(correct);
  if (correct && status === 200) count(body);

  return correct;
}

// checkBulk checks a bulk response against a named case: the status, and for a 200 each
// expected flag present with its fields, matched by key so the check doesn't
// depend on the order featuregate answers in.
export function checkBulk(response, want, status, label) {
  const body = json(response);
  if (status !== 200) {
    const correct = response.status === status && matches(body, want, label);
    check(response, { "bulk answers the expected status": (r) => r.status === status });
    evaluationCorrect.add(correct);

    return correct;
  }

  const flags = {};
  for (const flag of body.flags || []) flags[flag.key] = flag;

  let correct = 0;
  const keys = Object.keys(want.flags || {});
  for (const key of keys) {
    const ok = flags[key] !== undefined && matches(flags[key], want.flags[key], `${label} ${key}`);
    evaluationCorrect.add(ok);
    if (ok) {
      correct++;
      count(flags[key]);
    }
  }

  check(response, {
    "bulk answers 200": (r) => r.status === 200,
    "bulk evaluates every expected flag as expected": () => correct === keys.length,
    "bulk carries an ETag": (r) => Boolean(r.headers.Etag || r.headers.ETag),
  });

  return correct === keys.length;
}

// checkGenerated checks what holds for every context whatever the policies
// say: a single evaluation of each flag and the bulk evaluation of the same
// context agree, nothing failed closed, and a disabled flag says its off value.
export function checkGenerated(single, bulk, flag, label, context) {
  const one = json(single);
  const many = {};
  for (const item of json(bulk).flags || []) many[item.key] = item;
  const other = many[flag];

  const agree =
    sound(one, flag, context) && sound(other, flag, context) && one.value === other.value && one.variant === other.variant;

  check(single, {
    "generated single answers 200": (r) => r.status === 200,
    "generated single and bulk agree": () => agree,
  });
  evaluationCorrect.add(agree && single.status === 200);
  if (!agree && logged < maxLogged) {
    logged++;
    console.warn(`${label}: single ${JSON.stringify(one)} differs from bulk ${JSON.stringify(other)}`);
  }
  if (agree) count(one);

  return agree;
}

// checkBulkGenerated checks a bulk evaluation of a generated context: every
// served flag is there and sound, and an ETag comes with it.
export function checkBulkGenerated(bulk, flags, label, context) {
  const body = json(bulk);
  const answered = {};
  for (const item of body.flags || []) answered[item.key] = item;
  const all = flags.every((flag) => sound(answered[flag], flag, context));

  check(bulk, {
    "generated bulk answers 200": (r) => r.status === 200,
    "generated bulk answers every flag soundly": () => all,
    "generated bulk carries an ETag": (r) => Boolean(r.headers.Etag || r.headers.ETag),
  });
  evaluationCorrect.add(all && bulk.status === 200);
  if (!all && logged < maxLogged) {
    logged++;
    console.warn(`${label}: bulk answer is not sound for every flag: ${bulk.body}`);
  }
  if (all) for (const flag of flags) count(answered[flag]);

  return all;
}

// sound reports what must hold for any flag and context: the answer names the
// flag, carries a known reason and the Sigil metadata, never failed closed,
// and a disabled flag says false. A region the platform guardrail has not
// opened is never enabled, whatever the flag's own policy says.
function sound(result, flag, context) {
  if (result === undefined || result.key !== flag || !reasons.includes(result.reason)) return false;
  if (result.metadata === undefined) return false;
  if (result.reason === "DISABLED" && !isOff(result)) return false;
  if (!readyRegions.includes(context.region) && !isOff(result)) return false;

  return true;
}

// reloads_accepted and reloads_rejected count the reloads featuregate took,
// and the ones it refused because run.sh had broken the directory. While
// run.sh alternates it, a run that saw only one of the two didn't test
// keeping the last good policies under load, and the thresholds say so.
export const reloadsAccepted = new Counter("reloads_accepted");
export const reloadsRejected = new Counter("reloads_rejected");

// checkReload checks a reload response: 200, or, when badBundleFile names the
// document run.sh breaks the directory with, a 422 whose body names that
// document. A reload rejected for any other reason is wrong.
export function checkReload(response, flags, badBundleFile) {
  if (response.status === 200) {
    reloadsAccepted.add(1);
    return check(response, { "reload answers 200, or rejects the broken directory": () => true });
  }

  const rejected = Boolean(badBundleFile) && response.status === 422 && response.body.includes(badBundleFile);
  if (rejected) reloadsRejected.add(1);
  return check(response, { "reload answers 200, or rejects the broken directory": () => rejected });
}

// checkServing checks the flags featuregate serves after a round of reloads:
// whether they were taken or rejected, every flag the named cases use still
// has its policy.
export function checkServing(response, flags) {
  const served = servedFlags(json(response));
  return check(response, {
    "flags answers 200": (r) => r.status === 200,
    "every flag still serves": () => flags.every((flag) => served.has(flag)),
  });
}

// servedFlags lists the flag keys a flags response says are serving.
function servedFlags(body) {
  const list = Array.isArray(body) ? body : body.flags || [];
  return new Set(list.map((f) => (typeof f === "string" ? f : f.key)));
}

// matches reports whether an answer has every field the expectation names.
// A field may be a path whose segments are separated by "/", such as metadata/sigil.reason.
function matches(got, want, label) {
  const wrong = Object.keys(want).filter((path) => dig(got, path) !== want[path]);
  if (wrong.length === 0) return true;

  if (logged < maxLogged) {
    logged++;
    const actual = {};
    for (const path of Object.keys(want)) actual[path] = dig(got, path);
    const detail = got.errorCode ? ` (${got.errorCode}: ${got.errorDetails})` : "";
    console.warn(`${label}: ${wrong.join(", ")} wrong; want ${JSON.stringify(want)}, got ${JSON.stringify(actual)}${detail}`);
  }

  return false;
}

// isOff reports whether an answer is a flag's off value: false, or the control
// string of a string flag such as search-v2, whose off variant is "control".
function isOff(result) {
  return result.value === false || result.value === "control";
}

function dig(value, path) {
  return path.split("/").reduce((v, key) => (v === undefined || v === null ? undefined : v[key]), value);
}

function count(result) {
  flagsEvaluated.add(1, { enabled: String(!isOff(result) && result.value !== undefined) });
}

function json(response) {
  try {
    return response.json() || {};
  } catch (_) {
    return {};
  }
}
