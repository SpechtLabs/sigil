//! The ABI glue: one instance of the module, JSON in, JSON out. See
//! `cmd/sigil-wasm/doc.go` for the ABI; in short:
//!
//! - `sigil_alloc(size) -> ptr`: the host writes a request there
//! - `sigil_call(ptr, len) -> i64`: handles one JSON request; the result
//!   packs the response as `(ptr << 32) | len`
//! - `sigil_free(ptr, size)`: releases a request after the call, and a
//!   response once the host has read it
//!
//! and the one import, `sigil.host_call(ptr, len) -> i64`, through which the
//! module asks the host to run a host function. The module owns (and frees)
//! that request; the host answers in memory from `sigil_alloc`, which the
//! module frees once it has read it.

use std::collections::HashMap;
use std::panic::{self, AssertUnwindSafe};
use std::sync::{Arc, Mutex, OnceLock};
use std::thread::ThreadId;
use std::time::{Duration, Instant};

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::Value;
use wasmtime::{Caller, Extern, Instance, Linker, Memory, Store, StoreLimits, StoreLimitsBuilder, Trap, TypedFunc};

use crate::error::{Error, SigilError, StoppedError};
use crate::module::{Limits, Module, Ticker, ticks};
use crate::types::{Diagnostic, HostFunction};
use crate::wasi::{self, Exit, Stderr};

/// The ABI version this crate speaks.
pub const ABI_VERSION: i32 = 1;

/// The deadline of a call without one: far enough not to fire.
const FOREVER: u64 = u64::MAX / 4;

/// What the store carries.
pub(crate) struct State {
    limits: StoreLimits,
    /// What the module wrote to standard error.
    pub(crate) stderr: Stderr,
    /// The monotonic clock's zero.
    pub(crate) started: Instant,
    /// The host functions of the policy being evaluated; set around each eval.
    functions: Option<Arc<HashMap<String, HostFunction>>>,
}

/// What bounds one call.
#[derive(Debug, Clone, Copy, Default)]
pub(crate) struct CallLimits {
    /// Kills the call from outside after this long.
    pub deadline: Option<Duration>,
    pub fuel: Option<u64>,
}

pub(crate) struct Runtime {
    store: Store<State>,
    memory: Memory,
    alloc: TypedFunc<i32, i32>,
    free: TypedFunc<(i32, i32), ()>,
    call: TypedFunc<(i32, i32), i64>,
    ticker: Arc<Ticker>,
    fuel: bool,
    stderr: Stderr,
    stopped: Arc<OnceLock<StoppedError>>,
    /// The thread a call runs on, while it runs: a host function that calls back
    /// into its own instance is told apart from another thread waiting its turn.
    owner: Arc<Mutex<Option<ThreadId>>>,
}

#[derive(serde::Deserialize)]
struct Probe {
    ok: bool,
}

#[derive(serde::Deserialize)]
struct Failure {
    error: Option<FailureBody>,
    #[serde(default)]
    diagnostics: Vec<Diagnostic>,
}

#[derive(serde::Deserialize)]
struct FailureBody {
    message: String,
    #[serde(default)]
    help: Option<String>,
}

#[derive(Serialize)]
struct Request<'a, T: Serialize> {
    op: &'a str,
    #[serde(flatten)]
    fields: T,
}

/// Declares the module's imports: WASI preview 1 and `sigil.host_call`.
pub(crate) fn add_imports(linker: &mut Linker<State>) -> Result<(), Error> {
    wasi::add_to_linker(linker).map_err(linking)?;
    linker.func_wrap("sigil", "host_call", host_call).map_err(linking)?;
    Ok(())
}

fn linking(err: wasmtime::Error) -> Error {
    Error::sigil(format!("linking the module's imports: {err}"), "this is a bug in the binding; please report it")
}

impl Runtime {
    /// Instantiates the module and initializes the Go runtime.
    pub(crate) fn new(module: &Module, limits: &Limits) -> Result<Self, Error> {
        let stderr = Stderr::default();
        let mut memory = StoreLimitsBuilder::new().trap_on_grow_failure(false);
        if let Some(max) = limits.max_memory {
            memory = memory.memory_size(max);
        }
        let state = State { limits: memory.build(), functions: None, stderr: stderr.clone(), started: Instant::now() };
        let mut store = Store::new(&module.engine, state);
        store.limiter(|s| &mut s.limits);
        store.epoch_deadline_trap();
        store.set_epoch_deadline(FOREVER);
        if module.fuel {
            store.set_fuel(u64::MAX).map_err(|e| Error::sigil(format!("setting fuel: {e}"), "this is a bug in the binding"))?;
        }

        let not_sigil = |what: &str| {
            Error::sigil(
                format!("the module doesn't export {what}; it isn't a sigil.wasm reactor"),
                "build it with `mise run wasm-build` (GOOS=wasip1, -buildmode=c-shared)",
            )
        };

        // Instantiating and initializing run Go's runtime and can trap, so a
        // failure is reported as a stopped instance, with what Go wrote.
        let init = |store: &mut Store<State>| -> wasmtime::Result<Instance> {
            let instance = module.pre.instantiate(&mut *store)?;
            if let Some(init) = instance.get_func(&mut *store, "_initialize") {
                init.typed::<(), ()>(&mut *store)?.call(&mut *store, ())?;
            }
            Ok(instance)
        };
        let instance = init(&mut store).map_err(|err| {
            let tail = String::from_utf8_lossy(&stderr.contents()).trim().to_string();
            let message = format!("starting the Sigil module failed: {}", describe(&err));
            Error::Stopped(StoppedError {
                message: if tail.is_empty() { message } else { format!("{message}\n{tail}") },
                help: "build sigil.wasm from the same Sigil revision as this crate (mise run wasm-build); with Limits::max_memory, the module needs about 8 MiB to start".into(),
            })
        })?;

        let memory_export = instance.get_memory(&mut store, "memory").ok_or_else(|| not_sigil("memory"))?;
        let version: TypedFunc<(), i32> =
            instance.get_typed_func(&mut store, "sigil_abi_version").map_err(|_| not_sigil("sigil_abi_version"))?;
        let alloc = instance.get_typed_func(&mut store, "sigil_alloc").map_err(|_| not_sigil("sigil_alloc"))?;
        let free = instance.get_typed_func(&mut store, "sigil_free").map_err(|_| not_sigil("sigil_free"))?;
        let call = instance.get_typed_func(&mut store, "sigil_call").map_err(|_| not_sigil("sigil_call"))?;
        let abi = version.call(&mut store, ()).map_err(|err| {
            Error::Stopped(StoppedError {
                message: format!("the Sigil module trapped while reporting its ABI version: {}", describe(&err)),
                help: "build sigil.wasm from the same Sigil revision as this crate".into(),
            })
        })?;
        if abi != ABI_VERSION {
            return Err(Error::sigil(
                format!("the module speaks ABI version {abi}, and this crate version {ABI_VERSION}"),
                "use the sigil.wasm built from the same Sigil revision as this crate",
            ));
        }
        Ok(Self {
            store,
            memory: memory_export,
            alloc,
            free,
            call,
            ticker: Arc::clone(&module.ticker),
            fuel: module.fuel,
            stderr,
            stopped: Arc::new(OnceLock::new()),
            owner: Arc::default(),
        })
    }

    pub(crate) fn owner_handle(&self) -> Arc<Mutex<Option<ThreadId>>> {
        Arc::clone(&self.owner)
    }

    /// The error every call returns once the instance has stopped.
    pub(crate) fn stopped_handle(&self) -> Arc<OnceLock<StoppedError>> {
        Arc::clone(&self.stopped)
    }

    /// Bytes of linear memory the instance has, for tests that watch for leaks.
    pub(crate) fn memory_size(&self) -> usize {
        self.memory.data_size(&self.store)
    }

    /// Runs one op and decodes its response: the typed body on `ok: true`, a
    /// [`SigilError`] on `ok: false`.
    pub(crate) fn op<R: DeserializeOwned>(
        &mut self,
        op: &str,
        fields: impl Serialize,
        functions: Option<Arc<HashMap<String, HostFunction>>>,
        limits: CallLimits,
    ) -> Result<R, Error> {
        // Encoding happens before the module is touched: a request that isn't
        // JSON is the caller's error, not the module stopping.
        let request = serde_json::to_vec(&Request { op, fields }).map_err(|err| {
            Error::Sigil(
                SigilError::new(format!("the request can't be encoded as JSON: {err}"))
                    .with_help("pass values serde can serialize: string keys, no NaN or infinity")
                    .with_source(err),
            )
        })?;
        self.store.data_mut().functions = functions;
        let response = self.call_raw(&request, limits);
        self.store.data_mut().functions = None;
        let response = response?;

        let probe: Probe = serde_json::from_slice(&response).map_err(|err| bad_response(&err))?;
        if !probe.ok {
            let failure: Failure = serde_json::from_slice(&response).map_err(|err| bad_response(&err))?;
            let message = failure.error.as_ref().map_or("the module returned an error without a message", |e| e.message.as_str());
            let help = failure.error.as_ref().and_then(|e| e.help.clone());
            let mut error = SigilError::new(message).with_diagnostics(failure.diagnostics);
            error.help = help;
            return Err(Error::Sigil(error));
        }
        serde_json::from_slice(&response).map_err(|err| bad_response(&err))
    }

    /// Sends one encoded request and returns the encoded response.
    fn call_raw(&mut self, request: &[u8], limits: CallLimits) -> Result<Vec<u8>, Error> {
        if let Some(stopped) = self.stopped.get() {
            return Err(Error::Stopped(stopped.clone()));
        }
        let len = i32::try_from(request.len())
            .map_err(|_| Error::sigil("the request is larger than the module's 32-bit memory can take", "send fewer or smaller files"))?;

        self.store.set_epoch_deadline(limits.deadline.map_or(FOREVER, ticks));
        if self.fuel {
            let _ = self.store.set_fuel(limits.fuel.unwrap_or(u64::MAX));
        } else if limits.fuel.is_some() {
            return Err(Error::sigil(
                "a fuel limit needs a module built with fuel metering",
                "compile the module with ModuleConfig { fuel: true }",
            ));
        }
        // Ticking only while a deadline is set keeps an idle process quiet.
        let _watch = limits.deadline.map(|_| self.ticker.watch());

        let owner = Arc::clone(&self.owner);
        let outcome = {
            *owner.lock().unwrap_or_else(|e| e.into_inner()) = Some(std::thread::current().id());
            let outcome = self.call_guarded(request, len);
            *owner.lock().unwrap_or_else(|e| e.into_inner()) = None;
            outcome
        };
        match outcome {
            Ok(Ok(response)) => Ok(response),
            Ok(Err(error)) => Err(error),
            Err(trap) => Err(self.stop(&trap, limits)),
        }
    }

    /// The call itself. The outer error is whatever unwound the module (a
    /// trap, an exit, a killed call); the inner is this crate's own failure,
    /// such as an allocation that failed, which leaves the module intact.
    fn call_guarded(&mut self, request: &[u8], len: i32) -> wasmtime::Result<Result<Vec<u8>, Error>> {
        let ptr = self.alloc.call(&mut self.store, len)?;
        if ptr == 0 && len > 0 {
            return Ok(Err(Error::sigil(
                "the module couldn't allocate memory for a request",
                "the instance is out of memory; build a new one",
            )));
        }
        self.memory.write(&mut self.store, ptr as u32 as usize, request)?;
        // A call that traps leaves the request unfreed: the instance is dead,
        // and calling into it again could only fail too, and hide why.
        let packed = self.call.call(&mut self.store, (ptr, len))?;
        self.free.call(&mut self.store, (ptr, len))?;
        let (rptr, rlen) = unpack(packed);
        // The length comes from the guest: check it against its memory before
        // allocating, so a bad response is an error and not a 4 GiB allocation.
        let response = slice(self.memory.data(&self.store), rptr, rlen)
            .ok_or_else(|| wasmtime::Error::msg("the module returned a response outside its memory"))?
            .to_vec();
        self.free.call(&mut self.store, (rptr as i32, rlen as i32))?;
        Ok(Ok(response))
    }

    /// Records why the instance stopped, and returns the error for the call
    /// that stopped it. Whatever unwound the module mid-call, whether
    /// `proc_exit`, a trap or a killed call, left Go's runtime halfway through
    /// a function it can't resume: calling in again would run on corrupted
    /// state, leak the memory of every abandoned call, and trap later
    /// somewhere unrelated.
    fn stop(&mut self, err: &wasmtime::Error, limits: CallLimits) -> Error {
        let tail = String::from_utf8_lossy(&self.stderr.contents()).trim().to_string();
        let with_tail = |reason: String| if tail.is_empty() { reason } else { format!("{reason}\n{tail}") };
        let (reason, result) = match err.downcast_ref::<Trap>() {
            Some(Trap::Interrupt) => {
                let after = limits.deadline.unwrap_or_default();
                ("a call ran past its hard deadline and was killed".to_string(), Error::Timeout(after))
            }
            Some(Trap::OutOfFuel) => ("a call ran out of fuel and was stopped".to_string(), Error::OutOfFuel),
            _ => {
                let reason = format!("the Sigil module stopped: {}", describe(err));
                let help = "this instance can't be used again: build a new one with Sigil::new (a Pool does that by itself). \
                            Unless the input or policy was extreme, such as nesting thousands deep, this is a bug in Sigil: \
                            please report it with the output above";
                let stopped = StoppedError { message: with_tail(reason), help: help.into() };
                let _ = self.stopped.set(stopped.clone());
                return Error::Stopped(stopped);
            }
        };
        let _ = self.stopped.set(StoppedError {
            message: with_tail(format!("the Sigil module stopped earlier: {reason}")),
            help: "this instance can't be used again: build a new one with Sigil::new (a Pool does that by itself)".into(),
        });
        result
    }
}

/// Runs the host function the module asks for and returns the packed response.
fn host_call(mut caller: Caller<'_, State>, ptr: i32, len: i32) -> wasmtime::Result<i64> {
    let memory =
        caller.get_export("memory").and_then(Extern::into_memory).ok_or_else(|| wasmtime::Error::msg("the module has no memory"))?;
    // Guest-controlled: bounds-check before copying.
    let request = slice(memory.data(&caller), ptr as u32, len as u32)
        .ok_or_else(|| wasmtime::Error::msg("the module sent a host call outside its memory"))?
        .to_vec();

    let response = match run_host_function(caller.data().functions.as_deref(), &request) {
        Ok(result) => serde_json::json!({ "result": result }),
        Err(message) => serde_json::json!({ "error": message }),
    };
    let response = serde_json::to_vec(&response).map_err(|e| wasmtime::Error::msg(e.to_string()))?;

    let alloc = caller
        .get_export("sigil_alloc")
        .and_then(Extern::into_func)
        .ok_or_else(|| wasmtime::Error::msg("the module has no sigil_alloc"))?
        .typed::<i32, i32>(&caller)?;
    let rlen = i32::try_from(response.len()).map_err(|_| wasmtime::Error::msg("a host function's response is too large"))?;
    let rptr = alloc.call(&mut caller, rlen)?;
    if rptr == 0 && rlen > 0 {
        return Err(wasmtime::Error::msg("the module couldn't allocate memory for a host function's response"));
    }
    memory.write(&mut caller, rptr as u32 as usize, &response)?;
    Ok(pack(rptr as u32, rlen as u32))
}

/// Dispatches one host function request; a message on any failure, which the
/// module turns into a runtime error of the evaluation.
fn run_host_function(functions: Option<&HashMap<String, HostFunction>>, request: &[u8]) -> Result<Value, String> {
    #[derive(serde::Deserialize)]
    struct HostCall {
        function: String,
        #[serde(default)]
        args: Vec<Value>,
    }
    let HostCall { function, args } = serde_json::from_slice(request).map_err(|e| format!("the module's host call isn't readable: {e}"))?;
    let Some(f) = functions.and_then(|fns| fns.get(&function)) else {
        return Err(format!("no host function {function} is registered"));
    };
    // A panic must not unwind through the module's frames; it fails the call.
    match panic::catch_unwind(AssertUnwindSafe(|| f(args))) {
        Ok(result) => result,
        Err(payload) => {
            let what = payload.downcast_ref::<&str>().map(|s| (*s).to_string()).or_else(|| payload.downcast_ref::<String>().cloned());
            Err(format!("host function {function} panicked: {}", what.unwrap_or_else(|| "without a message".into())))
        }
    }
}

/// `len` bytes at `ptr` of `data`, if they are inside it.
fn slice(data: &[u8], ptr: u32, len: u32) -> Option<&[u8]> {
    let start = ptr as usize;
    data.get(start..start.checked_add(len as usize)?)
}

fn pack(ptr: u32, len: u32) -> i64 {
    ((u64::from(ptr) << 32) | u64::from(len)) as i64
}

fn unpack(packed: i64) -> (u32, u32) {
    let bits = packed as u64;
    ((bits >> 32) as u32, (bits & 0xffff_ffff) as u32)
}

fn bad_response(err: &serde_json::Error) -> Error {
    Error::sigil(
        format!("the module's response doesn't fit this crate's types: {err}"),
        "use the sigil.wasm built from the same Sigil revision as this crate",
    )
}

/// Why the module stopped, for the error's message.
fn describe(err: &wasmtime::Error) -> String {
    if let Some(exit) = err.downcast_ref::<Exit>() {
        return format!("it exited with code {}", exit.0);
    }
    if let Some(trap) = err.downcast_ref::<Trap>() {
        return format!("it trapped: {trap}");
    }
    format!("{err:#}")
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;

    #[rstest]
    #[case(0, 0)]
    #[case(1, 2)]
    #[case(0x0068_0000, 0x007e_7aa6)]
    #[case(u32::MAX, u32::MAX)]
    fn packs_and_unpacks_an_address_and_a_length(#[case] ptr: u32, #[case] len: u32) {
        assert_eq!(unpack(pack(ptr, len)), (ptr, len));
    }

    #[test]
    fn a_host_call_dispatches_by_name_and_reports_every_failure_as_a_message() {
        let fns: HashMap<String, HostFunction> = [
            ("echo".to_string(), crate::types::host_fn(|args| Ok(args.into_iter().next().unwrap_or(Value::Null)))),
            ("fail".to_string(), crate::types::host_fn(|_| Err("nope".into()))),
            ("boom".to_string(), crate::types::host_fn(|_| panic!("kaboom"))),
            ("boxed".to_string(), crate::types::host_fn(|_| std::panic::panic_any(7u8))),
        ]
        .into();
        let run = |req: &str, fns: Option<&HashMap<String, HostFunction>>| run_host_function(fns, req.as_bytes());
        assert_eq!(run(r#"{"function":"echo","args":[1]}"#, Some(&fns)), Ok(serde_json::json!(1)));
        assert_eq!(run(r#"{"function":"echo"}"#, Some(&fns)), Ok(Value::Null));
        assert_eq!(run(r#"{"function":"fail","args":[]}"#, Some(&fns)), Err("nope".into()));
        assert_eq!(run(r#"{"function":"boom","args":[]}"#, Some(&fns)), Err("host function boom panicked: kaboom".into()));
        assert_eq!(run(r#"{"function":"boxed","args":[]}"#, Some(&fns)), Err("host function boxed panicked: without a message".into()));
        assert_eq!(run(r#"{"function":"absent","args":[]}"#, Some(&fns)), Err("no host function absent is registered".into()));
        assert_eq!(run(r#"{"function":"echo","args":[]}"#, None), Err("no host function echo is registered".into()));
        assert!(run("not json", Some(&fns)).unwrap_err().contains("isn't readable"));
    }
}
