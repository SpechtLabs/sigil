// The API as one fetch function: it finds the route for a request, runs the
// handler, and reports the request through observeRequest the way the Go
// service's gin middleware does: an HTTP server span named after the route
// template, an access log line carrying the trace ids, and the request
// metrics. Next's route files call fetch; so do the integration tests,
// without Next.

import { humane } from "../errors";
import { observeRequest, QUIET_PATHS, UNMATCHED_ROUTE } from "../telemetry/http";
import type { Telemetry } from "../telemetry/types";
import { errNotLoaded, errorJSON, type Handlers, logFailure, NotLoadedError } from "./handlers";

/** The route templates, the request metrics' route label. */
export const ROUTES = {
  alerts: "/api/v1/alerts",
  route: "/api/v1/teams/:team/route",
  teams: "/api/v1/teams",
  policies: "/api/v1/policies",
  reload: "/api/v1/policies/reload",
  policyFiles: "/api/v1/policies/files",
  explain: "/api/v1/policies/:team/explain",
  history: "/api/v1/history",
  events: "/api/v1/events",
  healthz: "/healthz",
  readyz: "/readyz",
  metrics: "/metrics",
} as const;

type Params = Record<string, string>;

interface Route {
  method: string;
  template: string;
  pattern: RegExp;
  handle: (req: Request, params: Params) => Response | Promise<Response>;
}

export interface ApiOptions {
  handlers: Handlers;
  telemetry: Telemetry;
  /** The platform documents /api/v1/policies/files serves. */
  platform: readonly { path: string; source: string }[];
  /** Set once shutdown begins: new API requests are refused with 503. */
  draining: () => boolean;
}

export class Api {
  readonly #opts: ApiOptions;
  readonly #routes: Route[];
  #inFlight = 0;
  #idle: (() => void)[] = [];

  constructor(opts: ApiOptions) {
    this.#opts = opts;
    const h = opts.handlers;
    this.#routes = [
      route("POST", ROUTES.alerts, (req) => h.webhook(req)),
      route("POST", ROUTES.route, (req, p) => h.routeOne(req, p.team ?? "")),
      route("GET", ROUTES.teams, () => h.teams()),
      route("GET", ROUTES.policies, () => h.policies()),
      route("POST", ROUTES.reload, () => h.reload()),
      route("GET", ROUTES.policyFiles, () => h.policyFiles(opts.platform)),
      route("GET", ROUTES.explain, (_req, p) => h.explain(p.team ?? "")),
      route("GET", ROUTES.history, (req) => h.history(req)),
      route("GET", ROUTES.events, (req) => h.events(req)),
      route("GET", ROUTES.healthz, () => h.healthz()),
      route("GET", ROUTES.readyz, () => h.readyz()),
      route("GET", ROUTES.metrics, () => h.metrics()),
    ];
  }

  /** How many requests are being answered right now. */
  get inFlight(): number {
    return this.#inFlight;
  }

  /** Resolves once no request is in flight. */
  idle(): Promise<void> {
    if (this.#inFlight === 0) return Promise.resolve();
    return new Promise((resolve) => this.#idle.push(resolve));
  }

  /** Answers one request. */
  async fetch(req: Request): Promise<Response> {
    const path = new URL(req.url).pathname;
    const matched = this.#match(req.method, path);
    this.#inFlight++;
    try {
      return await observeRequest(this.#opts.telemetry, matched?.route.template ?? UNMATCHED_ROUTE, req, () =>
        this.#answer(req, matched, path),
      );
    } finally {
      this.#inFlight--;
      if (this.#inFlight === 0) for (const resolve of this.#idle.splice(0)) resolve();
    }
  }

  async #answer(req: Request, matched: { route: Route; params: Params } | undefined, path: string): Promise<Response> {
    const { handlers, telemetry, draining } = this.#opts;
    if (matched === undefined) return handlers.notFound(req);
    if (draining() && !QUIET_PATHS.includes(path)) {
      return errorJSON(
        503,
        humane(
          "alertrouter is shutting down",
          "retry the request; another replica, or this one once restarted, answers it",
        ),
      );
    }
    try {
      return await matched.route.handle(req, matched.params);
    } catch (err) {
      const status = err instanceof NotLoadedError ? 503 : 500;
      const cause = err instanceof NotLoadedError ? errNotLoaded() : err;
      logFailure(telemetry, status, path, cause);
      return errorJSON(status, cause);
    }
  }

  #match(method: string, path: string): { route: Route; params: Params } | undefined {
    for (const r of this.#routes) {
      if (r.method !== method) continue;
      const m = r.pattern.exec(path);
      if (m === null) continue;
      const params: Params = {};
      for (const [k, v] of Object.entries(m.groups ?? {})) params[k] = safeDecode(v);
      return { route: r, params };
    }
    return undefined;
  }
}

function route(method: string, template: string, handle: Route["handle"]): Route {
  const pattern = new RegExp(
    `^${template.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/:([a-z]+)/g, "(?<$1>[^/]+)")}$`,
  );
  return { method, template, pattern, handle };
}

function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
