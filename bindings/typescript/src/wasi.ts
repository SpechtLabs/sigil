// A WASI preview1 shim with just what a Go reactor calls: no filesystem, no
// network, no environment. Standard output and error go to a callback,
// clocks and randomness come from the platform, and every other file
// descriptor call fails with EBADF. The module never touches files (its
// inputs arrive as virtual files in requests), so this is the whole surface
// it needs; any import Go links in beyond it answers ENOSYS instead of
// failing instantiation.

/** The WASI module name imports live under. */
export const WASI_MODULE = "wasi_snapshot_preview1";

// errno values from the preview1 spec.
export const ERRNO_SUCCESS = 0;
export const ERRNO_BADF = 8;
export const ERRNO_INVAL = 28;
export const ERRNO_NOSYS = 52;
export const ERRNO_NOTSUP = 58;

const CLOCK_REALTIME = 0;
const CLOCK_MONOTONIC = 1;
const FILETYPE_CHARACTER_DEVICE = 2;
const EVENTTYPE_CLOCK = 0;
const SUBCLOCKFLAG_ABSTIME = 1;
const SUBSCRIPTION_SIZE = 48;
const EVENT_SIZE = 32;
// crypto.getRandomValues refuses more than 65536 bytes per call.
const RANDOM_CHUNK = 65536;

/** Thrown by `proc_exit`: the module has stopped and can't be called again. */
export class WasiExit extends Error {
  override readonly name: string = "WasiExit";
  readonly code: number;

  constructor(code: number) {
    super(`the module exited with code ${code}`);
    this.code = code;
  }
}

export interface WasiOptions {
  /** Receives what the module writes to standard output (1) and error (2). */
  write?: (fd: 1 | 2, text: string) => void;
  /** The module's `os.Args`. */
  args?: string[];
}

/** The imports for one module instance, bound to its memory once it exists. */
export interface Wasi {
  /** The import functions by name, for the `wasi_snapshot_preview1` namespace. */
  imports: Record<string, (...args: never[]) => number | bigint | void>;
  /** Gives the shim the instance's memory; call before the first export. */
  bind(memory: WebAssembly.Memory): void;
}

/**
 * Creates the imports for one instance. `names` lists every import the
 * module declares under {@link WASI_MODULE}; the ones this shim doesn't
 * implement answer ENOSYS.
 */
export function createWasi(names: Iterable<string>, options: WasiOptions = {}): Wasi {
  let memory: WebAssembly.Memory | undefined;
  const encoder = new TextEncoder();
  const decoders = { 1: new TextDecoder(), 2: new TextDecoder() };
  const write = options.write ?? (() => {});
  const args = (options.args ?? ["sigil"]).map((a) => encoder.encode(`${a}\0`));

  // The buffer detaches whenever memory grows, so every access takes a
  // fresh view.
  const view = (): DataView => {
    if (memory === undefined) throw new Error("WASI shim used before bind()");
    return new DataView(memory.buffer);
  };
  const bytes = (ptr: number, len: number): Uint8Array<ArrayBuffer> => {
    if (memory === undefined) throw new Error("WASI shim used before bind()");
    return new Uint8Array(memory.buffer, ptr >>> 0, len >>> 0);
  };

  const implemented = {
    args_sizes_get(countPtr: number, sizePtr: number): number {
      const v = view();
      v.setUint32(countPtr >>> 0, args.length, true);
      v.setUint32(sizePtr >>> 0, args.reduce((n, a) => n + a.length, 0), true);
      return ERRNO_SUCCESS;
    },
    args_get(argvPtr: number, bufPtr: number): number {
      let buf = bufPtr >>> 0;
      args.forEach((a, i) => {
        view().setUint32((argvPtr >>> 0) + i * 4, buf, true);
        bytes(buf, a.length).set(a);
        buf += a.length;
      });
      return ERRNO_SUCCESS;
    },
    environ_sizes_get(countPtr: number, sizePtr: number): number {
      const v = view();
      v.setUint32(countPtr >>> 0, 0, true);
      v.setUint32(sizePtr >>> 0, 0, true);
      return ERRNO_SUCCESS;
    },
    environ_get(): number {
      return ERRNO_SUCCESS;
    },
    clock_res_get(id: number, resPtr: number): number {
      if (id !== CLOCK_REALTIME && id !== CLOCK_MONOTONIC) return ERRNO_INVAL;
      view().setBigUint64(resPtr >>> 0, 1000n, true);
      return ERRNO_SUCCESS;
    },
    clock_time_get(id: number, _precision: bigint, timePtr: number): number {
      if (id !== CLOCK_REALTIME && id !== CLOCK_MONOTONIC) return ERRNO_INVAL;
      view().setBigUint64(timePtr >>> 0, now(id), true);
      return ERRNO_SUCCESS;
    },
    random_get(ptr: number, len: number): number {
      for (let off = 0; off < len >>> 0; off += RANDOM_CHUNK) {
        crypto.getRandomValues(bytes((ptr >>> 0) + off, Math.min(RANDOM_CHUNK, (len >>> 0) - off)));
      }
      return ERRNO_SUCCESS;
    },
    fd_write(fd: number, iovsPtr: number, iovsLen: number, nwrittenPtr: number): number {
      if (fd !== 1 && fd !== 2) return ERRNO_BADF;
      let n = 0;
      for (let i = 0; i < iovsLen; i++) {
        const v = view();
        const ptr = v.getUint32((iovsPtr >>> 0) + i * 8, true);
        const len = v.getUint32((iovsPtr >>> 0) + i * 8 + 4, true);
        const text = decoders[fd].decode(bytes(ptr, len), { stream: true });
        if (text !== "") write(fd, text);
        n += len;
      }
      view().setUint32(nwrittenPtr >>> 0, n, true);
      return ERRNO_SUCCESS;
    },
    fd_read(fd: number, _iovsPtr: number, _iovsLen: number, nreadPtr: number): number {
      if (fd !== 0) return ERRNO_BADF;
      view().setUint32(nreadPtr >>> 0, 0, true); // standard input is always at its end
      return ERRNO_SUCCESS;
    },
    fd_fdstat_get(fd: number, statPtr: number): number {
      if (fd < 0 || fd > 2) return ERRNO_BADF;
      // fdstat: filetype u8, flags u16 at 2, rights_base u64 at 8,
      // rights_inheriting u64 at 16.
      const v = view();
      const p = statPtr >>> 0;
      bytes(p, 24).fill(0);
      v.setUint8(p, FILETYPE_CHARACTER_DEVICE);
      v.setBigUint64(p + 8, 0xffff_ffff_ffff_ffffn, true);
      return ERRNO_SUCCESS;
    },
    // Go asks for non-blocking standard I/O at startup; staying blocking
    // keeps every write a direct fd_write.
    fd_fdstat_set_flags(fd: number): number {
      return fd >= 0 && fd <= 2 ? ERRNO_NOTSUP : ERRNO_BADF;
    },
    // EBADF from fd 3 on tells Go there are no preopened directories.
    fd_prestat_get: badf,
    fd_prestat_dir_name: badf,
    fd_close: badf,
    fd_seek: badf,
    fd_sync: badf,
    fd_pread: badf,
    fd_pwrite: badf,
    fd_readdir: badf,
    fd_filestat_get: badf,
    fd_filestat_set_size: badf,
    fd_fdstat_set_rights: badf,
    poll_oneoff(inPtr: number, outPtr: number, nsubs: number, neventsPtr: number): number {
      if (nsubs === 0) return ERRNO_INVAL;
      // Wait for the earliest clock, then report every subscription as
      // ready: standard I/O never blocks here, and a Go reactor only polls
      // to sleep.
      let wait = 0n;
      const current = now(CLOCK_MONOTONIC);
      for (let i = 0; i < nsubs; i++) {
        const v = view();
        const sub = (inPtr >>> 0) + i * SUBSCRIPTION_SIZE;
        if (v.getUint8(sub + 8) !== EVENTTYPE_CLOCK) continue;
        const id = v.getUint32(sub + 16, true);
        let timeout = v.getBigUint64(sub + 24, true);
        if ((v.getUint16(sub + 40, true) & SUBCLOCKFLAG_ABSTIME) !== 0) {
          timeout -= id === CLOCK_REALTIME ? now(CLOCK_REALTIME) : current;
          if (timeout < 0n) timeout = 0n;
        }
        if (i === 0 || timeout < wait) wait = timeout;
      }
      sleep(Number(wait) / 1e6);
      for (let i = 0; i < nsubs; i++) {
        const v = view();
        const sub = (inPtr >>> 0) + i * SUBSCRIPTION_SIZE;
        const ev = (outPtr >>> 0) + i * EVENT_SIZE;
        bytes(ev, EVENT_SIZE).fill(0);
        v.setBigUint64(ev, v.getBigUint64(sub, true), true); // userdata
        v.setUint8(ev + 10, v.getUint8(sub + 8)); // type
      }
      view().setUint32(neventsPtr >>> 0, nsubs, true);
      return ERRNO_SUCCESS;
    },
    sched_yield(): number {
      return ERRNO_SUCCESS;
    },
    proc_exit(code: number): never {
      throw new WasiExit(code);
    },
  };

  const imports: Wasi["imports"] = {};
  for (const name of names) {
    imports[name] = Object.hasOwn(implemented, name) ? implemented[name as keyof typeof implemented] : nosys;
  }
  return {
    imports,
    bind(m) {
      memory = m;
    },
  };
}

function badf(): number {
  return ERRNO_BADF;
}

function nosys(): number {
  return ERRNO_NOSYS;
}

/** The clock's time in nanoseconds. */
function now(id: number): bigint {
  const ms = id === CLOCK_REALTIME ? performance.timeOrigin + performance.now() : performance.now();
  return BigInt(Math.round(ms * 1e6));
}

let sleepCell: Int32Array | null | undefined;

/**
 * Blocks for ms milliseconds: with Atomics.wait where the platform allows
 * it (workers, Node, Bun, Deno), spinning where it doesn't (a browser's main
 * thread). Go only sleeps here for scheduler back-off, microseconds long.
 */
function sleep(ms: number): void {
  if (ms <= 0) return;
  if (sleepCell === undefined) {
    try {
      sleepCell = new Int32Array(new SharedArrayBuffer(4));
      Atomics.wait(sleepCell, 0, 0, 0);
    } catch {
      sleepCell = null;
    }
  }
  if (sleepCell !== null) {
    Atomics.wait(sleepCell, 0, 0, ms);
    return;
  }
  const end = performance.now() + ms;
  while (performance.now() < end) {
    // spin
  }
}
