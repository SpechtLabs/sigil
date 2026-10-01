// Shared links: the whole workspace in the URL fragment, compressed with
// deflate-raw and written as base64url. The fragment is a valid CSS id
// (letters, digits, - and _), so the router's scroll to an anchor finds
// nothing instead of failing on it. The fragment never reaches a
// server, so a link is all there is; nothing is stored anywhere.

import { modes, pathProblem, type Workspace } from "./workspace.js";

const PREFIX = "#ws-";
// The version of the format, first in the JSON, so a later playground can
// still read an old link.
const VERSION = 1;
// What a link may hold. A link is input from anyone, so these bound the work
// reading one does: the fragment, what it inflates to, and the files.
const MAX_FRAGMENT = 256 * 1024;
const MAX_JSON = 1024 * 1024;
const MAX_FILES = 200;

/** The fragment of a link that restores the workspace. */
export async function shareFragment(ws: Workspace): Promise<string> {
  const json = JSON.stringify({ v: VERSION, ...ws });
  const packed = await pipe(new TextEncoder().encode(json), new CompressionStream("deflate-raw"));
  return PREFIX + toBase64Url(packed);
}

/** Whether a fragment is a shared workspace, not an ordinary anchor. */
export function isShared(fragment: string): boolean {
  return fragment.startsWith(PREFIX);
}

/** The workspace a shared fragment holds; throws when it can't be read. */
export async function readFragment(fragment: string): Promise<Workspace> {
  if (fragment.length > MAX_FRAGMENT) throw new Error("the link is too long");
  const packed = fromBase64Url(fragment.slice(PREFIX.length));
  const json = new TextDecoder().decode(await inflate(packed));
  return validate(JSON.parse(json));
}

async function pipe(bytes: Uint8Array<ArrayBuffer>, transform: CompressionStream | DecompressionStream): Promise<Uint8Array<ArrayBuffer>> {
  const stream = new Blob([bytes]).stream().pipeThrough(transform);
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

// Inflates a link, stopping once the output passes MAX_JSON, so a small
// fragment can't expand into gigabytes.
async function inflate(bytes: Uint8Array<ArrayBuffer>): Promise<Uint8Array<ArrayBuffer>> {
  const reader = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("deflate-raw")).getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_JSON) {
      await reader.cancel();
      throw new Error("the link's workspace is too large");
    }
    chunks.push(value);
  }
  const out = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) {
    out.set(c, at);
    at += c.byteLength;
  }
  return out;
}

function toBase64Url(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

function fromBase64Url(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replaceAll("-", "+").replaceAll("_", "/");
  return Uint8Array.from(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4)), (c) => c.charCodeAt(0));
}

// A link is input from anyone, so everything in it is checked for shape.
// It only ever becomes text in editors; nothing in it runs.
function validate(v: unknown): Workspace {
  const o = v as Partial<Workspace> & { v?: unknown };
  const isString = (x: unknown): x is string => typeof x === "string";
  if (typeof o !== "object" || o === null || o.v !== VERSION) throw new Error("unknown link format");
  if (!Array.isArray(o.files) || !o.files.every((f) => isString(f?.path) && isString(f?.source))) {
    throw new Error("the link's files are malformed");
  }
  if (o.files.length === 0 || o.files.length > MAX_FILES) throw new Error(`a link holds 1 to ${MAX_FILES} files`);
  // Every path must be one the playground could have made, and unique.
  const files: Workspace["files"] = [];
  for (const f of o.files) {
    const problem = pathProblem(f.path, files);
    if (problem !== undefined) throw new Error(`${f.path}: ${problem}`);
    files.push({ path: f.path, source: f.source });
  }
  return {
    files,
    input: isString(o.input) ? o.input : "",
    stubs: isString(o.stubs) ? o.stubs : "",
    policy: isString(o.policy) ? o.policy : "",
    mode: modes.some((m) => m.id === o.mode) ? (o.mode as Workspace["mode"]) : "evaluate",
  };
}
