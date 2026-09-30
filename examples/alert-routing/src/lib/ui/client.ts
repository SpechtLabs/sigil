// The console's calls to the API. Every error body the API sends is an
// ErrorEnvelope or a RouteResponse with an error, both carrying an
// ErrorResponse, so an ApiError can always say what to do.

import type { ErrorResponse } from "./api-types";

export class ApiError extends Error {
  readonly status: number;
  readonly response: ErrorResponse;
  /** The whole body, for callers that read a decision out of an error response. */
  readonly body: unknown;

  constructor(status: number, response: ErrorResponse, body: unknown) {
    super(response.message);
    this.name = "ApiError";
    this.status = status;
    this.response = response;
    this.body = body;
  }
}

/** Reads the ErrorResponse out of an error body, or makes one from the status. */
export function errorOf(status: number, body: unknown): ErrorResponse {
  const err = (body as { error?: unknown } | null)?.error;
  if (typeof err === "object" && err !== null && typeof (err as ErrorResponse).message === "string") {
    return err as ErrorResponse;
  }
  if (typeof err === "string") return { message: err };
  return {
    message: `the server answered ${status}`,
    advice: ["check that alertrouter is running and its policies loaded (GET /readyz)"],
  };
}

/** The response, JSON-decoded, with its status; a body that isn't JSON decodes to null. */
export async function send(path: string, init?: RequestInit): Promise<{ status: number; body: unknown }> {
  const res = await fetch(path, { ...init, headers: { accept: "application/json", ...init?.headers } });
  const text = await res.text();
  let body: unknown = null;
  try {
    body = text === "" ? null : JSON.parse(text);
  } catch {
    body = null;
  }
  return { status: res.status, body };
}

/** GETs or POSTs path and returns its JSON, or throws an ApiError for a status other than 2xx. */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let result: { status: number; body: unknown };
  try {
    result = await send(path, init);
  } catch (err) {
    throw new ApiError(
      0,
      {
        message: `can't reach alertrouter: ${err instanceof Error ? err.message : String(err)}`,
        advice: ["check that the service is running and reload the page"],
      },
      null,
    );
  }
  if (result.status < 200 || result.status >= 300) {
    throw new ApiError(result.status, errorOf(result.status, result.body), result.body);
  }
  return result.body as T;
}

/** POSTs a JSON body. */
export function postJSON(path: string, body?: unknown): Promise<{ status: number; body: unknown }> {
  return send(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/** An ErrorResponse and its causes as lines of text, outermost first. */
export function errorLines(err: ErrorResponse): { message: string; advice: string[] }[] {
  const out: { message: string; advice: string[] }[] = [];
  for (let e: ErrorResponse | undefined = err; e !== undefined; e = e.cause) {
    out.push({ message: e.message, advice: e.advice ?? [] });
  }
  return out;
}
