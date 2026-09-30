//! Loading the module: compile once, instantiate cheaply per [`crate::Sigil`].

use std::path::Path;
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use wasmtime::{Engine, InstancePre, Linker};

use crate::error::{Error, SigilError};
use crate::runtime::{self, State};

/// How often the epoch ticker advances the engine's clock while a call with a
/// deadline runs. A hard deadline fires within about this much of its time.
const TICK: Duration = Duration::from_millis(2);

/// The imports the module may have, besides the WASI preview 1 ones.
const HOST_MODULE: &str = "sigil";
const WASI_MODULE: &str = "wasi_snapshot_preview1";

/// The compiled module: an `Engine` and a `Module`, shared by every instance.
/// Compiling takes seconds (the module is 11 MB); do it once, clone the
/// `Module` (cheap, reference counted), and give each [`crate::Sigil`] or
/// [`crate::Pool`] one.
#[derive(Clone)]
pub struct Module {
    pub(crate) engine: Engine,
    pub(crate) pre: InstancePre<State>,
    pub(crate) ticker: Arc<Ticker>,
    pub(crate) fuel: bool,
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
        let mut wasm = wasmtime::Config::new();
        // Epoch interruption is what kills a call from outside; it costs a
        // load and a compare at loop headers and function entries.
        wasm.epoch_interruption(true);
        wasm.consume_fuel(config.fuel);
        let engine = Engine::new(&wasm).map_err(|err| {
            Error::Sigil(
                SigilError::new(format!("configuring wasmtime: {err}")).with_help("this is a bug in the binding; please report it"),
            )
        })?;
        let module = wasmtime::Module::new(&engine, bytes).map_err(|err| {
            Error::Sigil(
                SigilError::new(format!("the bytes aren't a WebAssembly module wasmtime can compile: {err}"))
                    .with_help("build sigil.wasm with `mise run wasm-build` (GOOS=wasip1, -buildmode=c-shared)"),
            )
        })?;
        for import in module.imports() {
            let known = match import.module() {
                WASI_MODULE => true,
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
        Ok(Self { engine, pre, ticker, fuel: config.fuel })
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
        Self::from_bytes(crate::bundled::WASM)
    }

    /// [`Module::bundled`] with a [`ModuleConfig`].
    #[cfg(feature = "bundled")]
    pub fn bundled_with(config: ModuleConfig) -> Result<Self, Error> {
        Self::from_bytes_with(crate::bundled::WASM, config)
    }

    /// Whether the module meters fuel.
    pub fn fuel(&self) -> bool {
        self.fuel
    }
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
        self.shared.changed.notify_all();
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
    loop {
        while state.watching == 0 && !state.stop {
            due = None;
            state = shared.changed.wait(state).unwrap_or_else(|e| e.into_inner());
        }
        if state.stop {
            return;
        }
        let now = Instant::now();
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
