// Runs the integration tests (suite.ts) in a real VS Code: it copies the
// fixture into a temporary workspace whose sigil.path is a launcher for the
// fake server, downloads VS Code into .vscode-test/, and starts it with only
// this extension. `bun run test:integration` builds the bundles it loads
// first.
//
// When VS Code can't run here (no download, no display on Linux, Windows,
// which the launcher doesn't support) it says why and exits 0, so a laptop
// without them still passes `mise run vscode-test`. CI sets
// SIGIL_VSCODE_TEST_REQUIRED=1, which turns each of those into a failure.
// VSCODE_TEST_VERSION picks the VS Code build: "stable" (the default),
// "insiders" or a version such as 1.91.0, the oldest engines.vscode allows.

import { chmodSync, cpSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { downloadAndUnzipVSCode, runTests } from "@vscode/test-electron";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");
const required = process.env.SIGIL_VSCODE_TEST_REQUIRED === "1";
const version = process.env.VSCODE_TEST_VERSION || "stable";

function skip(reason) {
  if (required) {
    console.error(`integration tests can't run: ${reason}`);
    process.exit(1);
  }
  console.log(`Skipping the VS Code integration tests: ${reason}`);
  process.exit(0);
}

if (process.platform === "win32") skip("the fake server's launcher is a shell script");
if (process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) {
  skip("there's no display; run it under xvfb-run");
}

// The workspace: the fixture, with the fake server as sigil.path.
const scratch = mkdtempSync(join(tmpdir(), "sigil-vscode-"));
const workspace = join(scratch, "workspace");
cpSync(join(root, "test/integration/fixture"), workspace, { recursive: true });
// sigil runs the fake server; sigil-old stands in for a sigil from before the
// language server, which rejects `lsp --stdio` and exits.
const fake = join(root, "dist/test/fake-server.js");
const launcher = join(scratch, "sigil");
const oldLauncher = join(scratch, "sigil-old");
writeFileSync(launcher, `#!/bin/sh\nexec "${process.execPath}" "${fake}" "$@"\n`);
writeFileSync(oldLauncher, `#!/bin/sh\nexec "${process.execPath}" "${fake}" old "$@"\n`);
chmodSync(launcher, 0o755);
chmodSync(oldLauncher, 0o755);
mkdirSync(join(workspace, ".vscode"));
writeFileSync(join(workspace, ".vscode/settings.json"), `${JSON.stringify({ "sigil.path": launcher }, null, 2)}\n`);
// sigil.checkForUpdates is an application setting, so only user settings can
// turn it off; the tests mustn't ask GitHub.
const userSettings = join(scratch, "user-data/User");
mkdirSync(userSettings, { recursive: true });
writeFileSync(join(userSettings, "settings.json"), `${JSON.stringify({ "sigil.checkForUpdates": false }, null, 2)}\n`);

let vscodeExecutablePath;
try {
  vscodeExecutablePath = await downloadAndUnzipVSCode({ version, cachePath: join(root, ".vscode-test") });
} catch (err) {
  skip(`couldn't download VS Code ${version}: ${err instanceof Error ? err.message : String(err)}`);
}

// A VS Code terminal sets this for its own children; the VS Code under test
// would start as plain Node with it.
delete process.env.ELECTRON_RUN_AS_NODE;

try {
  await runTests({
    vscodeExecutablePath,
    extensionDevelopmentPath: root,
    extensionTestsPath: join(root, "dist/test/suite.js"),
    extensionTestsEnv: { SIGIL_FAKE_LOG: join(scratch, "fake-server.log"), SIGIL_FAKE_OLD: oldLauncher },
    launchArgs: [
      workspace,
      "--disable-extensions",
      "--disable-workspace-trust",
      "--skip-welcome",
      "--skip-release-notes",
      `--user-data-dir=${join(scratch, "user-data")}`,
    ],
  });
} catch (err) {
  console.error(`The VS Code integration tests failed: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
}
