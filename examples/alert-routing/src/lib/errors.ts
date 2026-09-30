// Errors that say what went wrong and what to do about it, the TypeScript
// twin of the Go service's humane errors. Every error that reaches a caller
// or an operator is one of these, and renders as an ErrorResponse.

/** An error with advice: what to do about it. */
export class HumaneError extends Error {
  readonly advice: readonly string[];

  constructor(message: string, advice: readonly string[] = [], options?: { cause?: unknown }) {
    super(message, options);
    this.name = "HumaneError";
    this.advice = advice;
  }
}

/** A new error with advice. */
export function humane(message: string, ...advice: string[]): HumaneError {
  return new HumaneError(message, advice);
}

/** A new error with advice that wraps cause, which the response renders below it. */
export function wrap(cause: unknown, message: string, ...advice: string[]): HumaneError {
  return new HumaneError(message, advice, { cause });
}

/**
 * A humane error as JSON: what went wrong, what to do about it, and what
 * caused it, down the chain. The wire shape of the Go service's
 * ErrorResponse.
 */
export interface ErrorResponse {
  message: string;
  advice?: string[];
  cause?: ErrorResponse;
}

/**
 * Renders err and its causes. A cause that isn't a HumaneError becomes a
 * message without advice. A plain cause whose text the message already
 * quotes is left out rather than printed twice. undefined gives undefined.
 */
export function errorResponse(err: unknown): ErrorResponse | undefined {
  if (err === undefined || err === null) return undefined;
  if (!(err instanceof HumaneError)) return { message: messageOf(err) };

  const resp: ErrorResponse = { message: err.message };
  if (err.advice.length > 0) resp.advice = [...err.advice];
  const cause = err.cause;
  if (cause === undefined || cause === null) return resp;
  if (!(cause instanceof HumaneError) && err.message.includes(messageOf(cause))) return resp;
  const rendered = errorResponse(cause);
  if (rendered !== undefined) resp.cause = rendered;
  return resp;
}

/** The message of anything thrown. */
export function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/** The advice of err and every HumaneError below it, innermost last. */
export function adviceOf(err: unknown): string[] {
  const out: string[] = [];
  for (let e: unknown = err; e instanceof Error; e = e.cause) {
    if (e instanceof HumaneError) out.push(...e.advice);
  }
  return out;
}
