// JSON responses, and errors in the Go service's envelope.

import { errorResponse } from "../errors";
import type { ErrorEnvelope } from "./wire";

/** A JSON response. */
export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json; charset=utf-8" } });
}

/** An error response in the Go service's envelope. */
export function errorJSON(status: number, err: unknown): Response {
  const rendered = errorResponse(err) ?? { message: "unknown error" };
  return json(status, { error: rendered } satisfies ErrorEnvelope);
}
