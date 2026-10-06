import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { hostTarget, PLATFORMS, vsixName } from "../scripts/package";
import { repo } from "./sigil-cli";

interface GoReleaser {
  builds: { id: string; goos: string[]; goarch: string[] }[];
}

describe("the release's VSIX files", () => {
  test("cover exactly the platforms GoReleaser builds the CLI for", () => {
    const config = Bun.YAML.parse(readFileSync(join(repo, ".goreleaser.yaml"), "utf8")) as GoReleaser;
    const cli = config.builds.find((b) => b.id === "sigil");
    expect(cli).toBeDefined();
    const built = (cli?.goos ?? []).flatMap((os) => (cli?.goarch ?? []).map((arch) => `${os}_${arch}`)).sort();
    expect(PLATFORMS.map((p) => p.archive).sort()).toEqual(built);
  });

  test("use vsce's names for the targets", () => {
    const targets = [
      "darwin-arm64",
      "darwin-x64",
      "linux-arm64",
      "linux-x64",
      "linux-armhf",
      "alpine-x64",
      "win32-x64",
    ];
    for (const p of PLATFORMS) expect(targets).toContain(p.target);
  });

  test("are named after the target and the version", () => {
    expect(vsixName("0.7.3", "darwin-arm64")).toBe("sigil-darwin-arm64-0.7.3.vsix");
    expect(vsixName("0.7.3")).toBe("sigil-0.7.3.vsix");
  });

  test("hostTarget is Node's platform and arch, which vsce's targets use", () => {
    expect(hostTarget("darwin", "arm64")).toBe("darwin-arm64");
    expect(hostTarget("linux", "x64")).toBe("linux-x64");
    expect(hostTarget()).toBe(`${process.platform}-${process.arch}`);
  });
});
