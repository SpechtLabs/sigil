// Runs after `bun install` (postinstall). bun installs the file: dependency
// on bindings/typescript as a directory of symlinks back into it, and
// Turbopack refuses to follow a symlink out of the project, so `next build`
// couldn't read the package. This replaces each symlink with a copy of what
// it points to. Run `bun install --force` after rebuilding the bindings, as
// with any file: dependency.

import { cpSync, lstatSync, readdirSync, realpathSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";

const pkg = join(dirname(import.meta.dirname), "node_modules", "@spechtlabs", "sigil");

function dereference(path: string): void {
  const info = lstatSync(path, { throwIfNoEntry: false });
  if (info === undefined) return;
  if (info.isSymbolicLink()) {
    const target = realpathSync(path);
    rmSync(path, { recursive: true, force: true });
    cpSync(target, path, { recursive: true, dereference: true });
    return;
  }
  if (info.isDirectory()) for (const name of readdirSync(path)) dereference(join(path, name));
}

dereference(pkg);
