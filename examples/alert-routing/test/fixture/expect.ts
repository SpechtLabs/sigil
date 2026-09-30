// What an answer must look like, stated once for both suites.
import { expect } from "bun:test";
import {
  type Batch,
  type ManifestCase,
  manifestBody,
  outcomeRoute,
  type ResultCase,
  type Route,
  type RouteCase,
  route,
  UNROUTED,
} from "./cases";
import { type Answer, type Client, decode, expectStatus, routePath } from "./client";
import {
  CHECKOUT_CHANNEL,
  CHECKOUT_ONCALL,
  PAYMENTS_CHANNEL,
  PAYMENTS_ONCALL,
  REASON_UNROUTED,
  STATUS_FAILED,
  STATUS_INVALID,
  STATUS_RESOLVED,
  STATUS_ROUTED,
  STATUS_UNOWNED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "./requests";
import {
  type AlertResult,
  asRoute,
  type ErrorResponse,
  type KindPolicies,
  messages,
  type PoliciesResponse,
  ROOT_SUFFIX,
  type RouteResponse,
  routing,
  type TeamsResponse,
  type WebhookResponse,
  winners,
} from "./wire";

/** Asserts that the answer is c's: 200, whatever the decision, and the route c wants. */
export function expectRouteCase(c: RouteCase, a: Answer, out: RouteResponse): void {
  expectStatus(a, 200);
  expectRoute(c.team, c.want, out);
  expect(out.error).toBeUndefined();
}

/**
 * Asserts that out is want, decided by team's policy: the decision, the
 * reason, and the target or channel, the other one absent, and a trace
 * whose one winner agrees with them.
 */
export function expectRoute(team: string, want: Route, out: RouteResponse): void {
  expect(out.team).toBe(team);
  expect(out.policy).toBe(team + ROOT_SUFFIX);
  expect(routeOf(out)).toEqual(want);
  // trace must be [], not null or missing.
  expect(Array.isArray(out.trace)).toBe(true);

  // The kind's default wins when no rule fires, and it has no candidate in
  // the trace to mark.
  if ((out.trace ?? []).length === 0) {
    expect(want.reason).toBe(REASON_UNROUTED);
    return;
  }

  const w = winners(out.trace);
  expect(w).toHaveLength(1);
  expect(w[0]?.decision).toBe(want.decision);
  expect(w[0]?.reason).toBe(want.reason);
  expect(w[0]?.location).not.toBe("");
}

/**
 * Asserts the answer to an alert whose evaluation failed: status, which is
 * 503 for a timeout, 500 for a policy that failed, and 422 for an alert that
 * failed an input assert; the kind's default, which the host acts on so the
 * alert still reaches #alerts; and an error whose messages contain each of
 * want.
 */
export function expectFallback(team: string, status: number, a: Answer, out: RouteResponse, ...want: string[]): void {
  expectStatus(a, status);
  expect(out.team).toBe(team);
  expect(out.policy).toBe(team + ROOT_SUFFIX);
  expect(routeOf(out)).toEqual(UNROUTED);
  expect(out.error).toBeDefined();
  for (const msg of want) expect(messages(out.error).some((m) => m.includes(msg))).toBe(true);
}

/**
 * Asserts that the answer is b's: 200, because Alertmanager retries anything
 * else; every alert counted and answered in the order it came; routed
 * counting the alerts a policy decided; and each result as b wants it.
 */
export function expectBatch(b: Batch, a: Answer, out: WebhookResponse): void {
  expectStatus(a, 200);
  expect(out.received).toBe(b.webhook.alerts.length);
  expect(out.results.map((r) => r.fingerprint)).toEqual(b.results.map((r) => r.fingerprint));
  b.results.forEach((want, i) => {
    const got = out.results[i];
    if (got !== undefined) expectResult(want, got);
  });
  expect(out.routed).toBe(b.results.filter((r) => r.status === STATUS_ROUTED).length);
}

/**
 * Asserts one alert's result: its status, its team when want names one, and
 * the route it took. A resolved alert takes none. An unowned or invalid
 * alert takes the kind's default without an evaluation, so it has no trace,
 * and says why no policy decided it; an unowned one names no team and no
 * policy either. A failed one carries the fallback and says what failed.
 */
export function expectResult(want: ResultCase, got: AlertResult): void {
  const at = `result ${JSON.stringify(got)}`;
  expect(got.fingerprint, at).toBe(want.fingerprint);
  if (want.team !== undefined) expect(got.team, at).toBe(want.team);
  expect(got.status, at).toBe(want.status);

  switch (want.status) {
    case STATUS_ROUTED:
      expectRoute(want.team ?? "", want.want as Route, asRoute(got));
      expect(got.error ?? "", at).toBe("");
      break;
    case STATUS_RESOLVED:
      // A resolved alert is only acknowledged: no route fields at all.
      expect(got.decision, at).toBeUndefined();
      expect(got.error ?? "", at).toBe("");
      expect(got.trace ?? [], at).toEqual([]);
      break;
    case STATUS_UNOWNED:
      // No team owns it, whatever its label says; the error names the label.
      expect(routeOf(got), at).toEqual(want.want as Route);
      expect(got.team ?? "", at).toBe("");
      // No policy ran, so the result names none.
      expect(got.policy, at).toBeUndefined();
      expect(got.trace ?? [], at).toEqual([]);
      expect(got.error ?? "", "an unowned alert must say why no policy decided it").not.toBe("");
      break;
    case STATUS_INVALID:
      expect(routeOf(got), at).toEqual(want.want as Route);
      expect(got.policy, at).toBeUndefined();
      expect(got.error ?? "", "an invalid alert must say what is wrong with it").not.toBe("");
      expect(got.trace ?? [], at).toEqual([]);
      break;
    case STATUS_FAILED:
      // A conflict keeps the candidates that fired, so the trace may hold
      // them; the error says what failed.
      expect(routeOf(got), at).toEqual(want.want as Route);
      expect(got.error ?? "", "a failed alert must say what went wrong").not.toBe("");
      break;
  }
}

/**
 * Asserts the policies listing and returns its one entry: the AlertRouting
 * kind with its contract version, one root per team in the directory, where
 * the bundle came from, its fingerprint and when it loaded.
 */
export function expectServedPolicies(list: PoliciesResponse): KindPolicies {
  expect(list.kinds).toHaveLength(1);
  const got = routing(list);
  expect(got).toBeDefined();
  const k = got as KindPolicies;
  expect(k.version).toBe(1);
  expect(k.source).not.toBe("");
  expect(k.fingerprint).not.toBe("");
  expect(Number.isNaN(Date.parse(k.loaded_at))).toBe(false);
  expect(sortBy(k.policies, (p) => p.team)).toEqual([
    { team: TEAM_CHECKOUT, policy: TEAM_CHECKOUT + ROOT_SUFFIX },
    { team: TEAM_PAYMENTS, policy: TEAM_PAYMENTS + ROOT_SUFFIX },
  ]);
  return k;
}

/** Asserts the default team directory, which the integration suite and the compose stack both serve. */
export function expectDefaultTeams(got: TeamsResponse): void {
  expect(sortBy(got.teams, (t) => t.name)).toEqual([
    { name: TEAM_CHECKOUT, oncall: CHECKOUT_ONCALL, channel: CHECKOUT_CHANNEL },
    { name: TEAM_PAYMENTS, oncall: PAYMENTS_ONCALL, channel: PAYMENTS_CHANNEL },
  ]);
}

/**
 * Sends c's request body from requests/ byte for byte, and asserts the
 * answer requests/cases.json expects: the route of a single alert, the error
 * of one the service refuses, or the result of every alert of a webhook.
 */
export async function expectManifestCase(client: Client, c: ManifestCase): Promise<void> {
  const body = manifestBody(c);
  switch (c.kind) {
    case "route": {
      const a = await client.postRaw(routePath(c.team ?? ""), body);
      const status = c.status ?? 200;
      expectStatus(a, status);
      if (status !== 200) {
        // A refused request has no route, only the error that says why.
        expect(decode<ErrorResponse>(a).error).toBeDefined();
        return;
      }
      expectRoute(c.team ?? "", outcomeRoute(c.expect), decode<RouteResponse>(a));
      return;
    }
    case "webhook": {
      const webhook = JSON.parse(body) as Batch["webhook"];
      expect(c.expect.received, `${c.name} expects a different count than its file holds`).toBe(webhook.alerts.length);
      const batch: Batch = {
        webhook,
        results: (c.expect.results ?? []).map((r) => ({
          fingerprint: r.fingerprint,
          status: r.status,
          team: r.team === "" ? undefined : r.team,
          want: r.status === STATUS_RESOLVED ? undefined : outcomeRoute(r),
        })),
      };
      const a = await client.webhookRaw(body);
      expectBatch(batch, a, a.out);
      expect(a.out.routed, `${c.name} expects a different routed count`).toBe(c.expect.routed ?? 0);
      return;
    }
    default:
      throw new Error(`case ${(c as ManifestCase).name} has kind ${(c as ManifestCase).kind}, not route or webhook`);
  }
}

/** The route an answer took, with its empty fields left out. */
export function routeOf(r: { decision?: string; reason?: string; target?: string; channel?: string }): Route {
  return route(r.decision ?? "", r.reason ?? "", r.target, r.channel);
}

/**
 * Where a route goes, as the logs and the notification metric name it: the
 * paged target, the channel, or "-" for a drop.
 */
export function destination(r: Route): string {
  return r.target ?? r.channel ?? "-";
}

function sortBy<T>(xs: T[], key: (x: T) => string): T[] {
  return [...xs].sort((a, b) => key(a).localeCompare(key(b)));
}
