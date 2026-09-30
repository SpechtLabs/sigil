import { describe, expect, test } from "bun:test";

import { History, type HistoryEntry, type HistoryEvent } from "./history";

function entry(name: string): Omit<HistoryEntry, "id"> {
  return {
    at: "2026-01-01T00:00:00.000Z",
    source: "webhook",
    severity: "critical",
    result: { fingerprint: name, alertname: name, status: "routed" },
    notification: { destination: "-", status: "sent" },
  };
}

describe("History", () => {
  test("keeps the newest entries up to its size, oldest first", () => {
    const h = new History(2);
    for (const n of ["a", "b", "c"]) h.add(entry(n));
    expect(h.entries().map((e) => [e.id, e.result.alertname])).toEqual([
      [2, "b"],
      [3, "c"],
    ]);
    expect(h.entries(2).map((e) => e.id)).toEqual([3]);
  });

  test("a size of 0 keeps nothing, but still streams", () => {
    const h = new History(0);
    const seen: HistoryEvent[] = [];
    h.subscribe((e) => seen.push(e));
    h.add(entry("a"));
    expect(h.entries()).toEqual([]);
    expect(seen).toHaveLength(1);
  });

  test("tells subscribers about entries and reloads until they unsubscribe", () => {
    const h = new History(10);
    const seen: string[] = [];
    const stop = h.subscribe((e) => seen.push(e.type));
    h.add(entry("a"));
    h.reload({ at: "t", trigger: "manual", ok: true, source: "embedded", fingerprint: "abc" });
    expect(h.subscribers).toBe(1);
    stop();
    h.add(entry("b"));
    expect(seen).toEqual(["alert", "reload"]);
    expect(h.subscribers).toBe(0);
  });

  test("a subscriber that throws doesn't stop the others", () => {
    const h = new History(10);
    const seen: number[] = [];
    h.subscribe(() => {
      throw new Error("closed");
    });
    h.subscribe((e) => seen.push(e.type === "alert" ? e.entry.id : 0));
    h.add(entry("a"));
    expect(seen).toEqual([1]);
  });
});
