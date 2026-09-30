// Host functions for the worker tests: the worker helper imports this
// module inside the worker, since functions can't cross to it.

/** split, except that a first argument of "hang" never returns. */
export function split(s: string, sep: string): string[] {
  if (s === "hang") for (;;) {
    // a runaway host function
  }
  return s.split(sep);
}
