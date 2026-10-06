// Packages the extension as VSIX files with vsce. A platform VSIX carries a
// sigil binary in bin/, which the extension runs before looking on PATH; the
// universal VSIX carries none, and is what Open VSX serves on every
// platform without its own (Windows, Alpine, and so on).
//
//   bun scripts/package.ts --binary <sigil> --target <target>   one platform VSIX
//   bun scripts/package.ts --universal                           the universal VSIX
//   bun scripts/package.ts --release <dir>                       every VSIX of a release
//
// --release reads the archives GoReleaser built (sigil_<version>_<os>_<arch>.tar.gz)
// from <dir>, which the release workflow has verified, and writes the universal
// VSIX and one per archive. --out picks the directory the VSIX files go to
// (default: the extension's directory). Run `bun run build` first.

import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";
import manifest from "../package.json" with { type: "json" };

/** A GoReleaser archive's platform, and the vsce target that runs it. */
export interface Platform {
  /** GOOS_GOARCH, as in the archive name. */
  archive: string;
  /** The vsce --target. */
  target: string;
}

/** The platforms .goreleaser.yaml builds the CLI for. */
export const PLATFORMS: readonly Platform[] = [
  { archive: "darwin_arm64", target: "darwin-arm64" },
  { archive: "darwin_amd64", target: "darwin-x64" },
  { archive: "linux_amd64", target: "linux-x64" },
  { archive: "linux_arm64", target: "linux-arm64" },
];

const root = join(import.meta.dir, "..");

/** The VSIX file name for a target, or the universal VSIX without one. */
export function vsixName(version: string, target?: string): string {
  return target === undefined ? `sigil-${version}.vsix` : `sigil-${target}-${version}.vsix`;
}

/** The vsce target for the machine this runs on, such as darwin-arm64. */
export function hostTarget(platform: string = process.platform, arch: string = process.arch): string {
  return `${platform}-${arch}`;
}

/** Packages one VSIX: with binary copied to bin/ for target, or universal without both. */
function pack(out: string, binary?: string, target?: string): string {
  const bin = join(root, "bin");
  rmSync(bin, { recursive: true, force: true });
  const file = join(out, vsixName(manifest.version, target));
  const args = ["vsce", "package", "--no-dependencies", "--out", file];
  try {
    if (binary !== undefined && target !== undefined) {
      mkdirSync(bin);
      const dest = join(bin, target.startsWith("win32") ? "sigil.exe" : "sigil");
      copyFileSync(binary, dest);
      chmodSync(dest, 0o755);
      args.push("--target", target);
    }
    execFileSync("bunx", args, { cwd: root, stdio: "inherit" });
  } finally {
    rmSync(bin, { recursive: true, force: true });
  }
  return file;
}

function main(): void {
  const { values } = parseArgs({
    options: {
      binary: { type: "string" },
      target: { type: "string" },
      universal: { type: "boolean" },
      release: { type: "string" },
      out: { type: "string" },
    },
  });
  const out = resolve(values.out ?? root);
  mkdirSync(out, { recursive: true });
  if (!existsSync(join(root, "dist/extension.js"))) throw new Error("dist/extension.js is missing; run bun run build");

  if (values.release !== undefined) {
    const dir = resolve(values.release);
    pack(out);
    for (const p of PLATFORMS) {
      const archive = join(dir, `sigil_${manifest.version}_${p.archive}.tar.gz`);
      const tmp = mkdtempSync(join(tmpdir(), "sigil-vsix-"));
      execFileSync("tar", ["-xzf", archive, "-C", tmp, "sigil"]);
      pack(out, join(tmp, "sigil"), p.target);
      rmSync(tmp, { recursive: true, force: true });
    }
    return;
  }
  if (values.universal === true) {
    pack(out);
    return;
  }
  if (values.binary === undefined) throw new Error("pass --binary <sigil>, --universal or --release <dir>");
  pack(out, resolve(values.binary), values.target ?? hostTarget());
}

if (import.meta.main) main();
