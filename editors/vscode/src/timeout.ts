// A deadline for a promise that may never settle, such as a language
// client's start when the server never answers initialize.

/** Thrown by withTimeout when the deadline passes first. */
export class TimeoutError extends Error {
  override name = "TimeoutError";
}

/**
 * Settles like promise, or rejects with a TimeoutError carrying message once
 * ms milliseconds pass without it settling. The timer never keeps the
 * process alive, and is cleared when promise settles first.
 */
export function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new TimeoutError(message)), ms);
    timer.unref?.();
  });
  return Promise.race([promise, deadline]).finally(() => clearTimeout(timer));
}
