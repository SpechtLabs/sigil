// An HTTP client for one alertrouter (or one observability backend) that
// reads every answer whole, so the specs only state what they expect of it.
import { expect } from "bun:test";
import { randomBytes } from "node:crypto";
import type { PoliciesResponse, RouteRequest, RouteResponse, TeamsResponse, Webhook, WebhookResponse } from "./wire";
import { KIND_ROUTING, type KindPolicies, routing } from "./wire";

/** The API's paths, as a client spells them. */
export const PATH_ALERTS = "/api/v1/alerts";
export const PATH_TEAMS = "/api/v1/teams";
export const PATH_POLICIES = "/api/v1/policies";
export const PATH_RELOAD = "/api/v1/policies/reload";
export const PATH_EVENTS = "/api/v1/events";
export const PATH_HEALTHZ = "/healthz";
export const PATH_READYZ = "/readyz";
export const PATH_METRICS = "/metrics";

/** The single-alert endpoint of team. */
export function routePath(team: string): string {
  return `/api/v1/teams/${team}/route`;
}

/**
 * Bounds every call a Client makes. The servers under test run locally, so
 * anything slower than this is a hang, not a slow answer.
 */
const REQUEST_TIMEOUT_MS = 10_000;

/** One answer, read whole. */
export interface Answer {
  status: number;
  headers: Headers;
  body: string;
}

/** An answer whose JSON body is decoded, when it has one. */
export interface Decoded<T> extends Answer {
  out: T;
}

/** Calls one HTTP server. */
export class Client {
  readonly baseURL: string;
  /** The W3C trace context every request carries; see {@link traced}. */
  readonly #traceparent: string | undefined;

  constructor(baseURL: string, traceparent?: string) {
    this.baseURL = baseURL.replace(/\/$/, "");
    this.#traceparent = traceparent;
  }

  /**
   * A copy whose requests all belong to one new, sampled trace, and that
   * trace's ID. alertrouter continues the trace it's given, so a spec can
   * fetch the spans of exactly its own requests by ID instead of searching.
   */
  traced(): { client: Client; traceID: string } {
    const traceID = randomBytes(16).toString("hex");
    const spanID = randomBytes(8).toString("hex");
    return { client: new Client(this.baseURL, `00-${traceID}-${spanID}-01`), traceID };
  }

  /** Asks team's policy what to do with the alert in req. */
  async route(team: string, req: RouteRequest): Promise<Decoded<RouteResponse>> {
    return decodeJSON<RouteResponse>(await this.postJSON(routePath(team), req));
  }

  /** Delivers wh the way Alertmanager does. */
  async webhook(wh: Webhook): Promise<Decoded<WebhookResponse>> {
    return decodeJSON<WebhookResponse>(await this.postJSON(PATH_ALERTS, wh));
  }

  /** Posts body to the webhook endpoint byte for byte, which is how the specs send the batches under requests/. */
  async webhookRaw(body: string): Promise<Decoded<WebhookResponse>> {
    return decodeJSON<WebhookResponse>(await this.postRaw(PATH_ALERTS, body));
  }

  /** What GET /api/v1/policies reports; expects 200. */
  async listPolicies(): Promise<PoliciesResponse> {
    const a = await this.get(PATH_POLICIES);
    expectStatus(a, 200);
    return decode<PoliciesResponse>(a);
  }

  /** The AlertRouting bundle GET /api/v1/policies reports; expects the listing to have it. */
  async served(): Promise<KindPolicies> {
    const k = routing(await this.listPolicies());
    if (k === undefined) throw new Error(`the policy listing of ${this.baseURL} has no ${KIND_ROUTING}`);
    return k;
  }

  /** The team directory GET /api/v1/teams reports; expects 200. */
  async listTeams(): Promise<TeamsResponse> {
    const a = await this.get(PATH_TEAMS);
    expectStatus(a, 200);
    return decode<TeamsResponse>(a);
  }

  /** Asks the server to reload the team bundle now. */
  reload(): Promise<Answer> {
    return this.send("POST", PATH_RELOAD);
  }

  /** Fetches path, which may carry a query string. */
  get(path: string): Promise<Answer> {
    return this.send("GET", path);
  }

  /** Posts v rendered as JSON. */
  postJSON(path: string, v: unknown): Promise<Answer> {
    return this.postRaw(path, JSON.stringify(v));
  }

  /** Posts body as JSON, byte for byte, which is how the specs send bodies a well-behaved encoder never would. */
  postRaw(path: string, body: string): Promise<Answer> {
    return this.send("POST", path, body, "application/json");
  }

  /**
   * Posts v rendered as JSON and gives up after waitMs, the way a client
   * that stops waiting closes its connection mid-request. It expects the
   * server not to have answered by then, so the request must take longer,
   * such as an alert routed by a policy built with slowPolicy.
   */
  async abandon(path: string, v: unknown, waitMs: number): Promise<void> {
    let answered: number | undefined;
    try {
      const res = await fetch(this.baseURL + path, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(v),
        signal: AbortSignal.timeout(waitMs),
      });
      answered = res.status;
      await res.body?.cancel();
    } catch (err) {
      const name = (err as { name?: string }).name;
      expect(["TimeoutError", "AbortError"]).toContain(name ?? String(err));
      return;
    }
    throw new Error(`POST ${this.baseURL}${path} answered ${answered} within ${waitMs}ms`);
  }

  /** Performs one request and reads the whole body. */
  async send(method: string, path: string, body?: string, contentType?: string): Promise<Answer> {
    const headers: Record<string, string> = {};
    if (contentType !== undefined) headers["content-type"] = contentType;
    if (this.#traceparent !== undefined) headers.traceparent = this.#traceparent;
    const res = await fetch(this.baseURL + path, {
      method,
      headers,
      body,
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    return { status: res.status, headers: res.headers, body: await res.text() };
  }
}

/** Decodes a JSON body, failing with the body in the message when it isn't JSON. */
export function decode<T>(a: Answer): T {
  try {
    return JSON.parse(a.body) as T;
  } catch {
    throw new Error(`decoding the ${a.status} answer's body: ${a.body.slice(0, 500)}`);
  }
}

/**
 * Asserts the status, with the body in the failure, because a bare
 * "expected 200, got 500" says nothing about why.
 */
export function expectStatus(a: Answer, status: number): void {
  expect({ status: a.status, body: a.status === status ? "" : a.body }).toEqual({ status, body: "" });
}

// A JSON body is decoded; anything else is left undefined, and the status
// matcher reports it.
function decodeJSON<T>(a: Answer): Decoded<T> {
  const json = a.headers.get("content-type")?.startsWith("application/json") ?? false;
  return { ...a, out: (json ? decode<T>(a) : undefined) as T };
}
