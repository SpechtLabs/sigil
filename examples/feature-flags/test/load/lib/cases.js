// The named request cases, read from requests/cases.json: the same file and
// expected outcomes the Rust tests check (featuregate::cases), so the load test
// can't drift into testing something else.

import { requestsDir } from "./config.js";

// cases is every entry of requests/cases.json, normalized for the checks:
// `flag: null` is a bulk evaluation, `rawBody` a body that isn't JSON, and
// `expect` becomes the fields a response must carry (see wanted).
export const cases = JSON.parse(open(`${requestsDir}/cases.json`)).map((item) => {
  const bulk = item.flag === null || item.flag === undefined;
  const body = item.rawBody !== undefined ? item.rawBody : JSON.stringify({ context: item.context });

  return {
    name: item.name,
    kind: bulk ? "bulk" : "single",
    flag: item.flag,
    body,
    status: item.expect.status || 200,
    expect: wanted(item.expect),
  };
});

// flagsOf lists the flags the named cases evaluate successfully, which
// featuregate must keep serving through every reload and generated contexts
// draw from.
export function flagsOf() {
  const flags = new Set();
  for (const item of cases) {
    if (item.status !== 200) continue;
    if (item.kind === "single") flags.add(item.flag);
    for (const key of Object.keys(item.expect.flags || {})) flags.add(key);
  }
  if (flags.size === 0) throw new Error("requests/cases.json has no case that evaluates a flag");

  return [...flags].sort();
}

// wanted turns a case's expect into the paths checks.js compares: a single
// evaluation's value, reason and variant, Sigil's reason from the metadata
// (a "/" separates path segments, because the metadata keys contain dots),
// or an error's code; a bulk evaluation's flags, each the same.
function wanted(expect) {
  if (expect.flags) {
    const flags = {};
    for (const [key, flag] of Object.entries(expect.flags)) flags[key] = wanted(flag);
    return { flags };
  }

  const want = {};
  if (expect.errorCode) want.errorCode = expect.errorCode;
  for (const field of ["value", "reason", "variant"]) {
    if (expect[field] !== undefined) want[field] = expect[field];
  }
  if (expect.sigilReason !== undefined) want["metadata/sigil.reason"] = expect.sigilReason;

  return want;
}
