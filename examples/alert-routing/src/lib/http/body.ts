// Request bodies: read under a size cap, decoded as exactly one JSON value,
// and, for alertrouter's own formats, checked strictly so a typo in a field
// name is reported instead of silently routing a zero value.

import { HumaneError, wrap } from "../errors";

/**
 * The cap on a request body. A kube-prometheus alert with its labels and
 * annotations is 1 to 1.5 KB, so a full webhook of a thousand alerts fits
 * with room to spare; the cap keeps a runaway client from making the server
 * buffer more. The Go service capped at 1 MiB, too little for real alerts.
 */
export const MAX_BODY_BYTES = 4 << 20;

/** A body the handler refuses: 400, or 413 over the size cap. */
export class BodyError extends HumaneError {
  constructor(
    readonly status: 400 | 413,
    message: string,
    advice: string[],
    options?: { cause?: unknown },
  ) {
    super(message, advice, options);
  }
}

/** Reads and decodes the body as one JSON value. Throws a BodyError. */
export async function readJSON(req: Request, advice: string): Promise<unknown> {
  const text = await readText(req, advice);
  if (text.trim() === "") throw new BodyError(400, "the request body is empty", [advice]);
  try {
    return JSON.parse(text);
  } catch (err) {
    if (endsEarly(text)) {
      throw new BodyError(400, "the request body holds more than one JSON value", [
        "send exactly one JSON object per call",
      ]);
    }
    const msg = err instanceof Error ? err.message : String(err);
    throw new BodyError(
      400,
      `the request body isn't a valid request: ${msg}`,
      [advice, "field names are case-sensitive"],
      {
        cause: err,
      },
    );
  }
}

/**
 * Checks that obj has no keys but allowed, the way Go's
 * DisallowUnknownFields does, naming the first unknown one.
 */
export function strictKeys(obj: Record<string, unknown>, allowed: readonly string[], advice: string): void {
  for (const key of Object.keys(obj)) {
    if (!allowed.includes(key)) {
      throw new BodyError(400, `the request body isn't a valid request: json: unknown field ${JSON.stringify(key)}`, [
        advice,
        "field names are case-sensitive",
      ]);
    }
  }
}

/** A body field with the wrong JSON type. */
export function wrongType(field: string, want: string, advice: string): BodyError {
  return new BodyError(400, `the request body isn't a valid request: ${field} isn't ${want}`, [
    advice,
    "field names are case-sensitive",
  ]);
}

/** A duration field that isn't a duration string; its own error carries the advice. */
export function badDuration(err: unknown, advice: string): BodyError {
  const inner = err instanceof HumaneError ? err : wrap(err, String(err));
  return new BodyError(400, "the request body isn't a valid request", [advice], { cause: inner });
}

async function readText(req: Request, advice: string): Promise<string> {
  const declared = Number(req.headers.get("content-length") ?? "NaN");
  if (Number.isFinite(declared) && declared > MAX_BODY_BYTES) throw tooLarge(advice);
  if (req.body === null) return "";
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_BODY_BYTES) {
      await reader.cancel().catch(() => undefined);
      throw tooLarge(advice);
    }
    chunks.push(value);
  }
  return new TextDecoder().decode(Buffer.concat(chunks));
}

function tooLarge(advice: string): BodyError {
  return new BodyError(413, `the request body is larger than ${MAX_BODY_BYTES} bytes`, [
    advice,
    "split the alerts over several webhooks, or lower max_alerts on the Alertmanager receiver",
  ]);
}

// Whether text starts with one complete JSON object or array followed by
// more, which JSON.parse reports as a syntax error but is really a second
// value. One linear scan: it tracks nesting and skips strings.
function endsEarly(text: string): boolean {
  const s = text.trim();
  if (s[0] !== "{" && s[0] !== "[") return false;
  let depth = 0;
  let inString = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (inString) {
      if (ch === "\\") i++;
      else if (ch === '"') inString = false;
      continue;
    }
    if (ch === '"') inString = true;
    else if (ch === "{" || ch === "[") depth++;
    else if (ch === "}" || ch === "]") {
      depth--;
      if (depth === 0) return s.slice(i + 1).trim() !== "";
    }
  }
  return false;
}

/** A plain object, not an array or null. */
export function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}
