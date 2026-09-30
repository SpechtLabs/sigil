// A hand-assembled stand-in for sigil.wasm with the same imports and
// exports, for testing the ABI glue without the Go build. Its sigil_call
// forwards the request to host_call and returns the host's answer, so a
// test drives both directions through one call. It counts allocations and
// frees in exported globals.

export interface FakeOptions {
  /** What sigil_abi_version returns. */
  abi?: number;
  /** _initialize writes "panic: boom" to standard error and exits with code 2. */
  crashOnInit?: boolean;
  /** sigil_call traps. */
  trapOnCall?: boolean;
  /** sigil_call recurses until the engine runs out of stack, which throws a RangeError. */
  recurseOnCall?: boolean;
  /** Leaves out the sigil_call export. */
  omitCall?: boolean;
  /** Adds an import the package doesn't provide. */
  extraImport?: boolean;
}

const I32 = 0x7f;
const I64 = 0x7e;
const PANIC = new TextEncoder().encode("panic: boom\n");

export function fakeModule(options: FakeOptions = {}): Uint8Array<ArrayBuffer> {
  const types = [
    functype([I32, I32], [I64]), // 0: host_call, sigil_call
    functype([I32], []), // 1: proc_exit
    functype([I32, I32, I32, I32], [I32]), // 2: fd_write
    functype([], [I32]), // 3: sigil_abi_version
    functype([I32], [I32]), // 4: sigil_alloc
    functype([I32, I32], []), // 5: sigil_free
    functype([], []), // 6: _initialize
  ];
  const imports = [
    importFunc("sigil", "host_call", 0),
    importFunc("wasi_snapshot_preview1", "proc_exit", 1),
    importFunc("wasi_snapshot_preview1", "fd_write", 2),
    ...(options.extraImport ? [importFunc("env", "surprise", 6)] : []),
  ];
  const n = imports.length; // index of the first defined function
  const bodies = [
    // sigil_abi_version
    [0x41, ...sleb(options.abi ?? 1)],
    // sigil_alloc: return the bump pointer, advance it, count the call
    [0x23, 0, 0x23, 0, 0x20, 0, 0x6a, 0x24, 0, 0x23, 2, 0x41, 1, 0x6a, 0x24, 2],
    // sigil_free: count the call
    [0x23, 1, 0x41, 1, 0x6a, 0x24, 1],
    // sigil_call: forward to host_call
    options.trapOnCall ? [0x00] : options.recurseOnCall ? [0x20, 0, 0x20, 1, 0x10, ...uleb(n + 3)] : [0x20, 0, 0x20, 1, 0x10, 0],
    // _initialize
    options.crashOnInit
      ? [0x41, 2, 0x41, 16, 0x41, 1, 0x41, 8, 0x10, 2, 0x1a, 0x41, 2, 0x10, 1]
      : [],
  ];
  const exports = [
    exportEntry("memory", 2, 0),
    exportEntry("sigil_abi_version", 0, n),
    exportEntry("sigil_alloc", 0, n + 1),
    exportEntry("sigil_free", 0, n + 2),
    ...(options.omitCall ? [] : [exportEntry("sigil_call", 0, n + 3)]),
    exportEntry("_initialize", 0, n + 4),
    exportEntry("frees", 3, 1),
    exportEntry("allocs", 3, 2),
  ];
  // An iovec at 16 pointing at the panic text at 32.
  const iovec = [...u32le(32), ...u32le(PANIC.length)];
  return new Uint8Array([
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
    ...section(1, vec(types)),
    ...section(2, vec(imports)),
    ...section(3, vec([[3], [4], [5], [0], [6]])),
    ...section(5, vec([[0x00, 1]])),
    ...section(6, vec([global(1024), global(0), global(0)])),
    ...section(7, vec(exports)),
    ...section(10, vec(bodies.map((b) => bytes([0x00, ...b, 0x0b])))),
    ...section(11, vec([[0x00, 0x41, 16, 0x0b, ...bytes(iovec)], [0x00, 0x41, 32, 0x0b, ...bytes([...PANIC])]])),
  ]);
}

function functype(params: number[], results: number[]): number[] {
  return [0x60, ...vec(params.map((p) => [p])), ...vec(results.map((r) => [r]))];
}

function importFunc(module: string, name: string, type: number): number[] {
  return [...name_(module), ...name_(name), 0x00, ...uleb(type)];
}

function exportEntry(name: string, kind: number, index: number): number[] {
  return [...name_(name), kind, ...uleb(index)];
}

function global(init: number): number[] {
  return [I32, 0x01, 0x41, ...sleb(init), 0x0b];
}

function section(id: number, content: number[]): number[] {
  return [id, ...bytes(content)];
}

function vec(items: number[][]): number[] {
  return [...uleb(items.length), ...items.flat()];
}

function bytes(b: number[]): number[] {
  return [...uleb(b.length), ...b];
}

function name_(s: string): number[] {
  return bytes([...new TextEncoder().encode(s)]);
}

function u32le(v: number): number[] {
  return [v & 0xff, (v >> 8) & 0xff, (v >> 16) & 0xff, (v >>> 24) & 0xff];
}

function uleb(v: number): number[] {
  const out: number[] = [];
  do {
    let b = v & 0x7f;
    v >>>= 7;
    if (v !== 0) b |= 0x80;
    out.push(b);
  } while (v !== 0);
  return out;
}

function sleb(v: number): number[] {
  const out: number[] = [];
  for (;;) {
    const b = v & 0x7f;
    v >>= 7;
    if ((v === 0 && (b & 0x40) === 0) || (v === -1 && (b & 0x40) !== 0)) {
      out.push(b);
      return out;
    }
    out.push(b | 0x80);
  }
}
