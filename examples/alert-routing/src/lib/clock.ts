// The wall clock, behind an interface so a test can pin how long an alert
// has fired, when a bundle loaded and when a dedup entry expires.

export interface Clock {
  /** Milliseconds since the epoch. */
  now(): number;
}

export const wallClock: Clock = { now: () => Date.now() };
