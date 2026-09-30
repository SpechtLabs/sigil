// The Grafana dashboards the compose stack provisions. Grafana loads them as
// they are, so these tests are what catch a hand edit that breaks the JSON,
// stacks one panel on another, or queries a metric alertrouter doesn't export.

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { METRIC_LABELS, PromMetrics } from "../../../src/lib/telemetry/metrics";

/** The width of Grafana's dashboard grid, in columns. */
const GRID_WIDTH = 24;

/** Labels every scraped series carries besides its own: Alloy's job and instance, and le on a histogram's buckets. */
const SCRAPE_LABELS = ["job", "instance", "le"];

/** A series alertrouter exports in a PromQL expression, and its label matchers if it has any. */
const SELECTOR = /\b((?:alertrouter|nodejs|process)_[a-z0-9_]+)(\{[^}]*\})?/g;

/** One label matcher inside a selector's braces. */
const MATCHER = /([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)/g;

/** What the tests read of a dashboard panel. */
interface Panel {
  id: number;
  title: string;
  type: string;
  panels?: Panel[];
  targets?: { expr?: string }[];
  gridPos: { x: number; y: number; w: number; h: number };
}

interface Dashboard {
  uid: string;
  panels: Panel[];
  links: { title: string; url: string }[];
}

const raw = readFileSync(new URL("./alertrouter.json", import.meta.url), "utf8");
const dashboard = JSON.parse(raw) as Dashboard;

/**
 * The profile types @pyroscope/nodejs uploads, as Pyroscope names them: CPU
 * time and wall-clock time from the wall profiler, and the sampled live heap.
 * A panel, link or datasource asking for another type, such as Go's
 * process_cpu or goroutines, shows nothing for this service.
 */
const NODE_PROFILE_TYPES = [
  "wall:cpu:nanoseconds:wall:nanoseconds",
  "wall:wall:nanoseconds:wall:nanoseconds",
  "wall:samples:count:wall:nanoseconds",
  "memory:inuse_space:bytes:inuse_space:bytes",
  "memory:inuse_objects:count:inuse_space:bytes",
];

/** A Pyroscope profile type id: name, sample type and unit, period type and unit. */
const PROFILE_TYPE = /\b[a-z_]+:[a-z_]+:[a-z_]+:[a-z_]+:[a-z_]+\b/g;

/**
 * Every series alertrouter exports that the dashboard may query, with its
 * labels: the service's metric contract, and the Node runtime and process
 * metrics prom-client registers next to it, read from a real registry. A
 * query for anything else is empty in Grafana without an error, so this is
 * the only place a renamed metric or label shows up.
 */
async function exported(): Promise<Map<string, readonly string[]>> {
  const metrics = new Map<string, readonly string[]>(Object.entries(METRIC_LABELS));
  const registry = new PromMetrics().registry;
  for (const { name } of await registry.getMetricsAsJSON()) {
    if (metrics.has(name)) continue;
    const metric = registry.getSingleMetric(name) as unknown as { labelNames?: readonly string[] };
    metrics.set(name, metric.labelNames ?? []);
  }
  return metrics;
}

/** Strips the suffix Prometheus adds to a histogram's series, so a _bucket query finds its histogram. */
function series(name: string, known: Map<string, readonly string[]>): string {
  for (const suffix of ["_bucket", "_sum", "_count"]) {
    if (name.endsWith(suffix) && known.has(name.slice(0, -suffix.length))) return name.slice(0, -suffix.length);
  }
  return name;
}

/** Whether two panels share a grid cell. */
function overlap(a: Panel, b: Panel): boolean {
  const [ag, bg] = [a.gridPos, b.gridPos];
  return ag.x < bg.x + bg.w && bg.x < ag.x + ag.w && ag.y < bg.y + bg.h && bg.y < ag.y + ag.h;
}

describe("the alertrouter dashboard", () => {
  test("has the uid the README links to", () => {
    expect(dashboard.uid).toBe("alertrouter");
  });

  test("gives every panel its own id, and keeps every row open", () => {
    const ids = dashboard.panels.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
    // A collapsed row carries its panels inline, at the positions they take
    // once it opens, which the overlap check below can't see.
    expect(dashboard.panels.filter((p) => (p.panels?.length ?? 0) > 0).map((p) => p.title)).toEqual([]);
  });

  test.each(dashboard.panels.map((p) => [p.title, p] as const))("%s lies inside the grid", (_title, p) => {
    const g = p.gridPos;
    expect(g.w).toBeGreaterThan(0);
    expect(g.h).toBeGreaterThan(0);
    expect(g.x).toBeGreaterThanOrEqual(0);
    expect(g.y).toBeGreaterThanOrEqual(0);
    expect(g.x + g.w).toBeLessThanOrEqual(GRID_WIDTH);
  });

  test("stacks no panel on another", () => {
    const overlaps: string[] = [];
    dashboard.panels.forEach((a, i) => {
      for (const b of dashboard.panels.slice(i + 1)) {
        if (overlap(a, b)) overlaps.push(`${a.title} and ${b.title}`);
      }
    });
    expect(overlaps).toEqual([]);
  });

  test("queries only metrics alertrouter exports, with labels they carry", async () => {
    const known = await exported();
    const wrong: string[] = [];
    for (const p of dashboard.panels) {
      for (const q of p.targets ?? []) {
        for (const [, raw = "", matchers = ""] of (q.expr ?? "").matchAll(SELECTOR)) {
          const name = series(raw, known);
          const labels = known.get(name);
          if (!labels) {
            wrong.push(`${p.title} queries ${raw}, which alertrouter doesn't export`);
            continue;
          }
          for (const [, label = ""] of matchers.matchAll(MATCHER)) {
            if (!labels.includes(label) && !SCRAPE_LABELS.includes(label)) {
              wrong.push(`${p.title} matches label ${label} on ${name}, which has only ${labels.join(", ")}`);
            }
          }
        }
      }
    }
    expect(wrong).toEqual([]);
  });

  test("shows every alertrouter_* metric on some panel", async () => {
    const known = await exported();
    const queried = new Set<string>();
    for (const p of dashboard.panels) {
      for (const q of p.targets ?? []) {
        for (const [, raw = ""] of (q.expr ?? "").matchAll(SELECTOR)) queried.add(series(raw, known));
      }
    }
    // A metric no panel shows is one an operator can't see without writing
    // the query themselves.
    expect(Object.keys(METRIC_LABELS).filter((name) => !queried.has(name))).toEqual([]);
  });
});

describe("the profiles", () => {
  const datasources = readFileSync(new URL("../provisioning/datasources/datasources.yaml", import.meta.url), "utf8");

  test.each([
    [
      "the dashboard's panels, variable and links",
      [raw, ...dashboard.links.map((l) => decodeURIComponent(l.url))].join("\n"),
    ],
    ["the Tempo datasource's traces-to-profiles link", datasources],
  ])("%s ask only for types the Node service uploads", (_where, text) => {
    const asked = [...new Set(text.match(PROFILE_TYPE) ?? [])];
    expect(asked.length).toBeGreaterThan(0);
    expect(asked.filter((t) => !NODE_PROFILE_TYPES.includes(t))).toEqual([]);
  });
});
