import { describe, expect, test } from "bun:test";

import { BodyError, MAX_BODY_BYTES, readJSON, strictKeys } from "./body";

const req = (body: string, headers: Record<string, string> = {}) =>
  new Request("http://x/", { method: "POST", body, headers });

describe("readJSON", () => {
  test("decodes one JSON value", async () => {
    expect(await readJSON(req(' {"a": [1, "}"]} \n'), "advice")).toEqual({ a: [1, "}"] });
  });

  test.each([
    ["an empty body", "", 400, "the request body is empty"],
    ["whitespace", "  \n", 400, "the request body is empty"],
    ["two objects", '{"a":1} {"b":2}', 400, "the request body holds more than one JSON value"],
    ["an object and garbage", '{"a":"}"}x', 400, "the request body holds more than one JSON value"],
    ["broken JSON", '{"a":', 400, "the request body isn't a valid request: "],
  ])("refuses %s", async (_name, body, status, message) => {
    const err = (await readJSON(req(body), "advice").catch((e: unknown) => e)) as BodyError;
    expect(err).toBeInstanceOf(BodyError);
    expect(err.status).toBe(status as 400);
    expect(err.message.startsWith(message)).toBe(true);
  });

  test("refuses a body over the cap, by its content-length or by what arrives", async () => {
    const big = "x".repeat(MAX_BODY_BYTES + 1);
    for (const r of [req(big), req("{}", { "content-length": String(MAX_BODY_BYTES + 1) })]) {
      const err = (await readJSON(r, "advice").catch((e: unknown) => e)) as BodyError;
      expect(err.status).toBe(413);
      expect(err.advice).toContain(
        "split the alerts over several webhooks, or lower max_alerts on the Alertmanager receiver",
      );
    }
  });
});

test("strictKeys names the first unknown field, as Go's DisallowUnknownFields does", () => {
  expect(() => strictKeys({ alert: {}, extra: 1 }, ["alert"], "advice")).toThrow('json: unknown field "extra"');
  expect(() => strictKeys({ alert: {} }, ["alert"], "advice")).not.toThrow();
});
