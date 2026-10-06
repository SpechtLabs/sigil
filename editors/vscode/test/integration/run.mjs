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

import { spawn } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { downloadAndUnzipVSCode } from "@vscode/test-electron";

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

const scratch = mkdtempSync(join(tmpdir(), "sigil-vscode-"));

// The fake server's launchers. sigil runs the fake server; sigil-old stands
// in for a sigil from before the language server, which rejects
// `lsp --stdio` and exits; path/sigil is the one on PATH in restricted mode.
const fake = join(root, "dist/test/fake-server.js");
const launcher = join(scratch, "sigil");
const oldLauncher = join(scratch, "sigil-old");
const pathDir = join(scratch, "path");
const pathLauncher = join(pathDir, "sigil");
mkdirSync(pathDir);
for (const [file, args] of [
  [launcher, ""],
  [oldLauncher, " old"],
  [pathLauncher, ""],
]) {
  writeFileSync(file, `#!/bin/sh\nexec "${process.execPath}" "${fake}"${args} "$@"\n`);
  chmodSync(file, 0o755);
}

let vscodeExecutablePath;
try {
  vscodeExecutablePath = await downloadAndUnzipVSCode({ version, cachePath: join(root, ".vscode-test") });
} catch (err) {
  skip(`couldn't download VS Code ${version}: ${err instanceof Error ? err.message : String(err)}`);
}

// A VS Code terminal sets this for its own children; the VS Code under test
// would start as plain Node with it.
delete process.env.ELECTRON_RUN_AS_NODE;

// Each suite gets its own copy of the fixture, whose workspace settings set
// sigil.path to the fake server, and its own user data. The trusted suite
// runs with workspace trust off; the restricted one leaves it on and never
// prompts, so the workspace stays untrusted, and puts path/sigil first on
// PATH, which is what the extension has to fall back to.
await runSuite("trusted", { "sigil.checkForUpdates": false }, {}, ["--disable-workspace-trust"]);
await runSuite(
  "restricted",
  { "sigil.checkForUpdates": false, "security.workspace.trust.startupPrompt": "never" },
  { PATH: `${pathDir}${delimiter}${process.env.PATH ?? ""}`, SIGIL_FAKE_PATH: pathLauncher },
  [],
);

// VS Code is started here rather than with test-electron's runTests, which
// always passes --disable-workspace-trust and so can't run the restricted
// suite. The flags are runTests' others.
async function runSuite(suite, userSettings, env, args) {
  const dir = join(scratch, suite);
  const workspace = join(dir, "workspace");
  cpSync(join(root, "test/integration/fixture"), workspace, { recursive: true });
  mkdirSync(join(workspace, ".vscode"));
  writeFileSync(join(workspace, ".vscode/settings.json"), `${JSON.stringify({ "sigil.path": launcher }, null, 2)}\n`);
  // sigil.checkForUpdates is an application setting, so only user settings
  // can turn it off; the tests mustn't ask GitHub.
  mkdirSync(join(dir, "user-data/User"), { recursive: true });
  writeFileSync(join(dir, "user-data/User/settings.json"), `${JSON.stringify(userSettings, null, 2)}\n`);
  console.log(`VS Code ${version}, ${suite} workspace:`);
  const code = await new Promise((resolve) => {
    const child = spawn(
      vscodeExecutablePath,
      [
        workspace,
        "--disable-extensions",
        ...args,
        "--no-sandbox",
        "--disable-gpu-sandbox",
        "--disable-updates",
        "--skip-welcome",
        "--skip-release-notes",
        "--no-cached-data",
        // Without it VS Code on macOS replaces PATH with the login shell's.
        "--force-disable-user-env",
        `--extensionDevelopmentPath=${root}`,
        `--extensionTestsPath=${join(root, "dist/test/suite.js")}`,
        `--user-data-dir=${join(dir, "user-data")}`,
        `--extensions-dir=${join(dir, "extensions")}`,
      ],
      {
        env: {
          ...process.env,
          SIGIL_TEST_SUITE: suite,
          SIGIL_FAKE_LOG: join(dir, "fake-server.log"),
          SIGIL_FAKE_OLD: oldLauncher,
          ...env,
        },
        stdio: "inherit",
      },
    );
    child.on("error", (err) => {
      console.error(`Couldn't start VS Code: ${err.message}`);
      resolve(1);
    });
    child.on("exit", (status, signal) => resolve(status ?? signal));
  });
  if (code !== 0) {
    console.error(`The ${suite} integration tests failed: VS Code exited with ${code}`);
    // What the fake server read, in order, which says how far the client got.
    const log = join(dir, "fake-server.log");
    console.error(`The fake server's log:\n${existsSync(log) ? readFileSync(log, "utf8") : "(empty)"}`);
    process.exit(1);
  }
}
