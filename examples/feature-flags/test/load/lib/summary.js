// The end-of-run report: the full k6 summary with the run's configuration
// and provenance as JSON in results/, and a few lines on stdout.

import * as config from "./config.js";

// report returns handleSummary's outputs for a finished run.
export function report(data, scenarios) {
  const full = {
    run_id: config.runID,
    mode: config.mode,
    target: config.baseURL,
    seed: config.seed,
    ...config.provenance,
    configured_scenarios: scenarios,
    workload: {
      rate: config.rate,
      bulk_share: config.bulkShare,
      reloads_per_minute: config.reloadsPerMinute,
      reload_concurrency: config.reloadConcurrency,
      bad_bundle_every_seconds: config.badBundleEvery,
    },
    latency_budget_ms: config.budget,
    note: "Full HTTP service with per-flag policy evaluation and telemetry, under concurrent policy reloads; not a Sigil engine microbenchmark.",
    ...data,
  };

  const m = data.metrics;
  const value = (name, stat) => m[name]?.values[stat] || 0;
  const failed = Object.values(m).some((metric) => Object.values(metric.thresholds || {}).some((t) => !t.ok));
  const latency = (name) => {
    const values = m[`http_req_duration{name:${name}}`]?.values || {};
    return `p95=${(values["p(95)"] || 0).toFixed(2)} ms, p99=${(values["p(99)"] || 0).toFixed(2)} ms`;
  };

  return {
    [`/results/${config.runID}.json`]: JSON.stringify(full, null, 2),
    stdout:
      `\n${config.runID}: ${failed ? "FAIL" : "PASS"}${data.state?.testRunDurationMs ? ` after ${(data.state.testRunDurationMs / 1000).toFixed(0)}s` : ""} (seed ${config.seed})\n` +
      `${value("http_reqs", "count")} requests, ${value("http_reqs", "rate").toFixed(1)} req/s\n` +
      `single: ${latency("single")}\n` +
      `bulk: ${latency("bulk")}\n` +
      `${value("flags_evaluated", "count")} flags evaluated\n` +
      `evaluations correct=${value("evaluation_correct", "rate")}, failed request rate=${value("http_req_failed", "rate")}, dropped iterations=${value("dropped_iterations", "count")}\n` +
      `reloads: ${value("reloads_accepted", "count")} taken, ${value("reloads_rejected", "count")} rejected\n` +
      `Report: results/${config.runID}.json\n`,
  };
}
