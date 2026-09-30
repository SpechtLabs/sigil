import { describe, expect, test } from "bun:test";

import { loadSigil } from "../sigil";
import { AlertRouting, DEFAULT_CHANNEL, Drop, Notify, Page, parseSeverity, Unrouted } from "./kind";

const KIND_FILE = new URL("../../../policies/alert_routing.sigil", import.meta.url);

describe("AlertRouting", () => {
  // policies/alert_routing.sigil was written by the Go service's
  // `sigilc export` from its Go types, so this is also the parity check:
  // the TypeScript kind is byte for byte the Go kind.
  test("policies/alert_routing.sigil is the kind's current export; run `bun run export-kind` when this fails", async () => {
    expect(AlertRouting.schema()).toBe(await Bun.file(KIND_FILE).text());
  });

  test("the engine accepts the kind file", async () => {
    const sigil = await loadSigil();
    expect(AlertRouting.check(sigil)).toEqual([]);
  });

  test("is version 1 and names its file alert_routing.sigil", () => {
    expect(AlertRouting.version).toBe(1);
    expect(AlertRouting.file().path).toBe("alert_routing.sigil");
  });

  test("the default posts to #alerts", () => {
    expect(Unrouted.toString()).toBe("notify.unrouted");
    expect(DEFAULT_CHANNEL).toBe("#alerts");
    expect(AlertRouting.schema()).toContain('channel: string = "#alerts"');
  });

  test("the decision handles name the kind's decisions and reasons", () => {
    expect([Page.name, Drop.name, Notify.name]).toEqual(["page", "drop", "notify"]);
    expect(Page.reasons).toEqual(["critical_alert", "sustained"]);
    expect(() => Page.reason("sustaned" as never)).toThrow('decision page has no reason "sustaned"');
  });
});

test.each([
  ["critical", "critical"],
  ["warning", "warning"],
  ["info", "info"],
  ["Critical", undefined],
  ["urgent", undefined],
  ["", undefined],
])("parseSeverity(%j)", (s, want) => {
  expect(parseSeverity(s)).toBe(want as never);
});
