//! Loading the module: compile once, instantiate cheaply per [`crate::Sigil`].

use std::path::Path;
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use wasmtime::{Engine, InstancePre, Linker};

use crate::error::{Error, SigilError};
use crate::runtime::{self, State};
use crate::wasi::{self, MODULE as WASI_MODULE};

/// How often the epoch ticker advances the engine's clock while a call with a
/// deadline runs. A hard deadline fires within about this much of its time.
const TICK: Duration = Duration::from_millis(2);

/// How long the ticker keeps ticking after the last watched call ended before it
/// goes back to sleep. An idle process is quiet after this; a busy one never
/// pays a wakeup per call.
const LINGER: Duration = Duration::from_millis(50);

/// The imports the module may have, besides the WASI preview 1 ones.
const HOST_MODULE: &str = "sigil";

/// The compiled module: an `Engine` and a `Module`, shared by every instance.
/// Compiling the 11 MB module is Cranelift work, about 4 s of CPU (0.3 s on a
/// 12-core machine), paid at every process start unless the module is
/// precompiled: the `precompiled` feature does it at build time, and
/// [`Module::precompile`] and [`Module::from_precompiled`] do it for a module of
/// your own. Compile (or load) once, clone the `Module` (cheap, reference
/// counted), and give each [`crate::Sigil`] or [`crate::Pool`] one.
#[derive(Clone)]
pub struct Module {
    pub(crate) engine: Engine,
    pub(crate) pre: InstancePre<State>,
    pub(crate) ticker: Arc<Ticker>,
    pub(crate) fuel: bool,
    pub(crate) precompiled: bool,
}

/// How a [`Module`] is compiled.
#[derive(Debug, Clone, Copy, Default)]
pub struct ModuleConfig {
    /// Meters fuel, so a call can be bounded by work done rather than time,
    /// with [`crate::EvalOptions::fuel`]. Costs a few percent of speed, so
    /// it's off by default.
    pub fuel: bool,
}

/// Bounds for every instance built from a module, see [`crate::Sigil::with_limits`].
#[derive(Debug, Clone, Copy)]
pub struct Limits {
    /// How long past an evaluation's timeout it may run before it's killed.
    /// Default 500 ms, which leaves the module time to stop by itself at its
    /// next deadline check.
    pub grace: Duration,
    /// The hard deadline of ops that have no timeout of their own (`check`,
    /// `compile`, `explain`, `format`, `version`, `release`), and of an
    /// evaluation without one. Default 60 s; `None` for no deadline.
    pub op_deadline: Option<Duration>,
    /// The most linear memory an instance may grow to, in bytes. Default 4 GiB,
    /// the module's own maximum. A Go runtime that can't grow stops.
    pub max_memory: Option<usize>,
}

impl Default for Limits {
    fn default() -> Self {
        Self { grace: Duration::from_millis(500), op_deadline: Some(Duration::from_secs(60)), max_memory: None }
    }
}

impl Module {
    /// Compiles the module from its bytes.
    pub fn from_bytes(bytes: &[u8]) -> Result<Self, Error> {
        Self::from_bytes_with(bytes, ModuleConfig::default())
    }

    /// [`Module::from_bytes`] with a [`ModuleConfig`].
    pub fn from_bytes_with(bytes: &[u8], config: ModuleConfig) -> Result<Self, Error> {
        let engine = engine(config)?;
        let module = wasmtime::Module::new(&engine, bytes).map_err(|err| {
            Error::Sigil(
                SigilError::new(format!("the bytes aren't a WebAssembly module wasmtime can compile: {err}"))
                    .with_help("build sigil.wasm with `mise run wasm-build` (GOOS=wasip1, -buildmode=c-shared)"),
            )
        })?;
        Self::link(engine, module, config, false)
    }

    /// Links a compiled module: checks its imports and prepares instantiation.
    fn link(engine: Engine, module: wasmtime::Module, config: ModuleConfig, precompiled: bool) -> Result<Self, Error> {
        for import in module.imports() {
            let known = match import.module() {
                WASI_MODULE => wasi::IMPORTS.contains(&import.name()),
                HOST_MODULE => import.name() == "host_call",
                _ => false,
            };
            if !known {
                return Err(Error::sigil(
                    format!("the module imports {}.{}, which this crate doesn't provide", import.module(), import.name()),
                    "build sigil.wasm from the same Sigil revision as this crate (mise run wasm-build)",
                ));
            }
        }
        let mut linker: Linker<State> = Linker::new(&engine);
        runtime::add_imports(&mut linker)?;
        let pre = linker.instantiate_pre(&module).map_err(|err| {
            Error::sigil(format!("linking the module: {err}"), "build sigil.wasm from the same Sigil revision as this crate")
        })?;
        let ticker = Arc::new(Ticker::start(engine.clone()));
        Ok(Self { engine, pre, ticker, fuel: config.fuel, precompiled })
    }

    /// Reads and compiles the module from a file, such as `dist/wasm/sigil.wasm`.
    pub fn from_file(path: impl AsRef<Path>) -> Result<Self, Error> {
        Self::from_file_with(path, ModuleConfig::default())
    }

    /// [`Module::from_file`] with a [`ModuleConfig`].
    pub fn from_file_with(path: impl AsRef<Path>, config: ModuleConfig) -> Result<Self, Error> {
        let path = path.as_ref();
        let bytes = std::fs::read(path).map_err(|err| {
            Error::Sigil(
                SigilError::new(format!("reading {}: {err}", path.display()))
                    .with_help("build the module with `mise run wasm-build`, or pass the path of a sigil.wasm")
                    .with_source(err),
            )
        })?;
        Self::from_bytes_with(&bytes, config)
    }

    /// Compiles the module bundled into this crate (the default `bundled`
    /// feature). Compile it once and share the result.
    #[cfg(feature = "bundled")]
    pub fn bundled() -> Result<Self, Error> {
        Self::bundled_with(ModuleConfig::default())
    }

    /// [`Module::bundled`] with a [`ModuleConfig`].
    #[cfg(feature = "bundled")]
    pub fn bundled_with(config: ModuleConfig) -> Result<Self, Error> {
        #[cfg(feature = "precompiled")]
        return Self::load_bundled(crate::bundled::WASM, crate::bundled::PRECOMPILED, config);
        #[cfg(not(feature = "precompiled"))]
        Self::from_bytes_with(crate::bundled::WASM, config)
    }

    /// The bundled loader with its inputs as arguments, so a test can hand it an
    /// artifact wasmtime refuses: it must fall back to compiling `wasm`.
    #[cfg(feature = "precompiled")]
    fn load_bundled(wasm: &[u8], artifact: &[u8], config: ModuleConfig) -> Result<Self, Error> {
        if !config.fuel && !artifact.is_empty() {
            // SAFETY: build.rs wrote these bytes, with the engine configuration of
            // `engine::config`, and this crate embedded them: they are as trusted as
            // the binary itself. An artifact wasmtime refuses (a CPU without a
            // feature it was compiled for, say) falls through to compiling.
            if let Ok(module) = unsafe { Self::from_precompiled(artifact, config) } {
                return Ok(module);
            }
        }
        Self::from_bytes_with(wasm, config)
    }

    /// Whether the module meters fuel.
    pub fn fuel(&self) -> bool {
        self.fuel
    }

    /// Whether this module was loaded from a precompiled artifact rather than
    /// compiled: [`Module::bundled`] with the `precompiled` feature, or
    /// [`Module::from_precompiled`].
    pub fn is_precompiled(&self) -> bool {
        self.precompiled
    }

    /// Serializes the compiled module: native code for this machine, which
    /// [`Module::from_precompiled`] loads in microseconds instead of compiling
    /// for seconds. Do it once, at build time or at first start, and keep the
    /// bytes; they are only good for the same version of this crate, on the same
    /// architecture, with the same [`ModuleConfig`].
    pub fn precompile(&self) -> Result<Vec<u8>, Error> {
        self.pre
            .module()
            .serialize()
            .map_err(|err| Error::sigil(format!("serializing the module: {err}"), "this is a bug in the binding; please report it"))
    }

    /// Loads a module [`Module::precompile`] wrote, skipping the compile.
    ///
    /// # Safety
    ///
    /// The bytes are native machine code that is run as it is, so they must be
    /// exactly what [`Module::precompile`] returned, from a source you trust as
    /// much as this program's own binary: a file you wrote at build time into a
    /// directory only you can write to, not a download or a user's upload.
    /// Wasmtime checks the artifact's version, architecture and settings and
    /// refuses a mismatch with an error, but it can't tell a hand-edited
    /// artifact from a genuine one. `config` must be the one the module was
    /// precompiled with.
    pub unsafe fn from_precompiled(bytes: &[u8], config: ModuleConfig) -> Result<Self, Error> {
        let engine = engine(config)?;
        // SAFETY: the caller promises the bytes are a genuine artifact.
        let module = unsafe { wasmtime::Module::deserialize(&engine, bytes) }.map_err(precompiled_error)?;
        Self::link(engine, module, config, true)
    }

    /// [`Module::from_precompiled`] for a file, which is memory mapped instead
    /// of read: the start costs next to nothing, and processes that load the
    /// same file share its pages.
    ///
    /// # Safety
    ///
    /// As for [`Module::from_precompiled`], and the file must not be changed
    /// or truncated while the module is in use, because it is mapped.
    pub unsafe fn from_precompiled_file(path: impl AsRef<Path>, config: ModuleConfig) -> Result<Self, Error> {
        let engine = engine(config)?;
        // SAFETY: the caller promises the file is a genuine, unchanging artifact.
        let module = unsafe { wasmtime::Module::deserialize_file(&engine, path.as_ref()) }.map_err(precompiled_error)?;
        Self::link(engine, module, config, true)
    }
}

fn engine(config: ModuleConfig) -> Result<Engine, Error> {
    Engine::new(&crate::engine::config(config.fuel)).map_err(|err| {
        Error::Sigil(SigilError::new(format!("configuring wasmtime: {err}")).with_help("this is a bug in the binding; please report it"))
    })
}

fn precompiled_error(err: wasmtime::Error) -> Error {
    Error::sigil(
        format!("the precompiled module can't be loaded: {err}"),
        "precompile it again with Module::precompile from the same version of this crate, on this architecture, and with the same ModuleConfig",
    )
}

impl std::fmt::Debug for Module {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Module").field("fuel", &self.fuel).finish_non_exhaustive()
    }
}

/// Advances the engine's epoch clock every [`TICK`], but only while a call
/// with a deadline runs, so an idle process has no timer thread waking up.
/// Dropping the last `Module` clone stops the thread.
pub(crate) struct Ticker {
    shared: Arc<TickerShared>,
}

struct TickerShared {
    state: Mutex<TickerState>,
    changed: Condvar,
}

#[derive(Default)]
struct TickerState {
    /// Calls with a deadline that are running.
    watching: usize,
    stop: bool,
    /// The thread is waiting without a timeout: only a `watch` that finds this
    /// set needs to wake it. While calls keep coming it stays awake, so a call
    /// that starts costs a lock and no system call.
    parked: bool,
}

/// Held while a call with a deadline runs.
pub(crate) struct Watch {
    shared: Arc<TickerShared>,
}

impl Ticker {
    fn start(engine: Engine) -> Self {
        let shared = Arc::new(TickerShared { state: Mutex::new(TickerState::default()), changed: Condvar::new() });
        let thread_shared = Arc::clone(&shared);
        // A detached thread: it ends by itself when `stop` is set.
        thread::Builder::new()
            .name("sigil-epoch-ticker".into())
            .spawn(move || ticker_loop(&engine, &thread_shared))
            .expect("the OS can start one more thread");
        Self { shared }
    }

    /// Starts ticking until the returned guard drops.
    pub(crate) fn watch(&self) -> Watch {
        let mut state = lock(&self.shared.state);
        state.watching += 1;
        if state.parked {
            self.shared.changed.notify_all();
        }
        Watch { shared: Arc::clone(&self.shared) }
    }
}

impl Drop for Ticker {
    fn drop(&mut self) {
        lock(&self.shared.state).stop = true;
        self.shared.changed.notify_all();
    }
}

impl Drop for Watch {
    fn drop(&mut self) {
        lock(&self.shared.state).watching -= 1;
    }
}

fn ticker_loop(engine: &Engine, shared: &TickerShared) {
    let mut state = lock(&shared.state);
    // When the clock is next due, while some call is watched. Absolute, so a
    // wakeup for any other reason (a call starting, a thousand per second)
    // neither delays the tick nor advances it early: the clock moves when its
    // time has come, whatever woke the thread.
    let mut due: Option<Instant> = None;
    // When the last call ended; the clock keeps ticking for [`LINGER`] after, so
    // a steady stream of calls never has to wake the thread.
    let mut idle_since: Option<Instant> = None;
    loop {
        if state.stop {
            return;
        }
        let now = Instant::now();
        if state.watching > 0 {
            idle_since = None;
        } else if idle_since.is_some_and(|since| now.duration_since(since) >= LINGER) {
            // Quiet for a while: sleep until a call asks for the clock.
            idle_since = None;
            due = None;
            state.parked = true;
            while state.watching == 0 && !state.stop {
                state = shared.changed.wait(state).unwrap_or_else(|e| e.into_inner());
            }
            state.parked = false;
            continue;
        } else if idle_since.is_none() {
            idle_since = Some(now);
        }
        let at = *due.get_or_insert(now + TICK);
        if now >= at {
            engine.increment_epoch();
            // Behind schedule (a stalled thread): don't fire a burst to catch up.
            due = Some((at + TICK).max(now));
            continue;
        }
        state = shared.changed.wait_timeout(state, at - now).unwrap_or_else(|e| e.into_inner()).0;
    }
}

fn lock<T>(m: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

/// The number of ticks a deadline of `d` spans, plus one for the tick in
/// progress.
pub(crate) fn ticks(d: Duration) -> u64 {
    let ticks = d.as_nanos().div_ceil(TICK.as_nanos());
    u64::try_from(ticks).unwrap_or(u64::MAX / 4).min(u64::MAX / 4).saturating_add(1)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_deadline_spans_its_ticks_plus_the_one_in_progress() {
        assert_eq!(ticks(Duration::ZERO), 1);
        assert_eq!(ticks(TICK), 2);
        assert_eq!(ticks(TICK + Duration::from_nanos(1)), 3);
        assert_eq!(ticks(Duration::from_millis(100)), 51);
        assert!(ticks(Duration::MAX) > 1 << 60);
    }

    #[test]
    fn what_isnt_a_module_is_refused_with_advice() {
        let err = Module::from_bytes(b"not wasm").unwrap_err().to_string();
        assert!(err.contains("aren't a WebAssembly module") && err.contains("mise run wasm-build"), "{err}");
        let err = Module::from_file("/no/such/sigil.wasm").unwrap_err().to_string();
        assert!(err.contains("/no/such/sigil.wasm") && err.contains("wasm-build"), "{err}");
    }

    #[test]
    fn a_module_with_imports_nobody_provides_is_refused() {
        let wasm = wasmtime::Engine::default().precompile_module(b"(module (import \"other\" \"f\" (func)))").map(|_| ());
        assert!(wasm.is_ok());
        let err = Module::from_bytes(b"(module (import \"other\" \"f\" (func)))").unwrap_err().to_string();
        assert!(err.contains("imports other.f"), "{err}");
    }

    #[test]
    fn a_module_that_isnt_a_sigil_reactor_is_named_as_such() {
        let module = Module::from_bytes(b"(module (memory (export \"memory\") 1))").unwrap();
        let err = crate::Sigil::new(&module).unwrap_err().to_string();
        assert!(err.contains("doesn't export sigil_abi_version"), "{err}");
    }

    #[test]
    fn a_module_speaking_another_abi_is_refused() {
        let wat = r#"(module
            (memory (export "memory") 1)
            (func (export "sigil_abi_version") (result i32) i32.const 9)
            (func (export "sigil_alloc") (param i32) (result i32) i32.const 0)
            (func (export "sigil_free") (param i32 i32))
            (func (export "sigil_call") (param i32 i32) (result i64) i64.const 0))"#;
        let module = Module::from_bytes(wat.as_bytes()).unwrap();
        let err = crate::Sigil::new(&module).unwrap_err().to_string();
        assert!(err.contains("speaks ABI version 9"), "{err}");
    }

    #[test]
    fn a_module_that_exits_while_starting_is_a_stopped_instance() {
        let wat = r#"(module
            (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
            (memory (export "memory") 1)
            (func (export "_initialize") i32.const 3 call $exit)
            (func (export "sigil_abi_version") (result i32) i32.const 1)
            (func (export "sigil_alloc") (param i32) (result i32) i32.const 0)
            (func (export "sigil_free") (param i32 i32))
            (func (export "sigil_call") (param i32 i32) (result i64) i64.const 0))"#;
        let module = Module::from_bytes(wat.as_bytes()).unwrap();
        let err = crate::Sigil::new(&module).unwrap_err();
        assert!(err.is_stopped());
        assert!(err.to_string().contains("exited with code 3"), "{err}");
    }
}

#[cfg(test)]
mod ticker_tests {
    use super::*;
    use std::sync::atomic::{AtomicBool, Ordering};

    /// A call that loops in WebAssembly past its deadline is interrupted even
    /// while other threads start and finish watched calls as fast as they can,
    /// each of which wakes the ticker.
    #[test]
    fn the_deadline_fires_while_other_calls_hammer_the_ticker() {
        let module = Module::from_bytes(b"(module)").unwrap();
        let wasm = wasmtime::Module::new(&module.engine, r#"(module (func (export "spin") (loop $l br $l)))"#).unwrap();
        let mut store = wasmtime::Store::new(&module.engine, ());
        store.epoch_deadline_trap();
        store.set_epoch_deadline(ticks(Duration::from_millis(20)));
        let spin = wasmtime::Instance::new(&mut store, &wasm, &[]).unwrap().get_typed_func::<(), ()>(&mut store, "spin").unwrap();

        let stop = Arc::new(AtomicBool::new(false));
        let hammers: Vec<_> = (0..4)
            .map(|_| {
                let (ticker, stop) = (Arc::clone(&module.ticker), Arc::clone(&stop));
                thread::spawn(move || {
                    while !stop.load(Ordering::Relaxed) {
                        drop(ticker.watch());
                    }
                })
            })
            .collect();

        // A starved clock would spin forever: a rescuer ends the call after 5 s, so
        // the test fails instead of hanging.
        let rescued = Arc::new(AtomicBool::new(false));
        let rescuer = {
            let (engine, stop, rescued) = (module.engine.clone(), Arc::clone(&stop), Arc::clone(&rescued));
            thread::spawn(move || {
                let until = Instant::now() + Duration::from_secs(5);
                while Instant::now() < until {
                    if stop.load(Ordering::Relaxed) {
                        return;
                    }
                    thread::sleep(Duration::from_millis(10));
                }
                rescued.store(true, Ordering::Relaxed);
                for _ in 0..1000 {
                    engine.increment_epoch();
                }
            })
        };

        let watch = module.ticker.watch();
        let started = Instant::now();
        let err = spin.call(&mut store, ()).unwrap_err();
        drop(watch);
        stop.store(true, Ordering::Relaxed);
        for h in hammers {
            h.join().unwrap();
        }
        rescuer.join().unwrap();
        assert!(!rescued.load(Ordering::Relaxed), "the ticker starved: the deadline never fired on its own");

        assert_eq!(err.downcast_ref::<wasmtime::Trap>(), Some(&wasmtime::Trap::Interrupt), "{err}");
        // 20 ms of deadline plus a tick or two; a starved clock would never get here.
        assert!(started.elapsed() < Duration::from_secs(2), "took {:?}", started.elapsed());
    }

    #[test]
    fn an_idle_ticker_leaves_the_clock_alone_and_restarts_fresh() {
        let module = Module::from_bytes(b"(module)").unwrap();
        let wasm = wasmtime::Module::new(&module.engine, r#"(module (func (export "spin") (loop $l br $l)))"#).unwrap();
        // Idle for a while, then a call: its deadline counts from its own start.
        thread::sleep(Duration::from_millis(50));
        let mut store = wasmtime::Store::new(&module.engine, ());
        store.epoch_deadline_trap();
        store.set_epoch_deadline(ticks(Duration::from_millis(40)));
        let spin = wasmtime::Instance::new(&mut store, &wasm, &[]).unwrap().get_typed_func::<(), ()>(&mut store, "spin").unwrap();
        let watch = module.ticker.watch();
        let started = Instant::now();
        assert!(spin.call(&mut store, ()).is_err());
        drop(watch);
        let took = started.elapsed();
        assert!(took >= Duration::from_millis(30) && took < Duration::from_secs(2), "took {took:?}");
    }
}

#[cfg(all(test, feature = "precompiled"))]
mod fallback_tests {
    use super::*;

    /// A module that was compiled, not loaded from the artifact, and works.
    fn assert_compiled_and_working(module: &Module) {
        assert!(!module.is_precompiled());
        assert!(crate::Sigil::new(module).unwrap().version().is_ok());
    }

    #[test]
    fn an_artifact_wasmtime_refuses_falls_back_to_compiling() {
        let mut corrupt = crate::bundled::PRECOMPILED.to_vec();
        corrupt.truncate(corrupt.len() / 2);
        for artifact in [&b"not an artifact"[..], &corrupt[..]] {
            let module = Module::load_bundled(crate::bundled::WASM, artifact, ModuleConfig::default()).unwrap();
            assert_compiled_and_working(&module);
        }
    }

    #[test]
    fn an_empty_artifact_or_fuel_compiles_and_a_good_artifact_loads() {
        let empty = Module::load_bundled(crate::bundled::WASM, &[], ModuleConfig::default()).unwrap();
        assert_compiled_and_working(&empty);
        let metered = Module::load_bundled(crate::bundled::WASM, crate::bundled::PRECOMPILED, ModuleConfig { fuel: true }).unwrap();
        assert_compiled_and_working(&metered);
        assert!(metered.fuel());
        let good = Module::load_bundled(crate::bundled::WASM, crate::bundled::PRECOMPILED, ModuleConfig::default()).unwrap();
        assert!(good.is_precompiled());
    }
}

/// Modules that misbehave at the ABI: what the guest says is checked against its
/// memory, so a bad pointer or length is an error that stops the instance and
/// not a huge allocation or a panic of the host.
#[cfg(test)]
mod hostile_guest_tests {
    use super::*;

    fn instance(wat: &str) -> crate::Sigil {
        crate::Sigil::new(&Module::from_bytes(wat.as_bytes()).unwrap()).unwrap()
    }

    #[test]
    fn a_response_outside_the_guests_memory_stops_the_instance_without_allocating_it() {
        // sigil_call answers (ptr 1, len 0xF0000000): 3.75 GiB, nowhere near 64 KiB of memory.
        let sigil = instance(
            r#"(module
                (memory (export "memory") 1)
                (func (export "sigil_abi_version") (result i32) i32.const 1)
                (func (export "sigil_alloc") (param i32) (result i32) i32.const 16)
                (func (export "sigil_free") (param i32 i32))
                (func (export "sigil_call") (param i32 i32) (result i64) i64.const 8321499136))"#,
        );
        let err = sigil.version().unwrap_err();
        assert!(err.is_stopped(), "{err}");
        assert!(err.to_string().contains("response outside its memory"), "{err}");
        assert!(sigil.stopped().is_some());
    }

    #[test]
    fn a_host_call_outside_the_guests_memory_stops_the_instance() {
        let sigil = instance(
            r#"(module
                (import "sigil" "host_call" (func $host (param i32 i32) (result i64)))
                (memory (export "memory") 1)
                (func (export "sigil_abi_version") (result i32) i32.const 1)
                (func (export "sigil_alloc") (param i32) (result i32) i32.const 16)
                (func (export "sigil_free") (param i32 i32))
                (func (export "sigil_call") (param i32 i32) (result i64)
                    i32.const 65000 i32.const -1 call $host))"#,
        );
        let err = sigil.version().unwrap_err();
        assert!(err.to_string().contains("host call outside its memory"), "{err}");
    }

    #[test]
    fn a_failed_allocation_for_a_host_response_is_reported() {
        // The first sigil_alloc (the request) succeeds, the second (the host function's response) answers 0.
        let sigil = instance(
            r#"(module
                (import "sigil" "host_call" (func $host (param i32 i32) (result i64)))
                (memory (export "memory") 1)
                (global $calls (mut i32) (i32.const 0))
                (func (export "sigil_abi_version") (result i32) i32.const 1)
                (func (export "sigil_alloc") (param i32) (result i32)
                    global.get $calls i32.const 1 i32.add global.set $calls
                    global.get $calls i32.const 1 i32.eq if (result i32) i32.const 16 else i32.const 0 end)
                (func (export "sigil_free") (param i32 i32))
                (func (export "sigil_call") (param i32 i32) (result i64)
                    i32.const 16 i32.const 0 call $host))"#,
        );
        let err = sigil.version().unwrap_err();
        assert!(err.to_string().contains("couldn't allocate memory for a host function's response"), "{err}");
    }
}

/// WASI calls with hostile arguments: the guest's counts and addresses are
/// checked, so a bad one is an errno, never a panic, a huge loop or a 50 ms sleep.
#[cfg(test)]
mod hostile_wasi_tests {
    use super::*;
    use rstest::rstest;

    /// A guest whose `sigil_call` makes one WASI call, traps unless it
    /// answers `errno`, and otherwise answers `{"ok":true}`.
    fn guest(import: &str, call: &str, errno: i32) -> crate::Sigil {
        let wat = format!(
            r#"(module
                {import}
                (memory (export "memory") 1)
                (data (i32.const 2000) "{{\"ok\":true}}")
                (data (i32.const 16) "\4d")
                (func (export "sigil_abi_version") (result i32) i32.const 1)
                (func (export "sigil_alloc") (param i32) (result i32) i32.const 4000)
                (func (export "sigil_free") (param i32 i32))
                (func (export "sigil_call") (param i32 i32) (result i64)
                    {call}
                    i32.const {errno}
                    i32.ne
                    if unreachable end
                    i64.const 8589934592011))"#
        );
        crate::Sigil::new(&Module::from_bytes(wat.as_bytes()).unwrap()).unwrap()
    }

    const POLL: &str = r#"(import "wasi_snapshot_preview1" "poll_oneoff" (func $call (param i32 i32 i32 i32) (result i32)))"#;
    const WRITE: &str = r#"(import "wasi_snapshot_preview1" "fd_write" (func $call (param i32 i32 i32 i32) (result i32)))"#;

    #[rstest]
    #[case::too_many_subscriptions(POLL, "i32.const 0 i32.const 0 i32.const -1 i32.const 0 call $call", 28)]
    #[case::no_subscriptions(POLL, "i32.const 0 i32.const 0 i32.const 0 i32.const 0 call $call", 28)]
    #[case::an_unknown_clock(POLL, "i32.const 0 i32.const 100 i32.const 1 i32.const 200 call $call", 28)]
    #[case::subscriptions_off_the_end_of_memory(POLL, "i32.const 65500 i32.const 100 i32.const 4 i32.const 200 call $call", 21)]
    #[case::too_many_iovecs(WRITE, "i32.const 2 i32.const 0 i32.const -1 i32.const 0 call $call", 28)]
    #[case::iovecs_off_the_end_of_memory(WRITE, "i32.const 2 i32.const -16 i32.const 4 i32.const 0 call $call", 21)]
    #[case::a_closed_descriptor(WRITE, "i32.const 7 i32.const 0 i32.const 1 i32.const 0 call $call", 8)]
    fn a_hostile_call_is_an_errno(#[case] import: &str, #[case] call: &str, #[case] errno: i32) {
        let sigil = guest(import, call, errno);
        let started = std::time::Instant::now();
        assert!(sigil.check(&[], &Default::default()).unwrap().is_empty());
        assert!(started.elapsed() < Duration::from_secs(2), "{:?}", started.elapsed());
    }
}

#[cfg(test)]
mod linger_tests {
    use super::*;

    fn parked(module: &Module) -> bool {
        lock(&module.ticker.shared.state).parked
    }

    fn wait_until(what: &str, mut cond: impl FnMut() -> bool) {
        let until = Instant::now() + Duration::from_secs(5);
        while !cond() {
            assert!(Instant::now() < until, "timed out waiting for {what}");
            thread::sleep(Duration::from_millis(5));
        }
    }

    #[test]
    fn the_ticker_sleeps_when_idle_stays_awake_between_calls_and_wakes_for_the_next_one() {
        let module = Module::from_bytes(b"(module)").unwrap();
        wait_until("the idle ticker to park", || parked(&module));

        // A call wakes it; calls in quick succession then find it awake and never wake it again.
        let first = module.ticker.watch();
        wait_until("the ticker to wake", || !parked(&module));
        drop(first);
        for _ in 0..20 {
            drop(module.ticker.watch());
            thread::sleep(Duration::from_millis(2));
            assert!(!parked(&module), "parked between calls closer than LINGER");
        }

        // Quiet for longer than LINGER: it parks again, and the next call still gets its clock.
        wait_until("the ticker to park again", || parked(&module));
        let wasm = wasmtime::Module::new(&module.engine, r#"(module (func (export "spin") (loop $l br $l)))"#).unwrap();
        let mut store = wasmtime::Store::new(&module.engine, ());
        store.epoch_deadline_trap();
        store.set_epoch_deadline(ticks(Duration::from_millis(20)));
        let spin = wasmtime::Instance::new(&mut store, &wasm, &[]).unwrap().get_typed_func::<(), ()>(&mut store, "spin").unwrap();
        let _watch = module.ticker.watch();
        let err = spin.call(&mut store, ()).unwrap_err();
        assert_eq!(err.downcast_ref::<wasmtime::Trap>(), Some(&wasmtime::Trap::Interrupt));
    }
}
