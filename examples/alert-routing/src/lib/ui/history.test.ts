import { describe, expect, test } from "bun:test";
import type { FeedItem, HistoryEntry } from "./api-types";
import {
  applyReload,
  countDecisions,
  destinationKind,
  feedItem,
  groupByDestination,
  MAX_ENTRIES,
  mergeEntries,
  statusFromFiles,
} from "./history";

function entry(id: number, change: Partial<FeedItem> & { decision?: string } = {}): FeedItem {
  const { decision = "notify", ...rest } = change;
  const destination = rest.destination ?? (decision === "drop" ? "-" : "#alerts");
  return {
    id: String(id),
    at: new Date(Date.UTC(2026, 0, 1, 0, 0, id)).toISOString(),
    source: "webhook",
    status: "routed",
    fingerprint: `fp${id}`,
    alertname: `Alert${id}`,
    severity: "warning",
    labels: {},
    response: { team: "checkout", policy: "checkout.alerts", decision, reason: "routine", trace: [] },
    destination,
    delivery: "sent",
    ...rest,
  };
}

describe("mergeEntries", () => {
  test("keeps each id once, newest first, the incoming copy winning", () => {
    const merged = mergeEntries([entry(1), entry(2)], [entry(2, { alertname: "again" }), entry(3)]);
    expect(merged.map((e) => e.id)).toEqual(["3", "2", "1"]);
    expect(merged[1]?.alertname).toBe("again");
  });

  test("orders by id numerically, not as text", () => {
    expect(mergeEntries([], [entry(9), entry(10), entry(100)]).map((e) => e.id)).toEqual(["100", "10", "9"]);
  });

  test("keeps at most MAX_ENTRIES", () => {
    const many = Array.from({ length: MAX_ENTRIES + 10 }, (_, i) => entry(i + 1));
    const merged = mergeEntries([], many);
    expect(merged).toHaveLength(MAX_ENTRIES);
    expect(merged[0]?.id).toBe(String(MAX_ENTRIES + 10));
  });
});

test("countDecisions counts each decision and the alerts no policy decided", () => {
  const counts = countDecisions([
    entry(1, { decision: "page", destination: "oncall" }),
    entry(2, { decision: "page", destination: "oncall" }),
    entry(3, { decision: "drop" }),
    entry(4),
    entry(5, { status: "unowned" }),
    entry(6, { status: "failed", decision: "page", destination: "oncall" }),
    entry(7, { status: "dispatch_failed", decision: "page", destination: "oncall" }),
  ]);
  expect(counts).toEqual({ total: 7, page: 4, drop: 1, notify: 2, fallback: 2, undelivered: 1 });
});

describe("destinationKind", () => {
  test.each<[string, FeedItem, ReturnType<typeof destinationKind>]>([
    ["a page reaches an on-call", entry(1, { decision: "page", destination: "checkout-primary" }), "oncall"],
    ["a notify posts to a channel", entry(2), "channel"],
    ["a drop goes nowhere", entry(3, { decision: "drop" }), "dropped"],
  ])("%s", (_, e, kind) => {
    expect(destinationKind(e)).toBe(kind);
  });
});

test("groupByDestination makes one inbox per destination, the busiest first", () => {
  const inboxes = groupByDestination([
    entry(5, { decision: "page", destination: "checkout-primary" }),
    entry(4, { destination: "#checkout-alerts" }),
    entry(3, { destination: "#checkout-alerts" }),
    entry(2, { decision: "drop" }),
    entry(1, { decision: "page", destination: "checkout-primary" }),
  ]);
  expect(inboxes.map((i) => [i.kind, i.destination, i.entries.length, i.pages])).toEqual([
    ["channel", "#checkout-alerts", 2, 0],
    ["oncall", "checkout-primary", 2, 2],
    ["dropped", "-", 1, 0],
  ]);
  expect(inboxes[1]?.entries.map((e) => e.id)).toEqual(["5", "1"]);
});

describe("feedItem", () => {
  const base: HistoryEntry = {
    id: 7,
    at: "2026-01-01T00:00:00Z",
    source: "webhook",
    alert: { name: "Disk", severity: "warning", labels: { team: "search", env: "production" }, firing_for: "3m" },
    severity: "warning",
    result: {
      fingerprint: "abc",
      alertname: "Disk",
      status: "unowned",
      error: "no team search in the directory",
      policy: "",
      decision: "notify",
      reason: "unrouted",
      channel: "#alerts",
      trace: [],
    },
    notification: { destination: "#alerts", status: "sent" },
    trace_id: "0af7651916cd43dd8448eb211c80319c",
  };

  test("flattens an entry for the pages", () => {
    expect(feedItem(base)).toEqual({
      id: "7",
      at: "2026-01-01T00:00:00Z",
      source: "webhook",
      status: "unowned",
      fingerprint: "abc",
      alertname: "Disk",
      severity: "warning",
      labels: { team: "search", env: "production" },
      firing_for: "3m",
      team_label: "search",
      response: {
        policy: "",
        decision: "notify",
        reason: "unrouted",
        channel: "#alerts",
        trace: [],
        error: { message: "no team search in the directory" },
      },
      destination: "#alerts",
      delivery: "sent",
      trace_id: "0af7651916cd43dd8448eb211c80319c",
    });
  });

  test("reads an alert the router couldn't, and a failed delivery", () => {
    const { alert: _, ...unread } = base;
    const item = feedItem({
      ...unread,
      severity: "urgent",
      result: { ...base.result, status: "invalid", alertname: "Backlog" },
      notification: { destination: "#alerts", status: "late", error: "chat is slow" },
    });
    expect(item).toMatchObject({
      alertname: "Backlog",
      severity: "urgent",
      labels: {},
      delivery: "late",
      notify_error: "chat is slow",
    });
    expect(item.firing_for).toBeUndefined();
    expect(item.team_label).toBeUndefined();
  });
});

describe("policy status", () => {
  const error = { message: "teams/checkout/alerts.sigil:3:1: unknown policy platform.pagin" };

  test("statusFromFiles keeps the failure since the bundle loaded", () => {
    expect(
      statusFromFiles({
        kind: { path: "alert_routing.sigil", source: "" },
        platform: [],
        teams: [],
        required: "platform.paging",
        roots: [],
        fingerprint: "f1",
        source: "/etc/alertrouter/policies",
        loaded_at: "2026-01-01T00:00:00Z",
        last_error: { at: "2026-01-01T00:01:00Z", trigger: "api", error },
      }),
    ).toEqual({
      fingerprint: "f1",
      source: "/etc/alertrouter/policies",
      loaded_at: "2026-01-01T00:00:00Z",
      last_error: { at: "2026-01-01T00:01:00Z", trigger: "api", error },
    });
  });

  test("a failed reload keeps the bundle and becomes the failure; a good one clears it", () => {
    const serving = { fingerprint: "f1", loaded_at: "2026-01-01T00:00:00Z", source: "dir" };
    const failed = applyReload(serving, {
      at: "2026-01-01T00:01:00Z",
      trigger: "interval",
      ok: false,
      source: "dir",
      error,
    });
    expect(failed).toMatchObject({ fingerprint: "f1", last_error: { trigger: "interval", error } });
    const fixed = applyReload(failed, {
      at: "2026-01-01T00:02:00Z",
      trigger: "sighup",
      ok: true,
      fingerprint: "f2",
      source: "dir",
    });
    expect(fixed).toMatchObject({ fingerprint: "f2", loaded_at: "2026-01-01T00:02:00Z" });
    expect(fixed.last_error).toBeUndefined();
  });
});
