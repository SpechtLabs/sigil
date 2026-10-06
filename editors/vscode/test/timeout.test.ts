import { describe, expect, test } from "bun:test";
import { TimeoutError, withTimeout } from "../src/timeout";

describe("withTimeout", () => {
  test("a promise that settles first wins", async () => {
    expect(await withTimeout(Promise.resolve(42), 1_000, "late")).toBe(42);
  });

  test("a rejection that comes first is passed on", async () => {
    await expect(withTimeout(Promise.reject(new Error("boom")), 1_000, "late")).rejects.toThrow("boom");
  });

  test("a promise that never settles rejects with a TimeoutError", async () => {
    const never = new Promise<never>(() => undefined);
    const err = await withTimeout(never, 10, "sigil lsp didn't answer").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(TimeoutError);
    expect((err as Error).message).toBe("sigil lsp didn't answer");
  });
});
