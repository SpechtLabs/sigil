// Decisions and their reasons as typed handles, the twin of Go's
// policy.NewDecision: a kind declares them, and a host reads a result
// through them, getting a typed payload back instead of a bag of JSON.

import { SigilError } from "./errors.js";
import type { AnyType, Defaulted, Out } from "./schema.js";
import type { EvalEntry, EvalResult } from "./types.js";

/** A decision's payload fields: a type, or a type with its default. */
export type PayloadSpec = Record<string, AnyType | Defaulted<AnyType>>;

type FieldType<F> = F extends Defaulted<infer T> ? T : F;

/** A decision's payload as the module hands it back. */
export type PayloadOf<P extends PayloadSpec> = { -readonly [K in keyof P]: Out<FieldType<P[K]>> } & {};

/** One entry of an outcome, with its payload typed. */
export interface Matched<R extends string, P> {
  payload: P;
  reason: R;
  /** The policy whose rule produced it; undefined for the default. */
  policy: string | undefined;
  /** The rule's position, `file:line:column`; undefined for the default. */
  position: string | undefined;
}

/**
 * How a kind's results read, which the kind attaches to each result so a
 * decision handle can tell a single outcome from a collection.
 */
export interface ResultShape {
  collect: boolean;
  /** A collecting kind with precedence: its outcome is the top rank. */
  ranked: boolean;
}

const SHAPE = Symbol.for("@spechtlabs/sigil.resultShape");

/** @internal Marks a result with the shape of the kind that produced it. */
export function markResult<T extends EvalResult>(res: T, shape: ResultShape | undefined): T {
  if (shape !== undefined) Object.defineProperty(res, SHAPE, { value: shape, enumerable: false });
  return res;
}

/**
 * A reason of one decision, `approve.release_manager` in a kind file: what
 * a kind ranks, picks as its default, or marks exclusive, and what a host
 * checks a result for.
 */
export class Outcome<D extends string = string, R extends string = string> {
  constructor(
    readonly decision: D,
    readonly reason: R,
  ) {}

  /**
   * Whether the result's outcome is exactly one entry, of this decision
   * with this reason. A failed evaluation's result holds the kind's
   * fallback, so check `error` first. Throws on an unranked collecting
   * kind's result, whose outcome has no single entry to compare.
   */
  is(res: EvalResult | undefined): boolean {
    if (res === undefined) return false;
    single(res, "is");
    const e = res.outcome[0];
    return res.outcome.length === 1 && e?.decision === this.decision && e.reason === this.reason;
  }

  toString(): string {
    return `${this.decision}.${this.reason}`;
  }
}

/**
 * A decision: its name, its reasons and its payload fields. Declare one
 * with {@link decision}.
 */
export class Decision<N extends string = string, R extends string = string, P extends PayloadSpec = PayloadSpec> {
  constructor(
    readonly name: N,
    readonly reasons: readonly R[],
    readonly payload: P,
  ) {}

  /**
   * One of the decision's reasons, as a handle. A reason the decision
   * doesn't declare is a type error, and throws at run time.
   */
  reason<const X extends R>(name: X): Outcome<N, X> {
    if (!this.reasons.includes(name)) {
      throw new SigilError(`decision ${this.name} has no reason ${JSON.stringify(name)}`, {
        help: didYouMean(name, this.reasons) + `${this.name} declares: ${this.reasons.join(", ")}`,
      });
    }
    return new Outcome(this.name, name);
  }

  /**
   * The payload, when the result's outcome is exactly one entry of this
   * decision, and undefined otherwise. A failed evaluation's result holds
   * the kind's fallback, so check `error` first. Throws on an unranked
   * collecting kind's result; use {@link matchAll} there.
   */
  match(res: EvalResult | undefined): PayloadOf<P> | undefined {
    if (res === undefined) return undefined;
    single(res, "match");
    const e = res.outcome[0];
    if (res.outcome.length !== 1 || e?.decision !== this.name) return undefined;
    return (e.payload ?? {}) as PayloadOf<P>;
  }

  /**
   * Every entry of this decision in the result's outcome, in outcome
   * order: how a collecting kind, which can grant a decision more than
   * once, is read.
   */
  matchAll(res: EvalResult | undefined): Matched<R, PayloadOf<P>>[] {
    if (res === undefined) return [];
    return res.outcome
      .filter((e: EvalEntry) => e.decision === this.name)
      .map((e) => ({
        payload: (e.payload ?? {}) as PayloadOf<P>,
        reason: e.reason as R,
        policy: e.policy,
        position: e.position,
      }));
  }

  toString(): string {
    return this.name;
  }
}

/**
 * Declares a decision: its name, its reasons, and its payload fields in
 * declaration order.
 *
 *     const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default("#alerts") });
 */
export function decision<const N extends string, const R extends readonly string[], const P extends PayloadSpec = {}>(
  name: N,
  reasons: R,
  payload?: P,
): Decision<N, R[number], P> {
  return new Decision<N, R[number], P>(name, reasons, (payload ?? {}) as P);
}

// Throws for a result whose outcome isn't one decision: an unranked
// collecting kind's. Without the kind's mark, `collect` alone says so.
function single(res: EvalResult, method: string): void {
  const shape = (res as unknown as Record<symbol, ResultShape | undefined>)[SHAPE];
  const unranked = shape === undefined ? res.collect === true : shape.collect && !shape.ranked;
  if (unranked) {
    throw new SigilError(`${method} on a collecting kind's result, which has no single outcome; read it with matchAll`, {
      help: "a collecting kind applies every decision that fired; with precedence, the top rank reads with match",
    });
  }
}

/** A did-you-mean for a misspelled name, or nothing. */
export function didYouMean(name: string, names: readonly string[]): string {
  let best: string | undefined;
  let bestDistance = Math.max(2, Math.floor(name.length / 3)) + 1;
  for (const candidate of names) {
    const d = distance(name, candidate);
    if (d < bestDistance) {
      best = candidate;
      bestDistance = d;
    }
  }
  return best === undefined ? "" : `did you mean ${JSON.stringify(best)}? `;
}

function distance(a: string, b: string): number {
  const row = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let prev = row[0] ?? 0;
    row[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const cur = row[j] ?? 0;
      row[j] = Math.min(cur + 1, (row[j - 1] ?? 0) + 1, prev + (a[i - 1] === b[j - 1] ? 0 : 1));
      prev = cur;
    }
  }
  return row[b.length] ?? 0;
}
