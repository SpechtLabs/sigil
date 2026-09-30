import { describe, expect, test } from "bun:test";

import { adviceOf, errorResponse, humane, messageOf, wrap } from "./errors";

describe("errorResponse", () => {
  test.each([
    ["undefined", undefined, undefined],
    ["a plain error", new Error("boom"), { message: "boom" }],
    ["a thrown string", "boom", { message: "boom" }],
    ["a humane error", humane("it broke", "fix it"), { message: "it broke", advice: ["fix it"] }],
    ["a humane error without advice", humane("it broke"), { message: "it broke" }],
    [
      "a humane error over a plain cause",
      wrap(new Error("disk full"), "saving failed", "free space"),
      { message: "saving failed", advice: ["free space"], cause: { message: "disk full" } },
    ],
    [
      "a plain cause the message quotes already",
      wrap(new Error("disk full"), "saving failed: disk full", "free space"),
      { message: "saving failed: disk full", advice: ["free space"] },
    ],
    [
      "a humane cause, which is always kept",
      wrap(humane("inner", "inner advice"), "outer: inner", "outer advice"),
      {
        message: "outer: inner",
        advice: ["outer advice"],
        cause: { message: "inner", advice: ["inner advice"] },
      },
    ],
  ])("%s", (_name, err, want) => {
    expect(errorResponse(err)).toEqual(want);
  });
});

test("adviceOf collects the advice down the chain, outermost first", () => {
  expect(adviceOf(wrap(wrap(humane("a", "1"), "b", "2"), "c", "3"))).toEqual(["3", "2", "1"]);
  expect(adviceOf(new Error("plain"))).toEqual([]);
});

test("messageOf reads anything thrown", () => {
  expect(messageOf(new Error("x"))).toBe("x");
  expect(messageOf(42)).toBe("42");
});
