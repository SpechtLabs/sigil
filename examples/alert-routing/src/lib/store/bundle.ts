// A policy bundle: the `.sigil` files of a directory, as the virtual files
// @spechtlabs/sigil compiles, and the fingerprint that tells one version of
// the directory from the next. The fingerprint is the Go service's,
// byte for byte, so the two report the same value for the same directory.

import { createHash } from "node:crypto";
import { readdir, readFile, stat } from "node:fs/promises";
import { join } from "node:path";

import type { SourceFile } from "@spechtlabs/sigil";

import { wrap } from "../errors";

/** A bundle read at one moment: its files and their fingerprint. */
export interface Bundle {
  files: SourceFile[];
  fingerprint: string;
}

/** Where a store reads its team bundle from: a directory, or the files bundled with the app. */
export interface BundleSource {
  /** The directory, or "embedded"; reported in /api/v1/policies, logs, spans and metrics. */
  readonly source: string;
  /** Reads the bundle as it is now. Throws a HumaneError when it can't be read. */
  read(): Promise<Bundle>;
}

/** The source name of the team bundle bundled with the app. */
export const SOURCE_EMBEDDED = "embedded";

/** A bundle that never changes: the team policies bundled with the app. */
export function embeddedBundle(files: readonly SourceFile[]): BundleSource {
  const bundle: Bundle = { files: [...files], fingerprint: fingerprint(files) };
  return { source: SOURCE_EMBEDDED, read: async () => bundle };
}

/**
 * A bundle read from dir on every read, so a reload sees what's there now.
 * Paths are relative to dir, like `checkout/alerts.sigil`, which is how
 * positions in a trace name them.
 */
export function directoryBundle(dir: string): BundleSource {
  return {
    source: dir,
    async read() {
      try {
        const files = await readDir(dir, "");
        return { files, fingerprint: fingerprint(files) };
      } catch (err) {
        throw wrap(
          wrap(
            err,
            "fingerprinting the team policies failed",
            "check that the policies directory exists and is readable",
          ),
          `reading the AlertRouting policies from ${dir} failed`,
          "check that the policies directory exists and is readable",
        );
      }
    },
  };
}

/**
 * The SHA-256 of the files in the order a depth-first walk of their
 * directory visits them (each directory's entries by name), each as
 * `<len(path)>:<path><len(data)>:<data>`. Lengths are in bytes.
 */
export function fingerprint(files: readonly SourceFile[]): string {
  const h = createHash("sha256");
  for (const f of [...files].sort(byWalkOrder)) {
    const path = Buffer.from(f.path);
    const data = Buffer.from(f.source);
    h.update(`${path.length}:`);
    h.update(path);
    h.update(`${data.length}:`);
    h.update(data);
  }
  return h.digest("hex");
}

// Reads every .sigil file under dir, skipping names that start with a dot
// (editor swap files, a ConfigMap's ..data links), following symbolic links
// like os.DirFS does.
async function readDir(root: string, rel: string): Promise<SourceFile[]> {
  const entries = (await readdir(join(root, rel))).filter((n) => !n.startsWith(".")).sort(compareBytes);
  const out: SourceFile[] = [];
  for (const name of entries) {
    const path = rel === "" ? name : `${rel}/${name}`;
    const info = await stat(join(root, path));
    if (info.isDirectory()) {
      out.push(...(await readDir(root, path)));
    } else if (name.endsWith(".sigil")) {
      out.push({ path, source: await readFile(join(root, path), "utf8") });
    }
  }
  return out;
}

// Orders paths the way a directory walk visits them: segment by segment,
// each compared byte-wise, as Go sorts directory entries.
function byWalkOrder(a: SourceFile, b: SourceFile): number {
  const as = a.path.split("/");
  const bs = b.path.split("/");
  for (let i = 0; i < Math.min(as.length, bs.length); i++) {
    const c = compareBytes(as[i] as string, bs[i] as string);
    if (c !== 0) return c;
  }
  return as.length - bs.length;
}

function compareBytes(a: string, b: string): number {
  return Buffer.compare(Buffer.from(a), Buffer.from(b));
}
