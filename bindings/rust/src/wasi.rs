//! WASI preview 1, just what a Go reactor calls: no filesystem, no network, no
//! environment. Standard error is captured (a stopped instance's error quotes
//! it), standard output is discarded, the clocks and randomness come from the
//! platform, and every file descriptor call fails with the errno Go expects.
//! The module never touches files (its inputs arrive as virtual files in
//! requests), so this is the whole surface it needs.
//!
//! It is the twin of `bindings/typescript/src/wasi.ts`, written directly on
//! `Linker::func_wrap` instead of wasmtime-wasi, which drags in an async
//! runtime that panics when a synchronous host calls it from inside one.
//!
//! The module's WASI imports are the ones in [`IMPORTS`]; an import the module
//! links beyond them fails when the module loads, not when the call that needs
//! it runs.

use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use wasmtime::{Caller, Extern, Linker, Memory};

use crate::runtime::State;

/// The import namespace of WASI preview 1.
pub(crate) const MODULE: &str = "wasi_snapshot_preview1";

/// The imports of the module, which [`add_to_linker`] implements.
pub(crate) const IMPORTS: &[&str] = &[
    "args_get",
    "args_sizes_get",
    "clock_time_get",
    "environ_get",
    "environ_sizes_get",
    "fd_close",
    "fd_fdstat_get",
    "fd_filestat_get",
    "fd_fdstat_set_flags",
    "fd_prestat_dir_name",
    "fd_prestat_get",
    "fd_read",
    "fd_write",
    "path_filestat_get",
    "path_open",
    "poll_oneoff",
    "proc_exit",
    "random_get",
    "sched_yield",
];

/// How much of the module's standard error a stopped instance's error quotes:
/// its first bytes, and its last.
const STDERR_HEAD: usize = 1024;
const STDERR_TAIL: usize = 3072;

// errno values from the preview 1 spec.
const SUCCESS: i32 = 0;
const BADF: i32 = 8;
const FAULT: i32 = 21;
const INVAL: i32 = 28;
const NOSYS: i32 = 52;
const NOTSUP: i32 = 58;

const CLOCK_REALTIME: i32 = 0;
const CLOCK_MONOTONIC: i32 = 1;
const FILETYPE_CHARACTER_DEVICE: u8 = 2;
const EVENTTYPE_CLOCK: u8 = 0;
const SUBCLOCKFLAG_ABSTIME: u16 = 1;
const SUBSCRIPTION_SIZE: u32 = 48;
const EVENT_SIZE: u32 = 32;
/// The most iovecs or subscriptions one call may carry. Go sends a handful; a
/// larger count from the guest is refused instead of looped over.
const MAX_ITEMS: u32 = 1024;
/// The longest `poll_oneoff` sleeps in one go. Go only sleeps there for
/// scheduler back-off, microseconds long; a longer request is answered early
/// (Go looks at its timers again and asks again), so a hard deadline's epoch
/// check gets to run.
const MAX_SLEEP: Duration = Duration::from_millis(50);
/// `os.Args` of the module.
const ARGS: &[u8] = b"sigil\0";

/// `proc_exit`: the module has stopped and can't be called again.
#[derive(Debug)]
pub(crate) struct Exit(pub i32);

impl std::fmt::Display for Exit {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "exit status {}", self.0)
    }
}

impl std::error::Error for Exit {}

/// What the module wrote to standard error, which the Go runtime does when
/// it's badly wrong: a stopped instance's error quotes it. The first lines (the
/// fatal error itself) and the last (where it happened) are kept; the middle of
/// a long goroutine dump is not.
#[derive(Clone, Default)]
pub(crate) struct Stderr(Arc<Mutex<StderrBuf>>);

#[derive(Default)]
struct StderrBuf {
    head: Vec<u8>,
    tail: Vec<u8>,
    elided: bool,
}

impl Stderr {
    pub(crate) fn write(&self, bytes: &[u8]) {
        let mut kept = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let room = STDERR_HEAD.saturating_sub(kept.head.len());
        let (head, rest) = bytes.split_at(room.min(bytes.len()));
        kept.head.extend_from_slice(head);
        kept.tail.extend_from_slice(rest);
        let excess = kept.tail.len().saturating_sub(STDERR_TAIL);
        if excess > 0 {
            kept.tail.drain(..excess);
            kept.elided = true;
        }
    }

    pub(crate) fn contents(&self) -> Vec<u8> {
        let buf = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let mut out = buf.head.clone();
        if buf.elided {
            out.extend_from_slice(b"\n[...]\n");
        }
        out.extend_from_slice(&buf.tail);
        out
    }
}

type Ctx<'a> = Caller<'a, State>;

/// Declares the module's WASI imports.
pub(crate) fn add_to_linker(linker: &mut Linker<State>) -> wasmtime::Result<()> {
    linker.func_wrap(MODULE, "args_sizes_get", |mut c: Ctx<'_>, count: u32, size: u32| {
        let Some(mem) = memory(&mut c) else { return FAULT };
        let data = mem.data_mut(&mut c);
        done(put_u32(data, count, 1).and(put_u32(data, size, ARGS.len() as u32)))
    })?;
    linker.func_wrap(MODULE, "args_get", |mut c: Ctx<'_>, argv: u32, buf: u32| {
        let Some(mem) = memory(&mut c) else { return FAULT };
        let data = mem.data_mut(&mut c);
        done(put_u32(data, argv, buf).and(range(data, buf, ARGS.len() as u32).map(|dst| dst.copy_from_slice(ARGS))))
    })?;
    linker.func_wrap(MODULE, "environ_sizes_get", |mut c: Ctx<'_>, count: u32, size: u32| {
        let Some(mem) = memory(&mut c) else { return FAULT };
        let data = mem.data_mut(&mut c);
        done(put_u32(data, count, 0).and(put_u32(data, size, 0)))
    })?;
    linker.func_wrap(MODULE, "environ_get", |_: Ctx<'_>, _: u32, _: u32| SUCCESS)?;

    linker.func_wrap(MODULE, "clock_time_get", |mut c: Ctx<'_>, id: i32, _precision: i64, out: u32| {
        let Some(nanos) = now(c.data(), id) else { return INVAL };
        let Some(mem) = memory(&mut c) else { return FAULT };
        done(put_u64(mem.data_mut(&mut c), out, nanos))
    })?;
    linker.func_wrap(MODULE, "random_get", |mut c: Ctx<'_>, ptr: u32, len: u32| {
        let Some(mem) = memory(&mut c) else { return FAULT };
        match range(mem.data_mut(&mut c), ptr, len) {
            Some(buf) => {
                if getrandom::fill(buf).is_ok() {
                    SUCCESS
                } else {
                    INVAL
                }
            }
            None => FAULT,
        }
    })?;

    linker.func_wrap(MODULE, "fd_write", |mut c: Ctx<'_>, fd: i32, iovs: u32, iovs_len: u32, written: u32| {
        if fd != 1 && fd != 2 {
            return BADF;
        }
        if iovs_len > MAX_ITEMS {
            return INVAL;
        }
        let Some(mem) = memory(&mut c) else { return FAULT };
        let mut total: u32 = 0;
        {
            let (data, state) = mem.data_and_store_mut(&mut c);
            for i in 0..iovs_len {
                let Some(iov) = element(iovs, i, 8) else { return FAULT };
                let (Some(ptr), Some(len)) = (get_u32(data, iov), iov.checked_add(4).and_then(|p| get_u32(data, p))) else {
                    return FAULT;
                };
                let Some(bytes) = range(data, ptr, len) else { return FAULT };
                if fd == 2 {
                    state.stderr.write(bytes);
                }
                total = total.wrapping_add(len);
            }
        }
        done(put_u32(mem.data_mut(&mut c), written, total))
    })?;
    // Standard input is always at its end.
    linker.func_wrap(MODULE, "fd_read", |mut c: Ctx<'_>, fd: i32, _iovs: u32, _len: u32, nread: u32| {
        if fd != 0 {
            return BADF;
        }
        let Some(mem) = memory(&mut c) else { return FAULT };
        done(put_u32(mem.data_mut(&mut c), nread, 0))
    })?;
    linker.func_wrap(MODULE, "fd_fdstat_get", |mut c: Ctx<'_>, fd: i32, stat: u32| {
        if !(0..=2).contains(&fd) {
            return BADF;
        }
        let Some(mem) = memory(&mut c) else { return FAULT };
        // fdstat: filetype u8, flags u16 at 2, rights_base u64 at 8,
        // rights_inheriting u64 at 16.
        let Some(buf) = range(mem.data_mut(&mut c), stat, 24) else { return FAULT };
        buf.fill(0);
        buf[0] = FILETYPE_CHARACTER_DEVICE;
        buf[8..16].copy_from_slice(&u64::MAX.to_le_bytes());
        SUCCESS
    })?;
    // Go asks for non-blocking standard I/O at startup; staying blocking keeps
    // every write a direct fd_write.
    linker.func_wrap(
        MODULE,
        "fd_fdstat_set_flags",
        |_: Ctx<'_>, fd: i32, _flags: i32| if (0..=2).contains(&fd) { NOTSUP } else { BADF },
    )?;
    // EBADF from fd 3 on tells Go there are no preopened directories.
    linker.func_wrap(MODULE, "fd_prestat_get", |_: Ctx<'_>, _: i32, _: u32| BADF)?;
    linker.func_wrap(MODULE, "fd_prestat_dir_name", |_: Ctx<'_>, _: i32, _: u32, _: u32| BADF)?;
    linker.func_wrap(MODULE, "fd_close", |_: Ctx<'_>, _: i32| BADF)?;
    linker.func_wrap(MODULE, "fd_filestat_get", |_: Ctx<'_>, _: i32, _: u32| BADF)?;
    // There is no filesystem: a path is never found.
    linker.func_wrap(MODULE, "path_filestat_get", |_: Ctx<'_>, _: i32, _: i32, _: u32, _: u32, _: u32| NOSYS)?;
    linker.func_wrap(MODULE, "path_open", |_: Ctx<'_>, _: i32, _: i32, _: u32, _: u32, _: i32, _: i64, _: i64, _: i32, _: u32| NOSYS)?;

    linker.func_wrap(MODULE, "poll_oneoff", poll_oneoff)?;
    linker.func_wrap(MODULE, "sched_yield", |_: Ctx<'_>| SUCCESS)?;
    linker.func_wrap(MODULE, "proc_exit", |_: Ctx<'_>, code: i32| -> wasmtime::Result<()> { Err(wasmtime::Error::new(Exit(code))) })?;
    Ok(())
}

/// Waits for the earliest clock subscription (at most [`MAX_SLEEP`]), then
/// reports every subscription ready.
///
/// Reporting everything ready is safe for Go's wasip1 runtime, the only caller:
/// it polls to sleep until its next timer, and its other subscriptions are file
/// descriptors it registered for network I/O, of which a reactor without a
/// network has none. Go matches the events it gets to its own pollers by
/// `userdata` and looks at its timers again after every return, so an early or
/// spurious wakeup only costs another poll. At most [`MAX_ITEMS`] subscriptions;
/// a clock this shim doesn't have is `EINVAL`.
fn poll_oneoff(mut c: Ctx<'_>, subs: u32, events: u32, nsubs: u32, nevents: u32) -> i32 {
    if nsubs == 0 || nsubs > MAX_ITEMS {
        return INVAL;
    }
    let Some(mem) = memory(&mut c) else { return FAULT };
    let mut wait: Option<Duration> = None;
    {
        let (data, state) = mem.data_and_store_mut(&mut c);
        for i in 0..nsubs {
            let Some(base) = element(subs, i, SUBSCRIPTION_SIZE) else { return FAULT };
            let Some(sub) = range(data, base, SUBSCRIPTION_SIZE) else { return FAULT };
            if sub[8] != EVENTTYPE_CLOCK {
                continue;
            }
            let id = u32::from_le_bytes(sub[16..20].try_into().expect("4 bytes")) as i32;
            let mut timeout = u64::from_le_bytes(sub[24..32].try_into().expect("8 bytes"));
            let flags = u16::from_le_bytes(sub[40..42].try_into().expect("2 bytes"));
            let Some(current) = now(state, id) else { return INVAL };
            if flags & SUBCLOCKFLAG_ABSTIME != 0 {
                timeout = timeout.saturating_sub(current);
            }
            let timeout = Duration::from_nanos(timeout);
            wait = Some(wait.map_or(timeout, |w| w.min(timeout)));
        }
    }
    if let Some(wait) = wait {
        std::thread::sleep(wait.min(MAX_SLEEP));
    }
    let data = mem.data_mut(&mut c);
    for i in 0..nsubs {
        let (Some(sub), Some(event_at)) = (element(subs, i, SUBSCRIPTION_SIZE), element(events, i, EVENT_SIZE)) else { return FAULT };
        let (Some(userdata), Some(kind)) = (get_u64(data, sub), sub.checked_add(8).and_then(|p| range(data, p, 1)).map(|b| b[0])) else {
            return FAULT;
        };
        let Some(event) = range(data, event_at, EVENT_SIZE) else { return FAULT };
        event.fill(0);
        event[..8].copy_from_slice(&userdata.to_le_bytes());
        event[10] = kind;
    }
    done(put_u32(data, nevents, nsubs))
}

/// The time of clock `id` in nanoseconds, or `None` for a clock this doesn't have.
fn now(state: &State, id: i32) -> Option<u64> {
    match id {
        CLOCK_REALTIME => Some(SystemTime::now().duration_since(UNIX_EPOCH).map_or(0, |d| u64::try_from(d.as_nanos()).unwrap_or(u64::MAX))),
        // Not zero: a Go runtime that sees a monotonic clock at 0 takes it for unset.
        CLOCK_MONOTONIC => {
            Some(u64::try_from(Instant::now().duration_since(state.started).as_nanos()).unwrap_or(u64::MAX).saturating_add(1))
        }
        _ => None,
    }
}

/// The module's linear memory, which a call from the module always has.
fn memory(c: &mut Ctx<'_>) -> Option<Memory> {
    c.get_export("memory").and_then(Extern::into_memory)
}

/// The address of element `i` of `size` bytes in an array at `base`, if it fits
/// the 32-bit address space without overflowing.
fn element(base: u32, i: u32, size: u32) -> Option<u32> {
    i.checked_mul(size).and_then(|offset| base.checked_add(offset))
}

/// `len` bytes at `ptr`, if they are in memory.
fn range(data: &mut [u8], ptr: u32, len: u32) -> Option<&mut [u8]> {
    let start = ptr as usize;
    data.get_mut(start..start.checked_add(len as usize)?)
}

fn get_u32(data: &mut [u8], ptr: u32) -> Option<u32> {
    range(data, ptr, 4).map(|b| u32::from_le_bytes((&*b).try_into().expect("4 bytes")))
}

fn get_u64(data: &mut [u8], ptr: u32) -> Option<u64> {
    range(data, ptr, 8).map(|b| u64::from_le_bytes((&*b).try_into().expect("8 bytes")))
}

fn put_u32(data: &mut [u8], ptr: u32, value: u32) -> Option<()> {
    range(data, ptr, 4).map(|b| b.copy_from_slice(&value.to_le_bytes()))
}

fn put_u64(data: &mut [u8], ptr: u32, value: u64) -> Option<()> {
    range(data, ptr, 8).map(|b| b.copy_from_slice(&value.to_le_bytes()))
}

/// SUCCESS for a write that landed in memory, FAULT for one that didn't.
fn done(written: Option<()>) -> i32 {
    written.map_or(FAULT, |()| SUCCESS)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn standard_error_keeps_its_first_and_last_bytes() {
        let tail = Stderr::default();
        tail.write(b"fatal error: out of memory\n");
        assert_eq!(tail.contents(), b"fatal error: out of memory\n");
        tail.write(&vec![b'x'; 10_000]);
        tail.write(b"\nmain.main()\n");
        let shown = String::from_utf8(tail.contents()).unwrap();
        assert!(shown.starts_with("fatal error: out of memory\n"), "{shown:.40}");
        assert!(shown.contains("\n[...]\n"));
        assert!(shown.ends_with("\nmain.main()\n"));
        assert!(shown.len() <= STDERR_HEAD + STDERR_TAIL + 16, "{}", shown.len());
    }

    #[test]
    fn memory_accessors_stay_in_bounds() {
        let mut mem = [0u8; 16];
        assert_eq!(put_u32(&mut mem, 12, 7), Some(()));
        assert_eq!(get_u32(&mut mem, 12), Some(7));
        assert_eq!(put_u64(&mut mem, 8, u64::MAX), Some(()));
        assert_eq!(get_u64(&mut mem, 8), Some(u64::MAX));
        for ptr in [13, 16, u32::MAX] {
            assert_eq!(put_u32(&mut mem, ptr, 1), None, "{ptr}");
        }
        assert!(range(&mut mem, 0, u32::MAX).is_none());
        assert!(range(&mut mem, u32::MAX, 2).is_none());
        assert_eq!(done(Some(())), SUCCESS);
        assert_eq!(done(None), FAULT);
    }
}

#[cfg(test)]
mod bounds_tests {
    use super::*;

    #[test]
    fn element_addresses_do_not_overflow() {
        assert_eq!(element(100, 3, 48), Some(244));
        assert_eq!(element(0, 0, 48), Some(0));
        assert_eq!(element(u32::MAX - 10, 1, 48), None);
        assert_eq!(element(0, u32::MAX, 48), None);
        assert_eq!(element(u32::MAX, 1, 0), Some(u32::MAX));
    }
}
