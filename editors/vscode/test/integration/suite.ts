// The integration tests, run inside VS Code by run.mjs against a copy of
// fixture/ whose sigil.path points at fake-server.ts. VS Code loads this
// module and calls run(); a rejection fails the run. Each step logs its name,
// so a failure in CI says which one. SIGIL_TEST_SUITE picks the suite: the
// trusted one runs with workspace trust off, the restricted one in an
// untrusted workspace.

import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import * as vscode from "vscode";
import type { Api } from "../../src/extension";

const EXTENSION_ID = "spechtlabs.sigil";

// The whole suite's budget: a step that hangs fails the run instead of CI's job.
const SUITE_TIMEOUT_MS = 180_000;

// What trace() saw, printed only when the suite fails.
const traced: string[] = [];

export function run(): Promise<void> {
  return Promise.race([
    process.env.SIGIL_TEST_SUITE === "restricted" ? restrictedSuite() : trustedSuite(),
    new Promise<never>((_, reject) =>
      setTimeout(
        () => reject(new Error(`the suite took longer than ${SUITE_TIMEOUT_MS} ms`)),
        SUITE_TIMEOUT_MS,
      ).unref(),
    ),
  ]).catch((err: unknown) => {
    console.log(`    What happened, in order:\n${traced.join("\n")}`);
    throw err;
  });
}

async function trustedSuite(): Promise<void> {
  const log = process.env.SIGIL_FAKE_LOG;
  assert.ok(log, "SIGIL_FAKE_LOG is set by run.mjs");
  const folder = vscode.workspace.workspaceFolders?.[0];
  assert.ok(folder, "the fixture is open as a workspace folder");

  const extension = vscode.extensions.getExtension<Api>(EXTENSION_ID);
  assert.ok(extension, `${EXTENSION_ID} is installed`);

  await step("the extension activates for a workspace with .sigil files", async () => {
    await waitFor(() => extension.isActive, "the extension to activate on its own");
  });
  const { server } = extension.exports;
  const tracing = trace(server);

  await step("the sigil language is registered", async () => {
    assert.ok((await vscode.languages.getLanguages()).includes("sigil"));
    const doc = await vscode.workspace.openTextDocument(join(folder.uri.fsPath, "policies/production.sigil"));
    assert.equal(doc.languageId, "sigil");
    await vscode.window.showTextDocument(doc);
  });

  await step("the client starts sigil.path as `lsp --stdio`", async () => {
    await waitFor(() => server.status.kind === "running", "the server to run");
    const status = server.status;
    assert.equal(status.kind === "running" && status.binary.source, "setting");
    assert.deepEqual(entries(log).find((e) => e.args?.[0] === "lsp")?.args, ["lsp", "--stdio"]);
  });

  await step("diagnostics from the server reach the editor", async () => {
    const uri = vscode.Uri.file(join(folder.uri.fsPath, "policies/production.sigil"));
    await waitFor(
      () => vscode.languages.getDiagnostics(uri).some((d) => d.message === "fake diagnostic"),
      "the fake diagnostic",
    );
  });

  await step("a document that gets its tab after the client started reaches the server", async () => {
    // The client syncs the documents that are open when it starts only once
    // they're in a tab, as they are here: no Sigil editor is open, and
    // common.sigil is open with no tab, when the server restarts.
    await vscode.commands.executeCommand("workbench.action.closeAllEditors");
    const common = await vscode.workspace.openTextDocument(join(folder.uri.fsPath, "policies/common.sigil"));
    const initialized = entries(log).filter((e) => e.method === "initialized").length;
    await vscode.commands.executeCommand("sigil.restartServer");
    await waitFor(
      () => entries(log).filter((e) => e.method === "initialized").length > initialized,
      "the restarted client to initialize",
    );
    const sent = entries(log).length;
    await vscode.window.showTextDocument(common, { preview: false });
    await waitFor(
      () =>
        entries(log)
          .slice(sent)
          .some((e) => e.method === "textDocument/didOpen" && e.uri === common.uri.toString()),
      "didOpen for common.sigil once it's in a tab",
    );
  });

  await step("a new .sigil file reaches the server through the watchers it registered", async () => {
    // VS Code sets a watcher up asynchronously after the client asks for it,
    // so a file written too early goes unseen: wait for the registration,
    // then write files until one is seen.
    await waitFor(() => entries(log).some((e) => e.method === "response"), "the watcher registration");
    let n = 0;
    await waitFor(
      () => {
        if (entries(log).some((e) => e.method === "workspace/didChangeWatchedFiles")) return true;
        writeFileSync(join(folder.uri.fsPath, `policies/new-${n++}.sigil`), "module deploy.extra: DeployApproval@1\n");
        return false;
      },
      "workspace/didChangeWatchedFiles",
      20_000,
      500,
    );
  });

  await step("Sigil: Show sigil Version runs sigil version", async () => {
    const { info, binary } = await server.version();
    assert.equal(info.version, "0.0.0-fake");
    assert.equal(binary.source, "setting");
    await vscode.commands.executeCommand("sigil.showVersion");
  });

  await step("Sigil: Restart Language Server starts a new client", async () => {
    const before = server.starts;
    await vscode.commands.executeCommand("sigil.restartServer");
    await waitFor(() => server.starts === before + 1 && server.status.kind === "running", "the restarted server");
  });

  await step("sigil.server.enabled stops and starts the server", async () => {
    const config = vscode.workspace.getConfiguration("sigil");
    await config.update("server.enabled", false, vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.status.kind === "disabled", "the server to be disabled");
    const before = server.starts;
    await config.update("server.enabled", undefined, vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.starts === before + 1 && server.status.kind === "running", "the server to run again");
  });

  await step("a sigil.path that doesn't exist is reported, not replaced", async () => {
    const config = vscode.workspace.getConfiguration("sigil");
    const path = config.get<string>("path");
    await config.update("path", join(folder.uri.fsPath, "no-such-sigil"), vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.status.kind === "no-binary", "the missing binary to be reported");
    await config.update("path", path, vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.status.kind === "running", "the server to run again");
  });

  await step("a sigil without a language server is reported as failed", async () => {
    const old = process.env.SIGIL_FAKE_OLD;
    assert.ok(old, "SIGIL_FAKE_OLD is set by run.mjs");
    const config = vscode.workspace.getConfiguration("sigil");
    const path = config.get<string>("path");
    await config.update("path", old, vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.status.kind === "failed", "the failed start to be reported");
    const status = server.status;
    assert.ok(status.kind === "failed" && status.message.includes("sigil lsp didn't start"), JSON.stringify(status));
    await config.update("path", path, vscode.ConfigurationTarget.Workspace);
    await waitFor(() => server.status.kind === "running", "the server to run again");
  });

  await step("the language configuration comments a line with //", async () => {
    const doc = await vscode.workspace.openTextDocument({ language: "sigil", content: "let a = 1\n" });
    const editor = await vscode.window.showTextDocument(doc);
    editor.selection = new vscode.Selection(0, 0, 0, 0);
    await vscode.commands.executeCommand("editor.action.commentLine");
    assert.equal(doc.lineAt(0).text, "// let a = 1");
  });

  await step("the server stops with the window", async () => {
    await server.stop();
    assert.equal(server.status.kind, "stopped");
    await waitFor(() => entries(log).some((e) => e.method === "exit"), "the server to get exit");
  });
  tracing.dispose();
}

// In an untrusted workspace the workspace can't choose the program that runs:
// VS Code drops the workspace's sigil.path, a relative sigil.path from user
// settings is refused, and the extension falls back to PATH.
async function restrictedSuite(): Promise<void> {
  const pathBinary = process.env.SIGIL_FAKE_PATH;
  assert.ok(pathBinary, "SIGIL_FAKE_PATH is set by run.mjs");
  const extension = vscode.extensions.getExtension<Api>(EXTENSION_ID);
  assert.ok(extension, `${EXTENSION_ID} is installed`);

  await step("the extension activates in an untrusted workspace", async () => {
    assert.equal(vscode.workspace.isTrusted, false);
    await waitFor(() => extension.isActive, "the extension to activate on its own");
  });
  const { server } = extension.exports;
  const tracing = trace(server);

  await step("the workspace's sigil.path is ignored, and sigil comes from PATH", async () => {
    await waitFor(() => server.status.kind === "running", "the server to run");
    const status = server.status;
    assert.ok(status.kind === "running", JSON.stringify(status));
    assert.equal(status.binary.source, "path");
    assert.equal(status.binary.path, pathBinary);
  });

  await step("a relative sigil.path in user settings is refused", async () => {
    const config = vscode.workspace.getConfiguration("sigil");
    await config.update("path", "bin/sigil", vscode.ConfigurationTarget.Global);
    await waitFor(() => server.status.kind === "no-binary", "the relative path to be refused");
    const status = server.status;
    assert.ok(status.kind === "no-binary" && status.message.includes("untrusted workspace"), JSON.stringify(status));
    await config.update("path", undefined, vscode.ConfigurationTarget.Global);
    await waitFor(() => server.status.kind === "running", "the server to run again");
  });

  await step("the server stops with the window", async () => {
    await server.stop();
    assert.equal(server.status.kind, "stopped");
  });
  tracing.dispose();
}

/**
 * Records what the extension and VS Code do while the suite runs: the
 * server's status changes, the events that restart it, documents opening and
 * closing and diagnostics changing, with the milliseconds since the suite
 * started. A step that times out in CI then says what happened.
 */
function trace(server: Api["server"]): vscode.Disposable {
  const start = Date.now();
  const say = (what: string) => traced.push(`      [${Date.now() - start} ms] ${what}`);
  say(`server ${server.status.kind}, ${server.starts} starts`);
  return vscode.Disposable.from(
    server.onDidChangeStatus((s) => say(`server ${s.kind}, ${server.starts} starts`)),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("sigil")) say("the sigil settings changed");
    }),
    vscode.workspace.onDidGrantWorkspaceTrust(() => say("workspace trust granted")),
    vscode.workspace.onDidChangeWorkspaceFolders(() => say("workspace folders changed")),
    vscode.workspace.onDidOpenTextDocument((d) => say(`opened ${d.uri.toString()}`)),
    vscode.workspace.onDidCloseTextDocument((d) => say(`closed ${d.uri.toString()}`)),
    vscode.languages.onDidChangeDiagnostics((e) => say(`diagnostics changed for ${e.uris.map(String).join(", ")}`)),
  );
}

interface Entry {
  args?: string[];
  method?: string;
  uri?: string;
}

/** What the fake server has logged so far. */
function entries(log: string): Entry[] {
  let text = "";
  try {
    text = readFileSync(log, "utf8");
  } catch {
    return [];
  }
  return text
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as Entry);
}

async function step(name: string, body: () => Promise<void>): Promise<void> {
  console.log(`  - ${name}`);
  await body();
}

async function waitFor(condition: () => boolean, what: string, timeoutMs = 20_000, everyMs = 50): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, everyMs));
  }
}
