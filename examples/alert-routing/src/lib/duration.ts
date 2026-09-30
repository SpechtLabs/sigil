// Durations on the wire and in the configuration: strings in Go's syntax
// ("1h30m", "90s", "1.5h", "500ms"), extended with Sigil's `d` unit as a
// leading component ("2d", "1d12h"), never numbers. A bare number would be
// ambiguous between milliseconds and seconds, which is exactly the mistake
// Sigil refuses to compile, so the API refuses it too. The port of the Go
// service's internal/server/duration.go; values are exact nanoseconds held
// in a bigint, so nothing is rounded on the way through.

import { type HumaneError, humane } from "./errors";

/** Nanoseconds per unit. */
const UNIT_NS: Record<string, bigint> = {
  ns: 1n,
  us: 1_000n,
  µs: 1_000n, // U+00B5 micro sign
  μs: 1_000n, // U+03BC Greek mu
  ms: 1_000_000n,
  s: 1_000_000_000n,
  m: 60_000_000_000n,
  h: 3_600_000_000_000n,
};

const DAY_NS = 86_400_000_000_000n;

/** The largest duration Go's time.Duration holds, about 292 years. */
const MAX_NS = 9_223_372_036_854_775_807n;

const ADVICE = `write durations like "6h", "1h30m" or "2d": an integer and a unit (d, h, m, s, ms), largest first`;

/** The units a duration renders with, largest first, the way Sigil writes a literal. */
const RENDER_UNITS: readonly (readonly [string, bigint])[] = [
  ["d", DAY_NS],
  ["h", UNIT_NS.h as bigint],
  ["m", UNIT_NS.m as bigint],
  ["s", UNIT_NS.s as bigint],
  ["ms", UNIT_NS.ms as bigint],
];

/**
 * Reads a duration in Go's syntax, extended with a leading `d` component,
 * into nanoseconds. A leading minus makes it negative. Surrounding
 * whitespace is ignored; empty text, text that isn't a duration and a
 * duration past about 292 years are errors with advice.
 */
export function parseDuration(text: string): bigint {
  const s = text.trim();
  if (s === "") throw humane("the duration is empty", ADVICE);

  let sign = 1n;
  let rest = s;
  if (rest.startsWith("-")) {
    sign = -1n;
    rest = rest.slice(1);
  }
  // Go's parser takes a sign of its own; one sign is enough.
  if (rest === "" || rest.startsWith("-") || rest.startsWith("+")) throw notDuration(text);

  let days = 0n;
  const dayMatch = /^(\d+)d/.exec(rest);
  if (dayMatch !== null) {
    days = BigInt(dayMatch[1] as string) * DAY_NS;
    if (days > MAX_NS) throw tooLong(text);
    rest = rest.slice(dayMatch[0].length);
  }

  let clock = 0n;
  if (rest !== "") {
    const parsed = parseGo(rest);
    if (parsed === undefined) throw notDuration(text);
    if (parsed === "overflow" || parsed > MAX_NS - days) throw tooLong(text);
    clock = parsed;
  }
  return sign * (days + clock);
}

/**
 * Renders nanoseconds the way Sigil writes a duration literal: the largest
 * units first, each at most once, "0s" for zero, so a policy's 15m comes
 * back as "15m" rather than Go's "15m0s". A sub-millisecond remainder, which
 * Sigil can't write, is appended in nanoseconds so the value stays exact.
 */
export function formatDuration(ns: bigint): string {
  if (ns === 0n) return "0s";
  let out = "";
  let d = ns;
  if (d < 0n) {
    out = "-";
    d = -d;
  }
  for (const [name, size] of RENDER_UNITS) {
    const n = d / size;
    if (n > 0n) {
      out += `${n}${name}`;
      d -= n * size;
    }
  }
  if (d > 0n) out += `${d}ns`;
  return out;
}

/** Whole milliseconds of a nanosecond duration, rounded down. */
export function nsToMs(ns: bigint): number {
  return Number(ns / 1_000_000n);
}

/** Nanoseconds of a millisecond count. */
export function msToNs(ms: number): bigint {
  return BigInt(Math.trunc(ms)) * 1_000_000n;
}

/**
 * Go's time.ParseDuration without the sign: components of digits, an
 * optional fraction and a unit, or a lone "0". undefined when the text isn't
 * a duration, "overflow" when it's too long.
 */
function parseGo(text: string): bigint | "overflow" | undefined {
  if (text === "0") return 0n;
  const component = /^(\d*)(?:\.(\d*))?(ns|us|µs|μs|ms|s|m|h)/;
  let rest = text;
  let total = 0n;
  while (rest !== "") {
    const m = component.exec(rest);
    if (m === null) return undefined;
    const whole = m[1] ?? "";
    const frac = m[2];
    if (whole === "" && (frac === undefined || frac === "")) return undefined;
    const unit = UNIT_NS[m[3] as string] as bigint;
    let value = BigInt(whole === "" ? "0" : whole) * unit;
    if (frac !== undefined && frac !== "") {
      // Go truncates the fraction to what the unit resolves to in nanoseconds.
      value += (BigInt(frac) * unit) / 10n ** BigInt(frac.length);
    }
    total += value;
    if (total > MAX_NS) return "overflow";
    rest = rest.slice(m[0].length);
  }
  return total;
}

function notDuration(text: string): HumaneError {
  return humane(`${JSON.stringify(text)} isn't a duration`, ADVICE);
}

function tooLong(text: string): HumaneError {
  return humane(
    `${JSON.stringify(text)} is longer than the longest duration, about 292 years`,
    'use a realistic duration, such as "6h" or "2d"',
  );
}

/**
 * Renders nanoseconds the way Go's time.Duration.String does, "53.208µs" or
 * "1m30.5s", for log fields the Go service wrote with zap's duration
 * encoder.
 */
export function goDurationString(ns: bigint): string {
  if (ns === 0n) return "0s";
  const neg = ns < 0n;
  let u = neg ? -ns : ns;
  let out: string;
  if (u < 1_000_000_000n) {
    const [unit, size] = u < 1_000n ? ["ns", 1n] : u < 1_000_000n ? ["µs", 1_000n] : ["ms", 1_000_000n];
    out = fraction(u, size) + unit;
  } else {
    const h = u / 3_600_000_000_000n;
    u -= h * 3_600_000_000_000n;
    const m = u / 60_000_000_000n;
    u -= m * 60_000_000_000n;
    out = `${h > 0n ? `${h}h` : ""}${h > 0n || m > 0n ? `${m}m` : ""}${fraction(u, 1_000_000_000n)}s`;
  }
  return neg ? `-${out}` : out;
}

// value/size with the remainder as a decimal fraction, trailing zeros trimmed.
function fraction(value: bigint, size: bigint): string {
  const whole = value / size;
  const rest = value % size;
  if (rest === 0n) return `${whole}`;
  const digits = size.toString().length - 1;
  return `${whole}.${rest.toString().padStart(digits, "0").replace(/0+$/, "")}`;
}
