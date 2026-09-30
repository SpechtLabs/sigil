import { describe, expect, test } from "bun:test";
import { parseDuration } from "../policies/suite";
import { parseMetrics } from "./metrics";

// A scrape in prom-client's rendering: help and type lines, labels in any
// order, escaped label values, a histogram and a label-less gauge.
const scrape = `# HELP alertrouter_decisions_total Decisions.
# TYPE alertrouter_decisions_total counter
alertrouter_decisions_total{team="checkout",policy="checkout.alerts",decision="page",reason="critical_alert"} 3
alertrouter_decisions_total{reason="muted",decision="drop",policy="checkout.alerts",team="checkout"} 1
# TYPE alertrouter_policy_last_reload_successful gauge
alertrouter_policy_last_reload_successful 1
# TYPE alertrouter_policy_loaded_info gauge
alertrouter_policy_loaded_info{source="/tmp/a \\"b\\"\\\\c",team="checkout"} 1
# TYPE alertrouter_evaluation_duration_seconds histogram
alertrouter_evaluation_duration_seconds_bucket{le="0.001",team="checkout"} 2
alertrouter_evaluation_duration_seconds_bucket{le="+Inf",team="checkout"} 4
alertrouter_evaluation_duration_seconds_sum{team="checkout"} 0.0125
alertrouter_evaluation_duration_seconds_count{team="checkout"} 4
alertrouter_evaluation_duration_seconds_bucket{le="+Inf",team="payments"} 1
alertrouter_evaluation_duration_seconds_sum{team="payments"} 0.5
alertrouter_evaluation_duration_seconds_count{team="payments"} 1
`;

describe("parseMetrics", () => {
  const f = parseMetrics(scrape);

  test.each([
    ["one series by a subset of its labels", "alertrouter_decisions_total", { decision: "page" }, 3],
    ["labels in another order", "alertrouter_decisions_total", { team: "checkout", reason: "muted" }, 1],
    ["a missing series as zero", "alertrouter_decisions_total", { team: "payments" }, 0],
    ["a label-less gauge", "alertrouter_policy_last_reload_successful", {}, 1],
    ["an escaped label value", "alertrouter_policy_loaded_info", { source: '/tmp/a "b"\\c' }, 1],
  ] as const)("reads %s", (_, name, labels, want) => {
    expect(f.value(name, labels)).toBe(want);
  });

  test("sums and counts series", () => {
    expect(f.sum("alertrouter_decisions_total")).toBe(4);
    expect(f.count("alertrouter_decisions_total")).toBe(2);
    expect(f.count("alertrouter_nothing_total")).toBe(0);
  });

  test("folds a histogram's bucket, sum and count lines into one series per label set", () => {
    expect(f.type("alertrouter_evaluation_duration_seconds")).toBe("histogram");
    expect(f.count("alertrouter_evaluation_duration_seconds")).toBe(2);
    expect(f.find("alertrouter_evaluation_duration_seconds", { team: "checkout" })?.histogram).toEqual({
      buckets: [
        { le: 0.001, count: 2 },
        { le: Number.POSITIVE_INFINITY, count: 4 },
      ],
      count: 4,
      sum: 0.0125,
    });
    expect(f.sampleCount("alertrouter_evaluation_duration_seconds", { team: "payments" })).toBe(1);
  });

  test("reads the special values in any case, as Prometheus does (prom-client writes Nan)", () => {
    const g = parseMetrics('# TYPE g gauge\ng{a="1"} Nan\ng{a="2"} +Inf\ng{a="3"} -inf\n');
    expect(g.value("g", { a: "1" })).toBeNaN();
    expect(g.value("g", { a: "2" })).toBe(Number.POSITIVE_INFINITY);
    expect(g.value("g", { a: "3" })).toBe(Number.NEGATIVE_INFINITY);
  });

  test("refuses a line that isn't a sample", () => {
    expect(() => parseMetrics("alertrouter_decisions_total{team=checkout} 1")).toThrow("label set");
  });
});

describe("parseDuration", () => {
  test.each([
    ["10m", 600_000],
    ["1h30m", 5_400_000],
    ["1h30m0s", 5_400_000],
    ["4m59s", 299_000],
    ["1.5s", 1_500],
    ["250ms", 250],
    ["0", 0],
    ["-5m", -300_000],
  ] as const)("reads %s", (s, ms) => {
    expect(parseDuration(s)).toBe(ms);
  });

  test.each(["", "ten minutes", "5", "#alerts", "checkout-primary"])("refuses %p", (s) => {
    expect(parseDuration(s)).toBeUndefined();
  });
});
