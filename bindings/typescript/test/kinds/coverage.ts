// Kinds that cover every corner of schema(), mirrored one for one in
// test/testdata/gokinds/main.go, which prints what Go's Kind.Schema writes
// for them.

import { decision, defineKind, enumType, fn, struct, t } from "../../src/index.js";

const Level = enumType("Level", ["low", "high"]);
const Region = enumType("Region", ["eu", "us"]);
const Unused = enumType("Unused", ["a", "b"]);
const Mood = enumType("Mood", ["calm", "tense"]);
const Group = struct("Group", { name: t.string, admin: t.bool });
const User = struct("User", { name: t.string, groups: t.list(Group), level: Level });
const Empty = struct("Empty", {});
const Site = struct("Site", { name: t.string });
const Request = struct("Request", {
  user: User,
  manager: t.optional(User),
  tags: t.list(t.string),
  meta: t.map(t.string, t.int),
  deadline: t.timestamp,
  score: t.float,
  empty: Empty,
});

export const Deny = decision("deny", ["bad", "worse", "fallback"]);
export const Allow = decision("allow", ["fine", "great"], {
  ttl: t.duration.default("90m"),
  days: t.duration.default("2d3h"),
  note: t.string.default('tab\t "quoted" \\ ✓ é 😀 \x01   \x7f'),
  limit: t.int.default(-3),
  ratio: t.float.default(0.5),
  big: t.float.default(1e21),
  tiny: t.float.default(1e-7),
  whole: t.float.default(2),
  on: t.bool.default(true),
  tags: t.list(t.string).default(["b", "a"]),
  level: Level.default("high"),
  limits: t.map(t.string, t.int).default({ b: 2, a: 1, ä: 3, Z: 4 }),
  maybe2: t.optional(t.int).default(4),
  empty: t.list(t.string).default([]),
});
export const Hold = decision("hold", ["wait"], { until: t.string, mood: Mood });

export const Everything = defineKind("Everything", {
  version: 3,
  accepts: 2,
  enums: [Unused, Mood, Region, Level],
  inputs: { request: Request, now: t.timestamp, limit: t.optional(t.int), weights: t.map(t.string, t.float) },
  functions: {
    lookup: fn([t.string, t.map(Region, t.int)], User),
    count: fn([t.list(t.string)], t.int, (items) => items.length),
    locate: fn([Site], Region, () => "eu" as const),
  },
  decisions: [Deny, Allow, Hold],
  reasonPrecedence: [[Deny.reason("worse"), Deny.reason("bad"), Deny.reason("fallback")]],
  exclusive: [[Allow.reason("great"), Hold]],
  default: Deny.reason("fallback"),
  conflict: Deny.reason("worse"),
});

export const A = decision("a", ["r1", "r2"]);
export const B = decision("b", ["x", "y"], { weight: t.float.default(1) });
export const C = decision("c", ["z"]);
export const Collecting = defineKind("Collecting", {
  version: 1,
  inputs: { items: t.list(t.string) },
  collect: [A, B, C],
  precedence: [C, A, B],
  reasonPrecedence: [[A.reason("r2"), A.reason("r1")]],
  exclusive: [[A, B.reason("y")], [B, C]],
  default: C.reason("z"),
});

export const Ok = decision("ok", ["yes"]);
export const Minimal = defineKind("Minimal", {
  version: 1,
  inputs: { who: t.string },
  functions: { upper: fn([t.string], t.string, (s) => s.toUpperCase()) },
  decisions: [Ok],
  default: Ok.reason("yes"),
});
