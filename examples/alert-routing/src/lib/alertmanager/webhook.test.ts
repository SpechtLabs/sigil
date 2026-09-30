import { describe, expect, test } from "bun:test";

import { convert, isResolved, MAX_ALERTS, parseWebhook, severityList, type WebhookAlert } from "./webhook";

const NOW = new Date("2026-01-01T00:12:00Z");

function alert(over: Partial<WebhookAlert> = {}): WebhookAlert {
  return {
    status: "firing",
    labels: { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" },
    startsAt: new Date("2026-01-01T00:00:00Z"),
    fingerprint: "f1",
    problem: undefined,
    ...over,
  };
}

describe("parseWebhook", () => {
  test("reads the carried-over example webhook", async () => {
    const hook = parseWebhook(await Bun.file(new URL("../../../requests/webhook-mixed.json", import.meta.url)).json());
    expect(hook.version).toBe("4");
    expect(hook.receiver).toBe("alertrouter");
    expect(hook.alerts).toHaveLength(6);
    expect(hook.alerts.map((a) => a.problem)).toEqual([
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
    ]);
    expect(hook.alerts[5]?.status).toBe("resolved");
    expect(hook.alerts[0]?.startsAt?.toISOString()).toBe("2026-01-01T00:00:00.000Z");
  });

  test.each([
    ["an array", [], "expected a JSON object"],
    ["null", null, "expected a JSON object"],
    ["no version", { alerts: [] }, `the webhook has version "", not "4"`],
    ["another version", { version: "3", alerts: [] }, `the webhook has version "3", not "4"`],
    ["alerts that aren't a list", { version: "4", alerts: {} }, "alerts isn't a list"],
  ])("refuses %s, the only 400s", (_name, body, message) => {
    expect(() => parseWebhook(body)).toThrow(message);
  });

  test("ignores fields it doesn't read and defaults the ones it does", () => {
    const hook = parseWebhook({ version: "4", something: "new" });
    expect(hook).toEqual({ version: "4", groupKey: "", status: "", receiver: "", truncatedAlerts: 0, alerts: [] });
  });

  test("reads truncatedAlerts", () => {
    expect(parseWebhook({ version: "4", truncatedAlerts: 7, alerts: [] }).truncatedAlerts).toBe(7);
  });

  test("takes more alerts than MAX_ALERTS; the handler decides what to do with them", () => {
    const alerts = Array.from({ length: MAX_ALERTS + 1 }, () => ({ status: "firing", labels: {} }));
    expect(parseWebhook({ version: "4", alerts }).alerts).toHaveLength(MAX_ALERTS + 1);
  });

  test.each([
    ["not an object", 42, "alert 0 isn't a JSON object"],
    ["labels that aren't strings", { status: "firing", labels: { a: 1 } }, "alert 0's labels aren't a map of strings"],
    ["an unknown status", { status: "pending", labels: {} }, `alert 0 has the status "pending"`],
    ["no status", { labels: {} }, `alert 0 has the status ""`],
    ["a startsAt that isn't a time", { status: "firing", labels: {}, startsAt: "yesterday" }, "alert 0's startsAt"],
    ["a numeric startsAt", { status: "firing", labels: {}, startsAt: 5 }, "alert 0's startsAt"],
  ])("marks one alert with %s invalid instead of refusing the batch", (_name, raw, message) => {
    const hook = parseWebhook({ version: "4", alerts: [raw] });
    expect(hook.alerts[0]?.problem?.message).toContain(message);
  });

  test("reads Go's zero time as no startsAt", () => {
    const hook = parseWebhook({
      version: "4",
      alerts: [{ status: "firing", labels: {}, startsAt: "0001-01-01T00:00:00Z" }],
    });
    expect(hook.alerts[0]?.startsAt).toBeUndefined();
    expect(hook.alerts[0]?.problem).toBeUndefined();
  });
});

describe("convert", () => {
  test("reads the kind's alert from the labels, with firing_for from startsAt", () => {
    expect(convert(alert(), NOW)).toEqual({
      name: "CheckoutErrorRate",
      severity: "critical",
      labels: { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" },
      firing_for: "12m",
    });
  });

  test("copies the labels", () => {
    const a = alert();
    const converted = convert(a, NOW);
    converted.labels.env = "staging";
    expect(a.labels.env).toBe("production");
  });

  test("an alert that starts in the future has fired for 0s", () => {
    expect(convert(alert({ startsAt: new Date("2026-01-01T01:00:00Z") }), NOW).firing_for).toBe("0s");
  });

  test.each([
    ["no alertname", { labels: { severity: "critical" } }, "the alert has no alertname label"],
    ["no severity", { labels: { alertname: "X" } }, "alert X has no severity label"],
    [
      "an unknown severity",
      { labels: { alertname: "X", severity: "urgent" } },
      `alert X has the severity "urgent", which the AlertRouting kind doesn't declare`,
    ],
    [
      "a capitalized severity",
      { labels: { alertname: "X", severity: "Critical" } },
      `alert X has the severity "Critical"`,
    ],
    ["no startsAt", { startsAt: undefined }, "alert CheckoutErrorRate has no startsAt"],
  ])("refuses %s", (_name, over, message) => {
    expect(() => convert(alert(over), NOW)).toThrow(message);
  });

  test("a problem found while reading comes first", () => {
    expect(() => convert(alert({ problem: new Error("broken") as never }), NOW)).toThrow("broken");
  });
});

test("isResolved", () => {
  expect(isResolved(alert({ status: "resolved" }))).toBe(true);
  expect(isResolved(alert())).toBe(false);
  expect(isResolved(alert({ status: "pending" }))).toBe(false);
});

test("severityList reads like advice", () => {
  expect(severityList()).toBe("critical, warning or info");
});
