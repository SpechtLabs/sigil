// Polling for what arrives asynchronously: spans a moment after the answer,
// scrapes Alloy batches, profiles uploaded every 15s.

export interface PollOptions {
  timeoutMs?: number;
  intervalMs?: number;
}

/**
 * Runs check until it stops throwing and returns its result. When the time
 * runs out it throws check's last error, so the failure says what was still
 * wrong rather than only that it timed out.
 */
export async function eventually<T>(check: () => T | Promise<T>, options: PollOptions = {}): Promise<T> {
  const timeoutMs = options.timeoutMs ?? 2_000;
  const intervalMs = options.intervalMs ?? 10;
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      return await check();
    } catch (err) {
      if (Date.now() >= deadline) {
        if (err instanceof Error) err.message = `still failing after ${timeoutMs}ms: ${err.message}`;
        throw err;
      }
    }
    await Bun.sleep(intervalMs);
  }
}
