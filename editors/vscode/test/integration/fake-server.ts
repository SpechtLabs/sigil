// A stand-in for the sigil binary, so the integration tests exercise the
// client without a language server: `version -o json` prints a version, and
// `lsp --stdio` speaks just enough LSP to start, register file watchers the
// way sigil lsp does, publish one diagnostic per opened document and shut
// down. Every message it reads, and its arguments, go to the JSON lines file
// SIGIL_FAKE_LOG names, which the tests read.

import { appendFileSync } from "node:fs";

const log = process.env.SIGIL_FAKE_LOG;
const args = process.argv.slice(2);

function record(entry: unknown): void {
  if (log !== undefined) appendFileSync(log, `${JSON.stringify(entry)}\n`);
}

function send(message: unknown): void {
  const body = JSON.stringify({ jsonrpc: "2.0", ...(message as object) });
  process.stdout.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
}

interface Message {
  id?: number | string;
  method?: string;
  params?: { textDocument?: { uri: string } };
}

function handle(msg: Message): void {
  record({ method: msg.method ?? "response" });
  switch (msg.method) {
    case "initialize":
      send({
        id: msg.id,
        result: { capabilities: { textDocumentSync: 1 }, serverInfo: { name: "fake-sigil", version: "0.0.0-fake" } },
      });
      break;
    case "initialized":
      // Like sigil lsp: the server registers the watchers, the client has none.
      send({
        id: "register-watchers",
        method: "client/registerCapability",
        params: {
          registrations: [
            {
              id: "sigil-watched-files",
              method: "workspace/didChangeWatchedFiles",
              registerOptions: {
                watchers: [{ globPattern: "**/*.sigil" }, { globPattern: "**/{sigil,.sigil}.{yaml,json,toml}" }],
              },
            },
          ],
        },
      });
      break;
    case "textDocument/didOpen": {
      const uri = msg.params?.textDocument?.uri;
      send({
        method: "textDocument/publishDiagnostics",
        params: {
          uri,
          diagnostics: [
            {
              range: { start: { line: 0, character: 0 }, end: { line: 0, character: 6 } },
              severity: 3,
              source: "fake-sigil",
              message: "fake diagnostic",
            },
          ],
        },
      });
      break;
    }
    case "shutdown":
      send({ id: msg.id, result: null });
      break;
    case "exit":
      process.exit(0);
      break;
    default:
      // A request the fake doesn't know gets an empty result, so the client
      // never waits on it; notifications need no answer.
      if (msg.id !== undefined && msg.method !== undefined) send({ id: msg.id, result: null });
  }
}

function serve(): void {
  let buffer = Buffer.alloc(0);
  process.stdin.on("data", (chunk: Buffer) => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      const headerEnd = buffer.indexOf("\r\n\r\n");
      if (headerEnd < 0) return;
      const length = Number(/Content-Length: (\d+)/i.exec(buffer.subarray(0, headerEnd).toString())?.[1]);
      const start = headerEnd + 4;
      if (buffer.length < start + length) return;
      const body = buffer.subarray(start, start + length).toString();
      buffer = buffer.subarray(start + length);
      handle(JSON.parse(body) as Message);
    }
  });
}

record({ args });
if (args[0] === "version") {
  process.stdout.write(
    `${JSON.stringify({ version: "0.0.0-fake", commit: "fake", commitTime: "unknown", dirty: false, goVersion: "go1.27.1", platform: `${process.platform}/${process.arch}` })}\n`,
  );
} else if (args[0] === "lsp" && args[1] === "--stdio") {
  serve();
} else {
  process.stderr.write(`fake sigil: unexpected arguments ${JSON.stringify(args)}\n`);
  process.exit(2);
}
