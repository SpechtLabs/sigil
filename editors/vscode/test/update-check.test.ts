import { describe, expect, test } from "bun:test";
import {
  CHECK_INTERVAL_MS,
  checkForUpdate,
  type Deps,
  fetchLatestRelease,
  isDue,
  isNewer,
  LATEST_RELEASE_URL,
  needsUpdateNotice,
  type Outcome,
  parseVersion,
  RELEASES_URL,
  releasePage,
} from "../src/update-check";

const DAY = CHECK_INTERVAL_MS;
const RELEASE = { tag: "v0.8.0", url: "https://github.com/SpechtLabs/sigil/releases/tag/v0.8.0" };

/** Deps that record what the check did, with the given overrides. */
function deps(overrides: Partial<Deps> = {}) {
  const calls: string[] = [];
  const d: Deps = {
    current: "0.7.3",
    now: 10 * DAY,
    lastChecked: undefined,
    skipped: undefined,
    setLastChecked: async (when) => {
      calls.push(`checked ${when}`);
    },
    setSkipped: async (version) => {
      calls.push(`skipped ${version}`);
    },
    fetchLatest: async () => {
      calls.push("fetch");
      return RELEASE;
    },
    notify: async (version) => {
      calls.push(`notify ${version}`);
      return undefined;
    },
    open: async (url) => {
      calls.push(`open ${url}`);
    },
    ...overrides,
  };
  return { d, calls };
}

describe("checkForUpdate", () => {
  const cases: { name: string; overrides: Partial<Deps>; want: Outcome; calls: string[] }[] = [
    {
      name: "a check that isn't due does nothing",
      overrides: { lastChecked: 10 * DAY - 1 },
      want: "not-due",
      calls: [],
    },
    {
      name: "the same version is up to date",
      overrides: { current: "0.8.0" },
      want: "up-to-date",
      calls: [`checked ${10 * DAY}`, "fetch"],
    },
    {
      name: "an older release is up to date",
      overrides: { current: "0.9.0" },
      want: "up-to-date",
      calls: [`checked ${10 * DAY}`, "fetch"],
    },
    {
      name: "a skipped version isn't offered again",
      overrides: { skipped: "0.8.0", lastChecked: 9 * DAY },
      want: "skipped",
      calls: [`checked ${10 * DAY}`, "fetch"],
    },
    {
      name: "a newer version is offered, and dismissing it changes nothing",
      overrides: {},
      want: "notified",
      calls: [`checked ${10 * DAY}`, "fetch", "notify 0.8.0"],
    },
    {
      name: "a skip of an older version doesn't hide a newer one",
      overrides: { skipped: "0.7.4" },
      want: "notified",
      calls: [`checked ${10 * DAY}`, "fetch", "notify 0.8.0"],
    },
  ];
  for (const c of cases) {
    test(c.name, async () => {
      const { d, calls } = deps(c.overrides);
      expect(await checkForUpdate(d)).toBe(c.want);
      expect(calls).toEqual(c.calls);
    });
  }

  test("Download opens the release page", async () => {
    const { d, calls } = deps({ notify: async () => "download" });
    expect(await checkForUpdate(d)).toBe("downloaded");
    expect(calls).toEqual([`checked ${10 * DAY}`, "fetch", `open ${RELEASE.url}`]);
  });

  test("Skip this version records it", async () => {
    const { d, calls } = deps({ notify: async () => "skip" });
    expect(await checkForUpdate(d)).toBe("skip-chosen");
    expect(calls).toEqual([`checked ${10 * DAY}`, "fetch", "skipped 0.8.0"]);
  });

  test("a failed request rejects, and the check still counts for today", async () => {
    const { d, calls } = deps({
      fetchLatest: async () => {
        throw new Error("offline");
      },
    });
    await expect(checkForUpdate(d)).rejects.toThrow("offline");
    expect(calls).toEqual([`checked ${10 * DAY}`]);
  });
});

describe("isDue", () => {
  const cases: { lastChecked: number | undefined; now: number; want: boolean }[] = [
    { lastChecked: undefined, now: 0, want: true },
    { lastChecked: 0, now: DAY - 1, want: false },
    { lastChecked: 0, now: DAY, want: true },
    { lastChecked: 5 * DAY, now: DAY, want: true }, // the clock went backwards
  ];
  for (const c of cases) {
    test(`last ${c.lastChecked}, now ${c.now}`, () => {
      expect(isDue(c.lastChecked, c.now)).toBe(c.want);
    });
  }
});

describe("versions", () => {
  test("parseVersion takes major.minor.patch with or without v", () => {
    expect(parseVersion("0.7.3")).toEqual([0, 7, 3]);
    expect(parseVersion("v1.10.0")).toEqual([1, 10, 0]);
    expect(parseVersion(" 2.0.1 ")).toEqual([2, 0, 1]);
    for (const bad of ["", "1.2", "1.2.3-rc.1", "v", "latest"]) expect(parseVersion(bad)).toBeUndefined();
  });

  const cases: { latest: string; current: string; want: boolean }[] = [
    { latest: "0.8.0", current: "0.7.3", want: true },
    { latest: "0.7.10", current: "0.7.9", want: true },
    { latest: "1.0.0", current: "0.99.99", want: true },
    { latest: "0.7.3", current: "0.7.3", want: false },
    { latest: "0.7.2", current: "0.7.3", want: false },
    { latest: "0.6.9", current: "0.7.0", want: false },
    { latest: "garbage", current: "0.7.3", want: false },
    { latest: "0.8.0", current: "garbage", want: false },
  ];
  for (const c of cases) {
    test(`${c.latest} newer than ${c.current}: ${c.want}`, () => {
      expect(isNewer(c.latest, c.current)).toBe(c.want);
    });
  }
});

test("only VS Code itself needs the notice", () => {
  expect(needsUpdateNotice("vscode")).toBe(true);
  expect(needsUpdateNotice("vscode-insiders")).toBe(true);
  for (const scheme of ["vscodium", "cursor", "windsurf", "code-oss", ""])
    expect(needsUpdateNotice(scheme)).toBe(false);
});

describe("fetchLatestRelease", () => {
  function fakeFetch(
    status: number,
    body: unknown,
    seen: { url?: string; init?: RequestInit | undefined } = {},
  ): typeof fetch {
    return (async (url: string | URL | Request, init?: RequestInit) => {
      seen.url = String(url);
      seen.init = init;
      return new Response(JSON.stringify(body), { status });
    }) as typeof fetch;
  }

  test("reads the tag and the page from GitHub's API", async () => {
    const seen: { url?: string; init?: RequestInit | undefined } = {};
    const got = await fetchLatestRelease(fakeFetch(200, { tag_name: "v0.8.0", html_url: RELEASE.url }, seen));
    expect(got).toEqual(RELEASE);
    expect(seen.url).toBe(LATEST_RELEASE_URL);
    expect(seen.init?.headers).toMatchObject({ Accept: "application/vnd.github+json" });
  });

  test("an error status rejects", async () => {
    await expect(fetchLatestRelease(fakeFetch(403, { message: "rate limited" }))).rejects.toThrow("403");
  });

  test("a body without the fields rejects", async () => {
    await expect(fetchLatestRelease(fakeFetch(200, { tag_name: 1 }))).rejects.toThrow("no tag_name");
  });
});

describe("releasePage", () => {
  const cases: { url: string; want: string }[] = [
    { url: RELEASE.url, want: RELEASE.url },
    {
      url: "https://github.com/SpechtLabs/sigil/releases/latest",
      want: "https://github.com/SpechtLabs/sigil/releases/latest",
    },
    { url: "http://github.com/SpechtLabs/sigil/releases/tag/v0.8.0", want: RELEASES_URL },
    { url: "https://evil.example/SpechtLabs/sigil/releases/tag/v0.8.0", want: RELEASES_URL },
    { url: "https://github.com.evil.example/SpechtLabs/sigil/releases/tag/v1", want: RELEASES_URL },
    { url: "https://user@github.com/SpechtLabs/sigil/releases/tag/v0.8.0", want: RELEASES_URL },
    { url: "https://github.com/SpechtLabs/sigil/releases/../../other/repo", want: RELEASES_URL },
    { url: "https://github.com/someone/else/releases/tag/v0.8.0", want: RELEASES_URL },
    { url: "javascript:alert(1)", want: RELEASES_URL },
    { url: "not a url", want: RELEASES_URL },
  ];
  for (const c of cases) {
    test(c.url, () => {
      expect(releasePage(c.url)).toBe(c.want);
    });
  }

  test("Download opens the releases page when the API's link isn't one of ours", async () => {
    const { d, calls } = deps({
      notify: async () => "download",
      fetchLatest: async () => ({ tag: "v0.8.0", url: "https://evil.example/x" }),
    });
    expect(await checkForUpdate(d)).toBe("downloaded");
    expect(calls.at(-1)).toBe(`open ${RELEASES_URL}`);
  });
});
