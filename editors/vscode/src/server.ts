// The language server's lifecycle: find the binary, start `sigil lsp
// --stdio` under a language client, and stop or restart it when a command
// or a setting asks. One server serves every workspace folder.

import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { promisify } from "node:util";
import * as vscode from "vscode";
import { LanguageClient, type LanguageClientOptions, type ServerOptions, State } from "vscode-languageclient/node";
import { ensureBundledExecutable, type Found, type Resolution, resolveBinary } from "./binary";
import { INSTALL_URL, readSettings, resolutionError, SECTION } from "./settings";
import { withTimeout } from "./timeout";

const run = promisify(execFile);

/** How long sigil lsp has to answer initialize before the start counts as failed. */
const START_TIMEOUT_MS = 30_000;

/** How long a client gets to stop or dispose before it's abandoned. */
const STOP_TIMEOUT_MS = 5_000;

/** The files the server hears about when they change on disk: sources and the configuration file. */
const WATCHED = "**/{*.sigil,sigil.yaml,sigil.json,sigil.toml,.sigil.yaml,.sigil.json,.sigil.toml}";

/** What `sigil version -o json` prints, as far as the extension reads it. */
export interface VersionInfo {
  version: string;
  commit: string;
  platform: string;
}

/** What the server is doing, for the language status item and the tests. */
export type Status =
  | { kind: "disabled" }
  | { kind: "no-binary"; message: string }
  | { kind: "starting"; binary: Found }
  | { kind: "running"; binary: Found }
  | { kind: "failed"; binary: Found; message: string }
  | { kind: "stopped" };

/** Runs the language server and keeps it in step with the settings. */
export class Server implements vscode.Disposable {
  /** Where the client and the server's stderr write. It outlives every restart. */
  readonly output: vscode.LogOutputChannel;
  private readonly trace: vscode.LogOutputChannel;
  private readonly statusEmitter = new vscode.EventEmitter<Status>();
  /** Fires on every change of status. */
  readonly onDidChangeStatus = this.statusEmitter.event;

  private client: LanguageClient | undefined;
  /** The running client's file watcher, disposed with it. */
  private watcher: vscode.FileSystemWatcher | undefined;
  private current: Status = { kind: "stopped" };
  /** How many times a client has been started, for the tests. */
  starts = 0;
  /** Serializes start, stop and restart, so a burst of setting changes can't race. */
  private queue: Promise<void> = Promise.resolve();

  constructor(private readonly context: vscode.ExtensionContext) {
    this.output = vscode.window.createOutputChannel("Sigil Language Server", { log: true });
    this.trace = vscode.window.createOutputChannel("Sigil Language Server Trace", { log: true });
  }

  /** The server's status. */
  get status(): Status {
    return this.current;
  }

  /** Starts the server, unless it's disabled or there's no binary to run. */
  start(): Promise<void> {
    return this.enqueue(() => this.doStart());
  }

  /** Stops the server and starts it again with the current settings. */
  restart(): Promise<void> {
    return this.enqueue(async () => {
      await this.doStop();
      await this.doStart();
    });
  }

  /** Stops the server. */
  stop(): Promise<void> {
    return this.enqueue(() => this.doStop());
  }

  /** Finds the binary the current settings select. */
  async resolve(): Promise<Resolution> {
    const settings = readSettings((key, fallback) => vscode.workspace.getConfiguration(SECTION).get(key, fallback));
    await ensureBundledExecutable(this.context.extensionPath, process.platform);
    return resolveBinary({
      setting: settings.path,
      extensionPath: this.context.extensionPath,
      workspaceFolder: vscode.workspace.workspaceFolders?.[0]?.uri.fsPath,
      workspaceTrusted: vscode.workspace.isTrusted,
      home: homedir(),
      platform: process.platform,
      env: process.env,
    });
  }

  /** Runs `sigil version -o json` with the binary the settings select. */
  async version(): Promise<{ binary: Found; info: VersionInfo }> {
    const res = await this.resolve();
    if (res.kind !== "found") throw new Error(resolutionError(res));
    const { stdout } = await run(res.path, ["version", "-o", "json"], { timeout: 10_000 });
    return { binary: res, info: JSON.parse(stdout) as VersionInfo };
  }

  /** Stops the server, then closes the output channels it writes to. */
  dispose(): void {
    void this.stop().finally(() => {
      this.statusEmitter.dispose();
      this.output.dispose();
      this.trace.dispose();
    });
  }

  private enqueue(step: () => Promise<void>): Promise<void> {
    this.queue = this.queue.then(step, step);
    return this.queue;
  }

  private setStatus(status: Status): void {
    this.current = status;
    this.statusEmitter.fire(status);
  }

  private async doStart(): Promise<void> {
    const settings = readSettings((key, fallback) => vscode.workspace.getConfiguration(SECTION).get(key, fallback));
    if (!settings.serverEnabled) {
      this.setStatus({ kind: "disabled" });
      return;
    }

    const res = await this.resolve();
    if (res.kind !== "found") {
      const message = resolutionError(res) ?? "";
      this.output.error(message);
      this.setStatus({ kind: "no-binary", message });
      void this.reportMissing(res, message);
      return;
    }

    this.output.info(`Starting ${res.path} lsp --stdio (from ${describeSource(res)})`);
    this.setStatus({ kind: "starting", binary: res });
    const client = new LanguageClient("sigil", "Sigil Language Server", serverOptions(res), this.clientOptions());
    this.client = client;
    this.starts++;
    client.onDidChangeState((e) => {
      if (this.client !== client) return;
      if (e.newState === State.Running) this.setStatus({ kind: "running", binary: res });
      else if (e.newState === State.Stopped && this.current.kind === "running") {
        this.setStatus({ kind: "failed", binary: res, message: "sigil lsp stopped" });
      }
    });

    try {
      // A start that never settles would block the queue, and every later
      // restart, forever.
      await withTimeout(
        client.start(),
        START_TIMEOUT_MS,
        `sigil lsp didn't answer within ${START_TIMEOUT_MS / 1000} s`,
      );
    } catch (err) {
      // A sigil from before the language server exits at once, which is
      // the likeliest cause; the output channel has what it printed.
      const message = `sigil lsp didn't start (${res.path}): ${err instanceof Error ? err.message : String(err)}`;
      this.output.error(message);
      this.client = undefined;
      this.watcher?.dispose();
      this.watcher = undefined;
      await withTimeout(client.dispose(STOP_TIMEOUT_MS), 2 * STOP_TIMEOUT_MS, "dispose").catch(() => undefined);
      this.setStatus({ kind: "failed", binary: res, message });
      void vscode.window
        .showErrorMessage(
          `${message}. A sigil older than the language server can't run it; check with "Sigil: Show sigil Version".`,
          "Show Output",
        )
        .then((choice) => {
          if (choice === "Show Output") this.output.show();
        });
    }
  }

  private async doStop(): Promise<void> {
    const client = this.client;
    this.client = undefined;
    if (client !== undefined) {
      try {
        await client.stop(STOP_TIMEOUT_MS);
      } catch (err) {
        this.output.warn(`Stopping sigil lsp: ${err instanceof Error ? err.message : String(err)}`);
      }
      await withTimeout(client.dispose(STOP_TIMEOUT_MS), 2 * STOP_TIMEOUT_MS, "dispose").catch(() => undefined);
    }
    this.watcher?.dispose();
    this.watcher = undefined;
    this.setStatus({ kind: "stopped" });
  }

  private clientOptions(): LanguageClientOptions {
    const watcher = vscode.workspace.createFileSystemWatcher(WATCHED);
    this.watcher = watcher;
    return {
      // The server only reads files on disk: an untitled document would
      // answer every request with an error.
      documentSelector: [{ scheme: "file", language: "sigil" }],
      synchronize: { fileEvents: watcher },
      outputChannel: this.output,
      traceOutputChannel: this.trace,
    };
  }

  private async reportMissing(res: Resolution, message: string): Promise<void> {
    const actions =
      res.kind === "missing"
        ? ["Install sigil", "Open Settings"]
        : res.kind === "bad-setting" && res.reason === "untrusted"
          ? ["Manage Workspace Trust", "Open Settings"]
          : ["Open Settings"];
    const choice = await vscode.window.showErrorMessage(message, ...actions);
    if (choice === "Manage Workspace Trust") await vscode.commands.executeCommand("workbench.trust.manage");
    if (choice === "Install sigil") await vscode.env.openExternal(vscode.Uri.parse(INSTALL_URL));
    if (choice === "Open Settings") await vscode.commands.executeCommand("workbench.action.openSettings", "sigil.path");
  }
}

/** Where a binary came from, in words. */
export function describeSource(binary: Found): string {
  switch (binary.source) {
    case "setting":
      return "sigil.path";
    case "bundled":
      return "the extension's bundled binary";
    case "path":
      return "PATH";
  }
}

/** Runs the binary as `sigil lsp --stdio`, in the first workspace folder. */
function serverOptions(binary: Found): ServerOptions {
  const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  // No transport: the client would append --stdio again for TransportKind.stdio.
  return {
    command: binary.path,
    args: ["lsp", "--stdio"],
    ...(cwd === undefined ? {} : { options: { cwd } }),
  };
}
