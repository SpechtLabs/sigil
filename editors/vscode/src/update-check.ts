// The update notice for VS Code. The extension isn't in the Visual Studio
// Marketplace, so VS Code never updates it; once a day this asks GitHub for
// the latest Sigil release and, when it's newer than the extension, offers
// a link to it. Editors that install from Open VSX update the extension
// themselves and never ask. Nothing here imports vscode: the extension passes
// in the clock, the stored state, the request and the notification, so the
// unit tests drive every path.

/** The latest stable Sigil release, as GitHub's API returns it. */
export const LATEST_RELEASE_URL = "https://api.github.com/repos/SpechtLabs/sigil/releases/latest";

/** The releases page, which the notice opens when the API's link isn't one of its pages. */
export const RELEASES_URL = "https://github.com/SpechtLabs/sigil/releases";

/** How long after a check the next one is due. */
export const CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000;

/** What a check reads and writes. */
export interface Deps {
  /** The extension's version, from its package.json. */
  current: string;
  /** Milliseconds since the epoch. */
  now: number;
  /** When the last check ran, or undefined before the first. */
  lastChecked: number | undefined;
  /** The version the user chose to skip, if any. */
  skipped: string | undefined;
  /** Records when this check ran. */
  setLastChecked(when: number): Promise<void>;
  /** Records a version the user chose to skip. */
  setSkipped(version: string): Promise<void>;
  /** The latest release's tag and page. */
  fetchLatest(): Promise<{ tag: string; url: string }>;
  /** Shows the notice and resolves to the action the user picked, if any. */
  notify(version: string): Promise<"download" | "skip" | undefined>;
  /** Opens the release page. */
  open(url: string): Promise<void>;
}

/** What a check did, for the tests and the output channel. */
export type Outcome = "not-due" | "up-to-date" | "skipped" | "notified" | "downloaded" | "skip-chosen";

/** Whether editors with this URI scheme get the extension from GitHub, and so need the notice. */
export function needsUpdateNotice(uriScheme: string): boolean {
  return uriScheme === "vscode" || uriScheme === "vscode-insiders";
}

/** Whether a check is due: none yet, a day since the last, or a clock that went backwards. */
export function isDue(lastChecked: number | undefined, now: number): boolean {
  return lastChecked === undefined || now - lastChecked >= CHECK_INTERVAL_MS || now < lastChecked;
}

/** Parses major.minor.patch, with or without a leading v; anything else is undefined. */
export function parseVersion(version: string): [number, number, number] | undefined {
  const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(version.trim());
  if (m === null) return undefined;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

/** Whether latest is a newer version than current. Unparseable versions are never newer. */
export function isNewer(latest: string, current: string): boolean {
  const l = parseVersion(latest);
  const c = parseVersion(current);
  if (l === undefined || c === undefined) return false;
  for (let i = 0; i < 3; i++) {
    if (l[i] !== c[i]) return (l[i] ?? 0) > (c[i] ?? 0);
  }
  return false;
}

/**
 * Runs one check: when one is due, records it before asking GitHub (so a
 * failed request isn't retried until tomorrow), then offers the newer
 * release unless the user skipped that version. A failed request rejects.
 */
export async function checkForUpdate(deps: Deps): Promise<Outcome> {
  if (!isDue(deps.lastChecked, deps.now)) return "not-due";
  await deps.setLastChecked(deps.now);
  const latest = await deps.fetchLatest();
  const version = latest.tag.replace(/^v/, "");
  if (!isNewer(version, deps.current)) return "up-to-date";
  if (deps.skipped === version) return "skipped";
  const choice = await deps.notify(version);
  if (choice === "download") {
    await deps.open(releasePage(latest.url));
    return "downloaded";
  }
  if (choice === "skip") {
    await deps.setSkipped(version);
    return "skip-chosen";
  }
  return "notified";
}

/**
 * The page to open for a release: the API's html_url when it's a page under
 * the Sigil releases on github.com, else the releases page. The response is
 * input from the network, and the notice opens it in a browser.
 */
export function releasePage(url: string): string {
  try {
    const u = new URL(url);
    const ours =
      u.protocol === "https:" &&
      u.host === "github.com" &&
      u.username === "" &&
      u.password === "" &&
      u.pathname.startsWith("/SpechtLabs/sigil/releases/");
    return ours ? u.href : RELEASES_URL;
  } catch {
    return RELEASES_URL;
  }
}

/** Reads the latest release from GitHub's API. */
export async function fetchLatestRelease(fetchImpl: typeof fetch = fetch): Promise<{ tag: string; url: string }> {
  const res = await fetchImpl(LATEST_RELEASE_URL, {
    headers: { Accept: "application/vnd.github+json", "User-Agent": "sigil-vscode" },
    signal: AbortSignal.timeout(10_000),
  });
  if (!res.ok) throw new Error(`GitHub answered ${res.status} for the latest release`);
  const body = (await res.json()) as { tag_name?: unknown; html_url?: unknown };
  if (typeof body.tag_name !== "string" || typeof body.html_url !== "string") {
    throw new Error("GitHub's latest release has no tag_name or html_url");
  }
  return { tag: body.tag_name, url: body.html_url };
}
