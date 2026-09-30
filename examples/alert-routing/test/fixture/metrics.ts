// Reads the Prometheus text format /metrics serves (and prom-client's
// registry renders), so both suites compare label sets and values instead
// of matching strings that change with label order or float formatting.

/**
 * The metric names the service promises. The Grafana dashboard, the k6
 * suite and the alerts an operator writes depend on them, so a rename
 * should fail a suite.
 */
export const METRIC_ALERTS_RECEIVED = "alertrouter_alerts_received_total";
export const METRIC_ALERTS_ROUTED = "alertrouter_alerts_routed_total";
export const METRIC_DECISIONS = "alertrouter_decisions_total";
export const METRIC_EVAL_DURATION = "alertrouter_evaluation_duration_seconds";
export const METRIC_EVAL_ERRORS = "alertrouter_evaluation_errors_total";
export const METRIC_NOTIFICATIONS = "alertrouter_notifications_total";
export const METRIC_BATCH_SIZE = "alertrouter_webhook_batch_size";
export const METRIC_RELOADS = "alertrouter_policy_reloads_total";
export const METRIC_LAST_RELOAD = "alertrouter_policy_last_reload_timestamp_seconds";
export const METRIC_RELOAD_OK = "alertrouter_policy_last_reload_successful";
export const METRIC_POLICY_INFO = "alertrouter_policy_loaded_info";
export const METRIC_REQUESTS = "alertrouter_requests_total";
export const METRIC_REQUEST_DURATION = "alertrouter_request_duration_seconds";

/** Labels that select series: a series matches when it carries every pair. */
export type Labels = Record<string, string>;

/** A histogram series: its buckets (cumulative, by upper bound), count and sum. */
export interface Histogram {
  buckets: { le: number; count: number }[];
  count: number;
  sum: number;
}

/** One series. value is set for a counter, gauge or untyped series, histogram for a histogram or summary. */
export interface Series {
  labels: Labels;
  value?: number;
  histogram?: Histogram;
}

/** One metric family. */
export interface Family {
  name: string;
  type: string;
  series: Series[];
}

/** Metric families by name. */
export class Families {
  readonly byName: Map<string, Family>;

  constructor(byName: Map<string, Family>) {
    this.byName = byName;
  }

  has(name: string): boolean {
    return this.byName.has(name);
  }

  type(name: string): string | undefined {
    return this.byName.get(name)?.type;
  }

  /** The first series of the named family that matches labels, or undefined. */
  find(name: string, labels: Labels = {}): Series | undefined {
    return this.byName.get(name)?.series.find((s) => matches(s, labels));
  }

  /**
   * The value of a counter or gauge series, and zero when it doesn't exist
   * yet: a counter with labels only appears after its first increment,
   * which is the "before" of many specs.
   */
  value(name: string, labels: Labels = {}): number {
    return this.find(name, labels)?.value ?? 0;
  }

  /** The sum of the series of the named family that match labels, zero when none does. */
  sum(name: string, labels: Labels = {}): number {
    let sum = 0;
    for (const s of this.byName.get(name)?.series ?? []) if (matches(s, labels)) sum += s.value ?? 0;
    return sum;
  }

  /** How many series of the named family match labels. */
  count(name: string, labels: Labels = {}): number {
    return (this.byName.get(name)?.series ?? []).filter((s) => matches(s, labels)).length;
  }

  /** The sample count of a histogram series, zero when it doesn't exist. */
  sampleCount(name: string, labels: Labels = {}): number {
    return this.find(name, labels)?.histogram?.count ?? 0;
  }
}

/**
 * Parses the Prometheus text exposition format. It throws on a line it
 * can't read, with the line, so a malformed scrape fails loudly.
 */
export function parseMetrics(text: string): Families {
  const families = new Map<string, Family>();
  const types = new Map<string, string>();
  // A histogram's samples are keyed by their labels without le, so its
  // _bucket, _sum and _count lines land in one series.
  const histograms = new Map<string, Series>();

  const family = (name: string): Family => {
    let f = families.get(name);
    if (f === undefined) {
      f = { name, type: types.get(name) ?? "untyped", series: [] };
      families.set(name, f);
    }
    return f;
  };

  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (line === "") continue;
    if (line.startsWith("#")) {
      const m = /^#\s+TYPE\s+(\S+)\s+(\S+)/.exec(line);
      if (m !== null) {
        types.set(m[1] as string, m[2] as string);
        family(m[1] as string).type = m[2] as string;
      }
      continue;
    }

    const { name, labels, value } = parseSample(line);
    const base = histogramBase(name, types);
    if (base === undefined) {
      family(name).series.push({ labels, value });
      continue;
    }

    const { le, ...rest } = labels;
    const key = `${base}${JSON.stringify(Object.entries(rest).sort())}`;
    let s = histograms.get(key);
    if (s === undefined) {
      s = { labels: rest, histogram: { buckets: [], count: 0, sum: 0 } };
      histograms.set(key, s);
      family(base).series.push(s);
    }
    const h = s.histogram as Histogram;
    if (name.endsWith("_bucket")) h.buckets.push({ le: parseValue(le ?? "+Inf"), count: value });
    else if (name.endsWith("_sum")) h.sum = value;
    else if (name.endsWith("_count")) h.count = value;
    else if (le === undefined && labels.quantile === undefined) s.value = value;
  }

  return new Families(families);
}

function matches(s: Series, labels: Labels): boolean {
  return Object.entries(labels).every(([k, v]) => (s.labels[k] ?? "") === v);
}

// The family a histogram or summary sample belongs to, or undefined for a
// sample of any other type.
function histogramBase(name: string, types: Map<string, string>): string | undefined {
  for (const suffix of ["_bucket", "_sum", "_count"]) {
    if (!name.endsWith(suffix)) continue;
    const base = name.slice(0, -suffix.length);
    const t = types.get(base);
    if (t === "histogram" || t === "summary") return base;
  }
  const t = types.get(name);
  return t === "summary" ? name : undefined;
}

function parseSample(line: string): { name: string; labels: Labels; value: number } {
  const nameEnd = line.search(/[{\s]/);
  if (nameEnd <= 0) throw new Error(`not a Prometheus sample: ${line}`);
  const name = line.slice(0, nameEnd);
  const labels: Labels = {};
  let i = nameEnd;

  if (line[i] === "{") {
    i++;
    while (line[i] !== "}") {
      const eq = line.indexOf("=", i);
      if (eq < 0 || line[eq + 1] !== '"') throw new Error(`not a Prometheus label set: ${line}`);
      const key = line.slice(i, eq).trim();
      let j = eq + 2;
      let v = "";
      while (line[j] !== '"') {
        if (j >= line.length) throw new Error(`unterminated label value: ${line}`);
        if (line[j] === "\\") {
          const next = line[j + 1];
          v += next === "n" ? "\n" : (next ?? "");
          j += 2;
          continue;
        }
        v += line[j];
        j++;
      }
      labels[key] = v;
      i = j + 1;
      if (line[i] === ",") i++;
      while (line[i] === " ") i++;
    }
    i++;
  }

  const rest = line.slice(i).trim().split(/\s+/);
  return { name, labels, value: parseValue(rest[0] ?? "") };
}

// Prometheus reads the special values case-insensitively (Go's ParseFloat), and
// prom-client writes NaN as "Nan".
function parseValue(s: string): number {
  switch (s.toLowerCase()) {
    case "+inf":
    case "inf":
      return Number.POSITIVE_INFINITY;
    case "-inf":
      return Number.NEGATIVE_INFINITY;
    case "nan":
      return Number.NaN;
  }
  const v = Number(s);
  if (s === "" || Number.isNaN(v)) throw new Error(`not a Prometheus value: ${s}`);
  return v;
}
