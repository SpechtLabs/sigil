// The extension's entry point: starts the language server for the
// workspace, registers the commands, shows the server's state in the
// language status item, and restarts the server when a setting it reads
// changes. Highlighting, the language configuration and the snippets are
// all declared in package.json and need none of this.

import * as vscode from "vscode";
import { describeSource, Server, type Status } from "./server";
import { needsRestart, SECTION } from "./settings";
import { checkForUpdate, fetchLatestRelease, needsUpdateNotice } from "./update-check";

// The globalState keys of the update notice.
const LAST_CHECKED = "sigil.updates.lastChecked";
const SKIPPED = "sigil.updates.skipped";

/** The running extension's server, which deactivate stops. */
let active: Server | undefined;

/** What activate returns: a handle the integration tests inspect the server through. */
export interface Api {
  readonly server: Server;
}

export function activate(context: vscode.ExtensionContext): Api {
  const server = new Server(context);
  active = server;
  context.subscriptions.push(server);

  const status = vscode.languages.createLanguageStatusItem("sigil.server", { language: "sigil" });
  status.name = "Sigil Language Server";
  context.subscriptions.push(status);
  showStatus(status, server.status);
  context.subscriptions.push(server.onDidChangeStatus((s) => showStatus(status, s)));

  context.subscriptions.push(
    vscode.commands.registerCommand("sigil.restartServer", () => server.restart()),
    vscode.commands.registerCommand("sigil.showOutput", () => server.output.show()),
    vscode.commands.registerCommand("sigil.showVersion", () => showVersion(server)),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (needsRestart((section) => e.affectsConfiguration(section))) void server.restart();
    }),
    // Both change what the binary resolves to: trust lets a relative
    // sigil.path and the workspace's own settings count, and the first
    // folder is the server's working directory and what a relative path is
    // relative to.
    vscode.workspace.onDidGrantWorkspaceTrust(() => void server.restart()),
    vscode.workspace.onDidChangeWorkspaceFolders(() => void server.restart()),
  );

  // Not awaited: a server that never answers mustn't hold up activation.
  void server.start();
  void notifyUpdates(context, server.output);
  return { server };
}

/**
 * Stops the server and resolves once sigil lsp has had its shutdown and exit,
 * which VS Code waits for before it disposes context.subscriptions.
 */
export function deactivate(): Promise<void> | undefined {
  const server = active;
  active = undefined;
  return server?.stop();
}

/**
 * Offers a newer release in VS Code, which can't update an extension that
 * isn't in its Marketplace; see update-check.ts. A failed check only logs.
 */
async function notifyUpdates(context: vscode.ExtensionContext, output: vscode.LogOutputChannel): Promise<void> {
  if (!needsUpdateNotice(vscode.env.uriScheme)) return;
  if (!vscode.workspace.getConfiguration(SECTION).get<boolean>("checkForUpdates", true)) return;
  const current = String(context.extension.packageJSON.version);
  try {
    const outcome = await checkForUpdate({
      current,
      now: Date.now(),
      lastChecked: context.globalState.get<number>(LAST_CHECKED),
      skipped: context.globalState.get<string>(SKIPPED),
      setLastChecked: async (when) => context.globalState.update(LAST_CHECKED, when),
      setSkipped: async (version) => context.globalState.update(SKIPPED, version),
      fetchLatest: () => fetchLatestRelease(),
      notify: async (version) => {
        const choice = await vscode.window.showInformationMessage(
          `Sigil ${version} is out; this extension is ${current}. VS Code doesn't update it, because it isn't in the Marketplace: download the VSIX for your platform from the release.`,
          "Download",
          "Skip this version",
        );
        return choice === "Download" ? "download" : choice === "Skip this version" ? "skip" : undefined;
      },
      open: async (url) => {
        await vscode.env.openExternal(vscode.Uri.parse(url));
      },
    });
    output.info(`Update check: ${outcome}`);
  } catch (err) {
    output.warn(`Update check failed: ${err instanceof Error ? err.message : String(err)}`);
  }
}

/** Shows what `sigil version` reports, and which binary said it. */
async function showVersion(server: Server): Promise<void> {
  try {
    const { binary, info } = await server.version();
    // Not awaited: the promise only settles when the notification closes.
    void vscode.window.showInformationMessage(
      `sigil ${info.version} (commit ${info.commit}, ${info.platform}), from ${describeSource(binary)}: ${binary.path}`,
    );
  } catch (err) {
    void vscode.window.showErrorMessage(`sigil version failed: ${err instanceof Error ? err.message : String(err)}`);
  }
}

function showStatus(item: vscode.LanguageStatusItem, s: Status): void {
  item.busy = s.kind === "starting";
  item.severity =
    s.kind === "no-binary" || s.kind === "failed"
      ? vscode.LanguageStatusSeverity.Error
      : vscode.LanguageStatusSeverity.Information;
  item.command = { title: "Show Output", command: "sigil.showOutput" };
  switch (s.kind) {
    case "disabled":
      item.text = "sigil lsp off";
      item.detail = "sigil.server.enabled is false";
      item.command = { title: "Open Settings", command: "workbench.action.openSettings", arguments: ["sigil.server"] };
      break;
    case "no-binary":
      item.text = "sigil not found";
      item.detail = s.message;
      item.command = { title: "Open Settings", command: "workbench.action.openSettings", arguments: ["sigil.path"] };
      break;
    case "starting":
      item.text = "sigil lsp starting";
      item.detail = s.binary.path;
      break;
    case "running":
      item.text = "sigil lsp";
      item.detail = `${s.binary.path} (from ${describeSource(s.binary)})`;
      break;
    case "failed":
      item.text = "sigil lsp failed";
      item.detail = s.message;
      break;
    case "stopped":
      item.text = "sigil lsp stopped";
      item.detail = "";
      item.command = { title: "Restart", command: "sigil.restartServer" };
      break;
  }
}
