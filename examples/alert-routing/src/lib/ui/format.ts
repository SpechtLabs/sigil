// Small renderings the console's components share.

/** "just now", "42s ago", "5m ago", "3h ago", or the date for anything older than a day. */
export function relativeTime(iso: string, now: Date): string {
  const then = new Date(iso);
  const s = Math.round((now.getTime() - then.getTime()) / 1000);
  if (Number.isNaN(s)) return iso;
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)}h ago`;
  return then.toISOString().slice(0, 10);
}

/** The time of day, 24-hour, in the viewer's time zone. */
export function clockTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

/** One step of a candidate's location: where it is, and whether it is the rule itself or a call on the way. */
export interface LocationStep {
  file: string;
  line: string;
  column: string;
  raw: string;
}

/**
 * Splits a location such as `teams/checkout/alerts.sigil:8:1 →
 * platform/routing.sigil:10:5` into its steps, outermost first; the last
 * step is the rule.
 */
export function locationSteps(location: string): LocationStep[] {
  if (location === "") return [];
  return location.split(" → ").map((raw) => {
    const m = /^(.*):(\d+):(\d+)$/.exec(raw);
    return m === null
      ? { file: raw, line: "", column: "", raw }
      : { file: m[1] ?? raw, line: m[2] ?? "", column: m[3] ?? "", raw };
  });
}

/** A payload value as the trace shows it: strings bare, everything else as JSON. */
export function payloadValue(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v);
}

/** Shortens a fingerprint for a table cell; the full one stays in the title. */
export function shortFingerprint(fp: string): string {
  return fp.length > 12 ? `${fp.slice(0, 12)}…` : fp;
}
