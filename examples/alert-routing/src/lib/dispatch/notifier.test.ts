import { expect, test } from "bun:test";

import { fakeTelemetry } from "../testing";
import { destination, LogNotifier } from "./notifier";

test.each([
  [{ target: "checkout-primary" }, "checkout-primary"],
  [{ channel: "#alerts" }, "#alerts"],
  [{ target: "", channel: "#alerts" }, "#alerts"],
  [{}, "-"],
])("destination(%p) is %s", (n, want) => {
  expect(destination(n)).toBe(want);
});

test("LogNotifier writes one line per notification, leaving policy out when none ran", async () => {
  const { telemetry, logs } = fakeTelemetry();
  const notifier = new LogNotifier(telemetry.logger);
  await notifier.notify({
    team: "-",
    alertname: "X",
    fingerprint: "f",
    decision: "notify",
    reason: "unrouted",
    channel: "#alerts",
  });
  await notifier.notify({
    team: "checkout",
    alertname: "Y",
    fingerprint: "g",
    policy: "checkout.alerts",
    decision: "page",
    reason: "critical_alert",
    target: "checkout-primary",
  });
  expect(logs).toEqual([
    {
      level: "info",
      msg: "notification dispatched",
      team: "-",
      alertname: "X",
      fingerprint: "f",
      decision: "notify",
      reason: "unrouted",
      destination: "#alerts",
    },
    {
      level: "info",
      msg: "notification dispatched",
      team: "checkout",
      alertname: "Y",
      fingerprint: "g",
      policy: "checkout.alerts",
      decision: "page",
      reason: "critical_alert",
      destination: "checkout-primary",
    },
  ]);
});
