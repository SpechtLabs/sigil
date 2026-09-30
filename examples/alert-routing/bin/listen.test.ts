import { expect, test } from "bun:test";

import { parseAddr as parseConfigAddr } from "../src/lib/config/config";
// @ts-expect-error: plain JavaScript, run by Node in the container without a build.
import { parseAddr } from "./listen.mjs";

test.each([":8080", "0.0.0.0:80", "localhost:3000", "[::1]:8080", "8080", "host:", ":123456", "::1:8080", ""])(
  "the entry point reads %j exactly like the service's configuration",
  (addr) => {
    expect(parseAddr(addr)).toEqual(parseConfigAddr(addr));
  },
);
