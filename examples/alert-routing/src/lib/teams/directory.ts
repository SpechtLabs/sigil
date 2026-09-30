// The team directory: every team alertrouter routes for, with the on-call
// target and the channel its policy pages and posts to. An alert's team
// label picks the entry, and the policy <team>.alerts decides with it. It is
// loaded once at startup, from teams.yaml or the copy bundled with the app,
// and checked strictly: a typo in a key or a channel without its # would
// otherwise route alerts to nowhere.

import { isMap, isScalar, isSeq, LineCounter, type Node, parseDocument } from "yaml";

import { HumaneError, humane, wrap } from "../errors";
import type { Team } from "../routing/kind";

/** The source name of the directory bundled with the app. */
export const DEFAULT_SOURCE = "embedded teams.yaml";

const FORMAT_ADVICE = `write the directory as "teams:" and a list of {name, oncall, channel} entries, like src/lib/teams/teams.yaml`;

const FILE_KEYS = ["teams"] as const;
const TEAM_KEYS = ["name", "oncall", "channel"] as const;

/** A team name becomes the policy <team>.alerts, so it must be a Sigil identifier. */
const NAME_PATTERN = /^[a-z_][a-z0-9_]*$/;

/** The teams, by name, and where they were read from. Immutable once loaded. */
export class TeamDirectory {
  readonly #teams: ReadonlyMap<string, Team>;
  readonly #names: readonly string[];

  private constructor(
    teams: Map<string, Team>,
    readonly source: string,
  ) {
    this.#teams = teams;
    this.#names = [...teams.keys()].sort();
  }

  /**
   * Parses a teams.yaml. source names it in errors. Throws a HumaneError
   * saying where the file is wrong and how to fix it.
   */
  static parse(text: string, source: string): TeamDirectory {
    const lines = new LineCounter();
    const doc = parseDocument(text, { lineCounter: lines, uniqueKeys: false, prettyErrors: false });
    const at = (node: Node | null | undefined): string => {
      const offset = node?.range?.[0];
      if (offset === undefined) return source;
      const { line, col } = lines.linePos(offset);
      return `${source}:${line}:${col}`;
    };

    if (doc.errors.length > 0) {
      throw wrap(doc.errors[0], `the team directory ${source} isn't valid YAML`, FORMAT_ADVICE);
    }
    const root = doc.contents;
    if (root === null || (isScalar(root) && (root.value === null || root.value === ""))) {
      throw humane(`the team directory ${source} is empty`, FORMAT_ADVICE);
    }
    checkKeys(root, FILE_KEYS, "the directory", at);
    const list = isMap(root) ? root.get("teams", true) : undefined;
    if (!isSeq(list) || list.items.length === 0) {
      throw humane(`the team directory ${source} lists no teams`, FORMAT_ADVICE);
    }

    const teams = new Map<string, Team>();
    for (const item of list.items) {
      const node = item as Node;
      checkKeys(node, TEAM_KEYS, "a team", at);
      const entry: Record<string, string> = { name: "", oncall: "", channel: "" };
      if (isMap(node)) {
        for (const pair of node.items) {
          const value = pair.value as Node | null;
          if (value !== null && !isScalar(value)) {
            throw humane(`${at(node)}: the team isn't a map of strings`, FORMAT_ADVICE);
          }
          entry[String((pair.key as { value: unknown }).value)] = value === null ? "" : String(value.value ?? "");
        }
      }
      add(teams, at(node), { name: entry.name ?? "", oncall: entry.oncall ?? "", channel: entry.channel ?? "" });
    }
    return new TeamDirectory(teams, source);
  }

  /** The team called name, or undefined when the directory doesn't list it. */
  lookup(name: string): Team | undefined {
    return this.#teams.get(name);
  }

  /** Every team's name, sorted. */
  names(): string[] {
    return [...this.#names];
  }

  /** Every team, sorted by name. */
  teams(): Team[] {
    return this.#names.map((n) => this.#teams.get(n) as Team);
  }

  /**
   * Whether dest is one of the directory's on-call targets or channels, the
   * destinations that are safe as a metric label because the platform, not
   * a team's payload, chose them.
   */
  isKnownDestination(dest: string): boolean {
    for (const t of this.#teams.values()) if (t.oncall === dest || t.channel === dest) return true;
    return false;
  }
}

/**
 * Reads the directory from path. Throws a HumaneError when the file can't be
 * read or isn't a valid directory.
 */
export async function loadTeamsFile(path: string, readFile: (path: string) => Promise<string>): Promise<TeamDirectory> {
  let text: string;
  try {
    text = await readFile(path);
  } catch (err) {
    throw wrap(
      err,
      `cannot open the team directory ${path}`,
      "check that ALERTROUTER_TEAMS_FILE names a readable YAML file, or leave it empty for the bundled directory",
    );
  }
  return TeamDirectory.parse(text, path);
}

function add(teams: Map<string, Team>, pos: string, team: Team): void {
  if (team.name === "") {
    throw humane(`${pos}: a team has no name`, "set name to the value alerts carry in their team label");
  }
  if (!NAME_PATTERN.test(team.name)) {
    throw humane(
      `${pos}: the team name ${JSON.stringify(team.name)} isn't a lower-case identifier`,
      "use letters a-z, digits and underscores, starting with a letter: the name becomes the policy <team>.alerts",
    );
  }
  if (team.oncall === "") {
    throw humane(
      `${pos}: team ${team.name} has no oncall`,
      "set oncall to the paging target of the team's rotation; without it a critical alert pages no one",
    );
  }
  if (!team.channel.startsWith("#")) {
    throw humane(
      `${pos}: team ${team.name} has the channel ${JSON.stringify(team.channel)}, which doesn't start with #`,
      `set channel to the team's alert channel, such as "#checkout-alerts", quoted so YAML doesn't read the # as a comment`,
    );
  }
  if (teams.has(team.name)) {
    throw humane(
      `${pos}: team ${team.name} is listed twice`,
      "merge the two entries: a team has one on-call target and one channel",
    );
  }
  teams.set(team.name, team);
}

function checkKeys(node: Node | null, keys: readonly string[], what: string, at: (n: Node | null) => string): void {
  if (node === null) throw humane(`${at(node)}: ${what} is missing`, FORMAT_ADVICE);
  if (!isMap(node)) {
    throw humane(`${at(node)}: ${what} must be a map with the keys ${keys.join(", ")}`, FORMAT_ADVICE);
  }
  const seen = new Set<string>();
  for (const pair of node.items) {
    const keyNode = pair.key as Node;
    const key = isScalar(keyNode) ? String(keyNode.value) : String(keyNode);
    if (!keys.includes(key)) {
      throw new HumaneError(`${at(keyNode)}: unknown key ${JSON.stringify(key)} in ${what}`, [keyAdvice(key, keys)]);
    }
    if (seen.has(key)) {
      throw humane(
        `${at(keyNode)}: the key ${JSON.stringify(key)} is set twice in ${what}`,
        "keep one of them; YAML would silently use the last",
      );
    }
    seen.add(key);
  }
}

function keyAdvice(key: string, keys: readonly string[]): string {
  const known = `the keys are ${keys.join(", ")}`;
  let best = "";
  let bestDist = 3; // more than two edits away is not a typo
  for (const k of keys) {
    const d = distance(key.toLowerCase(), k);
    if (d < bestDist) {
      best = k;
      bestDist = d;
    }
  }
  return best === "" ? known : `did you mean ${JSON.stringify(best)}? ${known}`;
}

function distance(a: string, b: string): number {
  let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
  let cur = new Array<number>(b.length + 1).fill(0);
  for (let i = 1; i <= a.length; i++) {
    cur[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      cur[j] = Math.min((prev[j] ?? 0) + 1, (cur[j - 1] ?? 0) + 1, (prev[j - 1] ?? 0) + cost);
    }
    [prev, cur] = [cur, prev];
  }
  return prev[b.length] ?? 0;
}
