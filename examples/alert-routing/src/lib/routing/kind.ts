// The AlertRouting kind: the contract every alert policy is checked
// against, defined in TypeScript with @spechtlabs/sigil's builder. It is the
// source of truth: `bun run export-kind` writes it out as
// policies/alert_routing.sigil for the tooling that runs without this code
// (sigil check, sigil test, the browser preview), and kind.test.ts fails when
// that copy is stale. The kind is immutable data, so it's a module constant
// like Go's `var Kind`; nothing with state is created here.

import { decision, defineKind, enumType, type InputOf, struct, t } from "@spechtlabs/sigil";

/** The severities, in the order the kind declares them. */
export const SEVERITIES = ["critical", "warning", "info"] as const;

export type Severity = (typeof SEVERITIES)[number];

/**
 * How bad an alert says it is. A misspelled severity is a compile error in a
 * policy instead of a comparison that never matches.
 */
export const SeverityEnum = enumType("Severity", SEVERITIES);

/** One firing alert, as the monitoring system reports it. */
export const AlertType = struct("Alert", {
  name: t.string,
  severity: SeverityEnum,
  labels: t.map(t.string, t.string),
  firing_for: t.duration,
});

/** The team that owns an alert. */
export const TeamType = struct("Team", {
  name: t.string,
  oncall: t.string,
  channel: t.string,
});

/**
 * Where a notification is posted when the notifying rule doesn't name a
 * channel, which makes it where the kind's default, notify(reason:
 * unrouted), posts, and where alertrouter posts an alert no policy ran for.
 */
export const DEFAULT_CHANNEL = "#alerts";

export const Page = decision("page", ["critical_alert", "sustained"], { target: t.string });
export const Drop = decision("drop", ["muted", "not_production"]);
export const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default(DEFAULT_CHANNEL) });

/**
 * The kind's default: an alert no rule routed, no policy ran for, or whose
 * evaluation failed is posted to #alerts rather than lost.
 */
export const Unrouted = Notify.reason("unrouted");

/**
 * AlertRouting, version 1. Decisions are listed in precedence order: a page
 * beats a drop beats a notification, so muting an alert silences its
 * notifications but never a page. Every decision's reasons are ranked in the
 * order declared, so two rules of the same decision never conflict over the
 * reason, only over a payload.
 */
export const AlertRouting = defineKind("AlertRouting", {
  version: 1,
  inputs: { alert: AlertType, team: TeamType },
  decisions: [Page, Drop, Notify],
  reasonPrecedence: [Page, Drop, Notify],
  default: Unrouted,
});

/** What a policy of the kind reads: the alert and the team that owns it. */
export type Input = InputOf<typeof AlertRouting>;

export type Alert = Input["alert"];

export type Team = Input["team"];

/** The severity s names, or undefined. The match is exact: "Critical" isn't critical. */
export function parseSeverity(s: string): Severity | undefined {
  return (SEVERITIES as readonly string[]).includes(s) ? (s as Severity) : undefined;
}
