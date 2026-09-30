// Durations as Sigil writes them: integer components with the units d, h,
// m, s and ms, largest first, each at most once, like "1h30m" or "2d".
// That's the form inputs, payloads and host function arguments carry
// across the boundary, and it isn't Go's time.ParseDuration syntax: there
// is a `d`, and no fractions or `us`.

import { SigilError } from "./errors.js";

/**
 * A duration in Sigil's syntax, like "1h30m". A plain string, so JSON
 * inputs type-check as they are; {@link duration} validates one.
 */
export type Duration = string;

const UNITS = [
  ["d", 86_400_000],
  ["h", 3_600_000],
  ["m", 60_000],
  ["s", 1_000],
  ["ms", 1],
] as const;

// The largest duration Go's time.Duration holds, in whole milliseconds.
const MAX_MS = 9_223_372_036_854;

/**
 * Validates a duration and returns it in canonical form: `duration("90m")`
 * is "1h30m". Throws a {@link SigilError} for text that isn't a duration.
 */
export function duration(text: string): Duration {
  return ms(toMs(text));
}

/**
 * The duration of a whole, non-negative number of milliseconds:
 * `ms(720_000)` is "12m", `ms(0)` is "0s".
 */
export function ms(milliseconds: number): Duration {
  if (!Number.isInteger(milliseconds) || milliseconds < 0 || milliseconds > MAX_MS) {
    throw new SigilError(`${milliseconds} ms isn't a duration Sigil can write`, {
      help: "pass a whole number of milliseconds from 0 to about 292 years; round it first",
    });
  }
  return formatDuration(milliseconds);
}

/** The number of milliseconds a duration stands for: `toMs("12m")` is 720000. */
export function toMs(d: Duration): number {
  if (d === "") return 0;
  let total = 0;
  let last = -1;
  let rest = d;
  while (rest !== "") {
    const m = /^(\d+)(ms|d|h|m|s)/.exec(rest);
    if (m === null) throw invalid(d, "units are d, h, m, s and ms, largest first, each at most once: \"1h30m\"");
    const rank = UNITS.findIndex(([u]) => u === m[2]);
    if (rank === last) throw invalid(d, "each unit may appear once in a duration; add the components together");
    if (rank < last) throw invalid(d, "write the largest unit first, like \"1h30m\"");
    last = rank;
    total += Number(m[1]) * (UNITS[rank]?.[1] ?? 0);
    rest = rest.slice(m[0].length);
  }
  if (total > MAX_MS) throw invalid(d, "a duration is at most about 292 years");
  return total;
}

/**
 * Renders milliseconds as Sigil's constant formatter does (the largest
 * units first, each at most once, `0s` for zero), so a payload default
 * prints byte for byte as Go's Kind.Schema writes it.
 */
export function formatDuration(milliseconds: number): string {
  if (milliseconds === 0) return "0s";
  let out = milliseconds < 0 ? "-" : "";
  let n = Math.abs(milliseconds);
  for (const [unit, size] of UNITS) {
    const q = Math.floor(n / size);
    if (q > 0) {
      out += `${q}${unit}`;
      n -= q * size;
    }
  }
  return out;
}

function invalid(d: string, help: string): SigilError {
  return new SigilError(`invalid duration ${JSON.stringify(d)}`, { help });
}
