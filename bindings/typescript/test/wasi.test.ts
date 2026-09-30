import { describe, expect, test } from "bun:test";

import { createWasi, ERRNO_BADF, ERRNO_INVAL, ERRNO_NOSYS, ERRNO_NOTSUP, ERRNO_SUCCESS, WasiExit } from "../src/wasi.js";

// Every preview1 import Go's runtime and syscall package can link in.
const GO_IMPORTS = [
  "args_get",
  "args_sizes_get",
  "clock_time_get",
  "environ_get",
  "environ_sizes_get",
  "fd_close",
  "fd_fdstat_get",
  "fd_fdstat_set_flags",
  "fd_prestat_dir_name",
  "fd_prestat_get",
  "fd_read",
  "fd_write",
  "path_open",
  "poll_oneoff",
  "proc_exit",
  "random_get",
  "sched_yield",
  "sock_accept",
];

type Fn = (...args: unknown[]) => number;

function setup(options: Parameters<typeof createWasi>[1] = {}) {
  const memory = new WebAssembly.Memory({ initial: 2 });
  const wasi = createWasi(GO_IMPORTS, options);
  wasi.bind(memory);
  const fn = (name: string) => wasi.imports[name] as unknown as Fn;
  const view = () => new DataView(memory.buffer);
  return { memory, fn, view, wasi };
}

describe("createWasi", () => {
  test("provides every requested import, with ENOSYS for the unimplemented ones", () => {
    const { wasi, fn } = setup();
    expect(Object.keys(wasi.imports).sort()).toEqual([...GO_IMPORTS].sort());
    expect(fn("path_open")()).toBe(ERRNO_NOSYS);
    expect(fn("sock_accept")()).toBe(ERRNO_NOSYS);
  });

  test("fd_write gathers iovecs to standard output and error", () => {
    const written: string[] = [];
    const { memory, fn, view } = setup({ write: (fd, text) => written.push(`${fd}:${text}`) });
    const bytes = new Uint8Array(memory.buffer);
    bytes.set(new TextEncoder().encode("hello, "), 100);
    bytes.set(new TextEncoder().encode("wörld"), 200);
    view().setUint32(0, 100, true);
    view().setUint32(4, 7, true);
    view().setUint32(8, 200, true);
    view().setUint32(12, 6, true);
    expect(fn("fd_write")(2, 0, 2, 50)).toBe(ERRNO_SUCCESS);
    expect(view().getUint32(50, true)).toBe(13);
    expect(written).toEqual(["2:hello, ", "2:wörld"]);
    expect(fn("fd_write")(3, 0, 2, 50)).toBe(ERRNO_BADF);
  });

  test("fd_write decodes a character split across writes", () => {
    const written: string[] = [];
    const { memory, fn, view } = setup({ write: (_fd, text) => written.push(text) });
    const euro = new TextEncoder().encode("€"); // three bytes
    new Uint8Array(memory.buffer).set(euro, 100);
    view().setUint32(0, 100, true);
    view().setUint32(4, 1, true);
    view().setUint32(8, 101, true);
    view().setUint32(12, 2, true);
    fn("fd_write")(1, 0, 1, 50);
    fn("fd_write")(1, 8, 1, 50);
    expect(written).toEqual(["€"]);
  });

  test("args are the configured ones, the environment is empty", () => {
    const { memory, fn, view } = setup({ args: ["sigil", "x"] });
    expect(fn("args_sizes_get")(0, 4)).toBe(ERRNO_SUCCESS);
    expect(view().getUint32(0, true)).toBe(2);
    expect(view().getUint32(4, true)).toBe(8);
    expect(fn("args_get")(16, 64)).toBe(ERRNO_SUCCESS);
    expect(view().getUint32(16, true)).toBe(64);
    expect(view().getUint32(20, true)).toBe(70);
    expect(new TextDecoder().decode(new Uint8Array(memory.buffer, 64, 8))).toBe("sigil\0x\0");

    expect(fn("environ_sizes_get")(0, 4)).toBe(ERRNO_SUCCESS);
    expect(view().getUint32(0, true)).toBe(0);
    expect(view().getUint32(4, true)).toBe(0);
    expect(fn("environ_get")(0, 0)).toBe(ERRNO_SUCCESS);
  });

  test("clock_time_get reads the wall and monotonic clocks in nanoseconds", () => {
    const { fn, view } = setup();
    expect(fn("clock_time_get")(0, 0n, 8)).toBe(ERRNO_SUCCESS);
    const wall = Number(view().getBigUint64(8, true)) / 1e6;
    expect(Math.abs(wall - Date.now())).toBeLessThan(1000);
    expect(fn("clock_time_get")(1, 0n, 8)).toBe(ERRNO_SUCCESS);
    const a = view().getBigUint64(8, true);
    fn("clock_time_get")(1, 0n, 8);
    expect(view().getBigUint64(8, true)).toBeGreaterThanOrEqual(a);
    expect(fn("clock_time_get")(7, 0n, 8)).toBe(ERRNO_INVAL);
  });

  test("random_get fills buffers larger than one getRandomValues call", () => {
    const { memory, fn } = setup();
    expect(fn("random_get")(0, 65536 + 100)).toBe(ERRNO_SUCCESS);
    const tail = new Uint8Array(memory.buffer, 65536, 100);
    expect(tail.some((b) => b !== 0)).toBe(true);
  });

  test("standard I/O is a blocking character device; there are no preopens", () => {
    const { memory, fn, view } = setup();
    expect(fn("fd_fdstat_get")(1, 0)).toBe(ERRNO_SUCCESS);
    expect(view().getUint8(0)).toBe(2);
    expect(view().getUint16(2, true)).toBe(0);
    expect(fn("fd_fdstat_get")(3, 0)).toBe(ERRNO_BADF);
    expect(fn("fd_fdstat_set_flags")(1, 4)).toBe(ERRNO_NOTSUP);
    expect(fn("fd_fdstat_set_flags")(5, 4)).toBe(ERRNO_BADF);
    expect(fn("fd_prestat_get")(3, 0)).toBe(ERRNO_BADF);
    expect(fn("fd_close")(3)).toBe(ERRNO_BADF);
    expect(fn("fd_read")(0, 0, 0, 40)).toBe(ERRNO_SUCCESS);
    expect(view().getUint32(40, true)).toBe(0);
    expect(fn("fd_read")(4, 0, 0, 40)).toBe(ERRNO_BADF);
  });

  test("poll_oneoff sleeps for the earliest clock and reports every subscription", () => {
    const { fn, view } = setup();
    const v = view();
    // Two relative monotonic clock subscriptions: 5 ms and 50 ms.
    for (const [i, ns, userdata] of [
      [0, 5_000_000n, 7n],
      [1, 50_000_000n, 9n],
    ] as const) {
      const sub = 1000 + i * 48;
      v.setBigUint64(sub, userdata, true);
      v.setUint8(sub + 8, 0);
      v.setUint32(sub + 16, 1, true);
      v.setBigUint64(sub + 24, ns, true);
    }
    const start = performance.now();
    expect(fn("poll_oneoff")(1000, 2000, 2, 3000)).toBe(ERRNO_SUCCESS);
    const took = performance.now() - start;
    expect(took).toBeGreaterThanOrEqual(4);
    expect(took).toBeLessThan(45);
    expect(view().getUint32(3000, true)).toBe(2);
    expect(view().getBigUint64(2000, true)).toBe(7n);
    expect(view().getBigUint64(2032, true)).toBe(9n);
    expect(view().getUint16(2008, true)).toBe(0);
    expect(fn("poll_oneoff")(1000, 2000, 0, 3000)).toBe(ERRNO_INVAL);
  });

  test("poll_oneoff doesn't sleep for an absolute time in the past", () => {
    const { fn, view } = setup();
    const v = view();
    v.setUint8(1008, 0);
    v.setUint32(1016, 0, true);
    v.setBigUint64(1024, 1n, true); // 1 ns after the epoch
    v.setUint16(1040, 1, true);
    const start = performance.now();
    expect(fn("poll_oneoff")(1000, 2000, 1, 3000)).toBe(ERRNO_SUCCESS);
    expect(performance.now() - start).toBeLessThan(20);
  });

  test("proc_exit throws WasiExit", () => {
    const { fn } = setup();
    expect(() => fn("proc_exit")(3)).toThrow(new WasiExit(3));
    expect(fn("sched_yield")()).toBe(ERRNO_SUCCESS);
  });

  test("using the imports before bind fails loudly", () => {
    const wasi = createWasi(["clock_time_get"]);
    expect(() => (wasi.imports["clock_time_get"] as unknown as Fn)(0, 0n, 0)).toThrow("before bind");
  });
});
