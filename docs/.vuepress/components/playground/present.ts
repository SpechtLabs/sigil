// How results read on the page: decisions as `page(reason: sustained)`,
// payload values as Sigil literals, and a colour per decision.

import type { EvalEntry, JsonValue } from "@spechtlabs/sigil/worker";

export type Hue = "red" | "yellow" | "green" | "brand" | "purple";

// Decisions whose name says how they read get the colour that says it, like
// the tour's playground; any other decision gets a stable colour of its own.
const NAMED: Record<string, Hue> = {
  page: "red",
  deny: "red",
  block: "red",
  reject: "red",
  drop: "yellow",
  review: "yellow",
  hold: "yellow",
  approve: "green",
  allow: "green",
  grant: "green",
  notify: "brand",
};
const OTHERS: Hue[] = ["brand", "purple", "green", "yellow"];

export function hueOf(decision: string | undefined): Hue {
  if (decision === undefined) return "brand";
  const named = NAMED[decision];
  if (named !== undefined) return named;
  let h = 0;
  for (const c of decision) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return OTHERS[h % OTHERS.length];
}

/** `page(reason: sustained)`. */
export function constructor(e: Pick<EvalEntry, "decision" | "reason">): string {
  return `${e.decision}(reason: ${e.reason})`;
}

/** A payload value as Sigil writes it: strings quoted, lists in brackets. */
export function literal(v: JsonValue): string {
  if (typeof v === "string") return JSON.stringify(v);
  if (Array.isArray(v)) return `[${v.map(literal).join(", ")}]`;
  if (v !== null && typeof v === "object") {
    return `{${Object.entries(v)
      .map(([k, x]) => `${JSON.stringify(k)}: ${literal(x)}`)
      .join(", ")}}`;
  }
  return String(v);
}

/** A byte count in megabytes, one decimal. */
export function megabytes(n: number): string {
  return (n / 1_000_000).toFixed(1);
}
