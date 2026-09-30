import { describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { TEAMS_YAML } from "../embedded";
import { DEFAULT_SOURCE, loadTeamsFile, TeamDirectory } from "./directory";

describe("TeamDirectory", () => {
  test("the bundled directory lists checkout and payments", () => {
    const dir = TeamDirectory.parse(TEAMS_YAML, DEFAULT_SOURCE);
    expect(dir.source).toBe("embedded teams.yaml");
    expect(dir.names()).toEqual(["checkout", "payments"]);
    expect(dir.lookup("payments")).toEqual({
      name: "payments",
      oncall: "payments-primary",
      channel: "#payments-alerts",
    });
    expect(dir.lookup("search")).toBeUndefined();
    expect(dir.teams().map((t) => t.name)).toEqual(["checkout", "payments"]);
  });

  test("sorts teams by name whatever the file's order", () => {
    const dir = TeamDirectory.parse(
      'teams:\n  - {name: zeta, oncall: z, channel: "#z"}\n  - {name: alpha, oncall: a, channel: "#a"}\n',
      "t.yaml",
    );
    expect(dir.names()).toEqual(["alpha", "zeta"]);
  });

  test("knows its destinations", () => {
    const dir = TeamDirectory.parse(TEAMS_YAML, DEFAULT_SOURCE);
    expect(dir.isKnownDestination("checkout-primary")).toBe(true);
    expect(dir.isKnownDestination("#payments-alerts")).toBe(true);
    expect(dir.isKnownDestination("#checkout-payments")).toBe(false);
  });

  test("names() hands out a copy", () => {
    const dir = TeamDirectory.parse(TEAMS_YAML, DEFAULT_SOURCE);
    dir.names().push("x");
    expect(dir.names()).toHaveLength(2);
  });

  test.each([
    ["an empty file", "", "the team directory t.yaml is empty"],
    ["a comment only", "# nothing\n", "the team directory t.yaml is empty"],
    ["invalid YAML", "teams: [\n", "the team directory t.yaml isn't valid YAML"],
    ["a list at the top", "- a\n", "t.yaml:1:1: the directory must be a map with the keys teams"],
    ["an unknown top-level key", "teem: []\n", `t.yaml:1:1: unknown key "teem" in the directory`],
    ["no teams", "teams: []\n", "the team directory t.yaml lists no teams"],
    ["teams not a list", "teams: {}\n", "the team directory t.yaml lists no teams"],
    [
      "a team that isn't a map",
      "teams:\n  - checkout\n",
      "t.yaml:2:5: a team must be a map with the keys name, oncall, channel",
    ],
    [
      "a misspelled key",
      'teams:\n  - {name: a, oncal: x, channel: "#a"}\n',
      `t.yaml:2:15: unknown key "oncal" in a team`,
    ],
    [
      "a key set twice",
      'teams:\n  - {name: a, name: b, oncall: x, channel: "#a"}\n',
      `t.yaml:2:15: the key "name" is set twice in a team`,
    ],
    [
      "a nested value",
      'teams:\n  - {name: [a], oncall: x, channel: "#a"}\n',
      "t.yaml:2:5: the team isn't a map of strings",
    ],
    ["no name", 'teams:\n  - {oncall: x, channel: "#a"}\n', "t.yaml:2:5: a team has no name"],
    [
      "a name that isn't an identifier",
      'teams:\n  - {name: Check-Out, oncall: x, channel: "#a"}\n',
      `t.yaml:2:5: the team name "Check-Out" isn't a lower-case identifier`,
    ],
    ["no oncall", 'teams:\n  - {name: a, channel: "#a"}\n', "t.yaml:2:5: team a has no oncall"],
    [
      "a channel without #",
      "teams:\n  - {name: a, oncall: x, channel: alerts}\n",
      `t.yaml:2:5: team a has the channel "alerts", which doesn't start with #`,
    ],
    [
      "an unquoted channel, which YAML reads as a comment",
      "teams:\n  - name: a\n    oncall: x\n    channel: #a\n",
      `t.yaml:2:5: team a has the channel "", which doesn't start with #`,
    ],
    [
      "a team listed twice",
      'teams:\n  - {name: a, oncall: x, channel: "#a"}\n  - {name: a, oncall: y, channel: "#b"}\n',
      "t.yaml:3:5: team a is listed twice",
    ],
  ])("refuses %s", (_name, text, message) => {
    expect(() => TeamDirectory.parse(text, "t.yaml")).toThrow(message);
  });

  test("a misspelled key's advice names the key it's probably meant to be", () => {
    try {
      TeamDirectory.parse('teams:\n  - {name: a, Channnel: "#a", oncall: x}\n', "t.yaml");
      throw new Error("parsed");
    } catch (err) {
      expect((err as { advice: string[] }).advice).toEqual([
        `did you mean "channel"? the keys are name, oncall, channel`,
      ]);
    }
  });
});

describe("loadTeamsFile", () => {
  test("loads a file and names it as the source", async () => {
    const dir = await mkdtemp(join(tmpdir(), "alertrouter-teams-"));
    try {
      const path = join(dir, "teams.yaml");
      await writeFile(path, TEAMS_YAML);
      const loaded = await loadTeamsFile(path, (p) => Bun.file(p).text());
      expect(loaded.source).toBe(path);
      expect(loaded.names()).toEqual(["checkout", "payments"]);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  test("a file that can't be read says what to set", async () => {
    await expect(loadTeamsFile("/nonexistent/teams.yaml", () => Promise.reject(new Error("ENOENT")))).rejects.toThrow(
      "cannot open the team directory /nonexistent/teams.yaml",
    );
  });
});
