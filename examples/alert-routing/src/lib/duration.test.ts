import { describe, expect, test } from "bun:test";

import { formatDuration, goDurationString, msToNs, nsToMs, parseDuration } from "./duration";

const S = 1_000_000_000n;

describe("parseDuration", () => {
  test.each([
    ["12m", 12n * 60n * S],
    ["1h30m", 90n * 60n * S],
    ["90s", 90n * S],
    ["1.5h", 90n * 60n * S],
    ["500ms", 500_000_000n],
    ["250us", 250_000n],
    ["250µs", 250_000n],
    ["250μs", 250_000n],
    ["7ns", 7n],
    ["0", 0n],
    ["0s", 0n],
    ["2d", 48n * 3600n * S],
    ["1d12h", 36n * 3600n * S],
    ["  6h ", 6n * 3600n * S],
    ["-12m", -12n * 60n * S],
    [".5s", 500_000_000n],
    ["1.s", S],
  ])("%s", (text, want) => {
    expect(parseDuration(text)).toBe(want);
  });

  test.each([
    ["", "the duration is empty"],
    ["   ", "the duration is empty"],
    ["12", `"12" isn't a duration`],
    ["twelve minutes", `"twelve minutes" isn't a duration`],
    ["--5m", `"--5m" isn't a duration`],
    ["-+5m", `"-+5m" isn't a duration`],
    ["+5m", `"+5m" isn't a duration`],
    ["-", `"-" isn't a duration`],
    ["5x", `"5x" isn't a duration`],
    [".s", `".s" isn't a duration`],
    ["1h2d", `"1h2d" isn't a duration`],
    ["300y", `"300y" isn't a duration`],
    ["110000d", `"110000d" is longer than the longest duration, about 292 years`],
    ["2562048h", `"2562048h" is longer than the longest duration, about 292 years`],
    ["106751d24h", `"106751d24h" is longer than the longest duration, about 292 years`],
  ])("refuses %j", (text, message) => {
    expect(() => parseDuration(text)).toThrow(message);
  });

  test("every refusal says how to write a duration", () => {
    try {
      parseDuration("soon");
      throw new Error("parsed");
    } catch (err) {
      expect((err as { advice: string[] }).advice[0]).toContain(`write durations like "6h"`);
    }
  });
});

describe("formatDuration", () => {
  test.each([
    [0n, "0s"],
    [15n * 60n * S, "15m"],
    [90n * 60n * S, "1h30m"],
    [48n * 3600n * S + 5n * S, "2d5s"],
    [1_500_000_000n, "1s500ms"],
    [1_000_000n + 7n, "1ms7ns"],
    [-12n * 60n * S, "-12m"],
  ])("%p is %s", (ns, want) => {
    expect(formatDuration(ns)).toBe(want);
  });

  test("round-trips through parseDuration", () => {
    for (const text of ["1d2h3m4s5ms", "12m", "0s", "-3h"]) expect(formatDuration(parseDuration(text))).toBe(text);
  });
});

describe("goDurationString", () => {
  test.each([
    [0n, "0s"],
    [7n, "7ns"],
    [53_208n, "53.208µs"],
    [1_234_567n, "1.234567ms"],
    [2_500_000_000n, "2.5s"],
    [90n * S + 500_000_000n, "1m30.5s"],
    [3600n * S, "1h0m0s"],
    [-1_500_000n, "-1.5ms"],
  ])("%p is %s", (ns, want) => {
    expect(goDurationString(ns)).toBe(want);
  });
});

test("ms and ns convert both ways", () => {
  expect(nsToMs(1_999_999n)).toBe(1);
  expect(msToNs(1500)).toBe(1_500_000_000n);
});
