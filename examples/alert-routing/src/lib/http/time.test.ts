import { expect, test } from "bun:test";

import { formatTime } from "./time";

test.each([
  ["2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00Z"],
  ["2026-01-01T00:00:00.120Z", "2026-01-01T00:00:00.12Z"],
  ["2026-01-01T00:00:00.123Z", "2026-01-01T00:00:00.123Z"],
  ["2026-01-01T00:00:00.100Z", "2026-01-01T00:00:00.1Z"],
])("%s renders as Go's RFC 3339 %s", (iso, want) => {
  expect(formatTime(new Date(iso))).toBe(want);
});
