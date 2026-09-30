// The named request cases, read from requests/cases.json: the same files and
// expected outcomes the TypeScript tests check, so the load test can't drift
// into testing something else.

import { requestsDir } from "./config.js";
import { expect as expected, parseDuration } from "./rules.js";

// cases is every entry of requests/cases.json with its request body loaded.
// open() only works while the script initializes, so this reads them all up
// front, in every VU.
export const cases = JSON.parse(open(`${requestsDir}/cases.json`)).map((item) => {
  if (item.kind !== "route" && item.kind !== "webhook") {
    throw new Error(`requests/cases.json: case ${item.name} has kind ${item.kind}; want route or webhook`);
  }
  if (item.kind === "route" && !item.team) {
    throw new Error(`requests/cases.json: route case ${item.name} names no team`);
  }

  return { ...item, status: item.status || 200, body: open(`${requestsDir}/${item.file}`) };
});

// verifyModel checks lib/rules.js against every route case that expects a
// decision, for the teams in the directory, and throws on the first
// disagreement: generated alerts are only checked right if the model is.
export function verifyModel(directory) {
  let verified = 0;
  for (const item of cases) {
    if (item.kind !== "route" || item.status !== 200 || !directory[item.team]) continue;
    const alert = JSON.parse(item.body).alert;
    const got = expected(
      { ...alert, labels: alert.labels || {}, firing_for: parseDuration(alert.firing_for) },
      directory[item.team],
    );
    for (const field of ["decision", "reason", "target", "channel"]) {
      if (got[field] !== item.expect[field]) {
        throw new Error(
          `lib/rules.js expects ${JSON.stringify(got)} for case ${item.name}, but requests/cases.json says ${JSON.stringify(item.expect)}: mirror the policy change in lib/rules.js`,
        );
      }
    }
    verified++;
  }
  if (verified === 0) throw new Error("requests/cases.json has no route case to check lib/rules.js against");

  return verified;
}
