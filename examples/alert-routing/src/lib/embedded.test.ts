import { expect, test } from "bun:test";
import { dirname } from "node:path";

import { readEmbedded } from "./embed-source";
import { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "./embedded";

test("src/lib/embedded.ts holds the current policies and team directory; run `bun run embed` when this fails", async () => {
  const root = dirname(dirname(import.meta.dirname));
  const want = await readEmbedded(root);
  expect(PLATFORM_FILES).toEqual(want.platform);
  expect(TEAM_FILES).toEqual(want.teams);
  expect(TEAMS_YAML).toBe(want.teamsYaml);
});

test("platform paths keep their platform/ prefix and team paths are relative to the teams directory", () => {
  expect(PLATFORM_FILES.map((f) => f.path)).toEqual([
    "platform/alerts.sigil",
    "platform/paging.sigil",
    "platform/routing.sigil",
  ]);
  expect(TEAM_FILES.map((f) => f.path)).toEqual(["checkout/alerts.sigil", "payments/alerts.sigil"]);
});
