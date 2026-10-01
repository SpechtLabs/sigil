//! [`Sigil`] and [`Policy`]: one instance of the engine, and a policy compiled
//! in it.

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::thread::{self, ThreadId};

use serde::{Deserialize, Serialize};

use crate::error::{Error, StoppedError};
use crate::kind::ResultShape;
use crate::module::{Limits, Module};
use crate::runtime::{CallLimits, Runtime};
use crate::types::{
    CheckOptions, CompileOptions, Diagnostic, EvalOptions, EvalResult, ExplainOptions, Explanation, FormatOptions, HostFunction,
    SourceFile, VersionInfo,
};

/// One instance of the Sigil engine: a wasmtime `Store` with the module in it.
/// The answers are the stock `sigil` CLI's for the same files: `check` is
/// `sigil check -o json`, `eval` is `sigil eval -o json`, and so on.
///
/// An instance handles one call at a time, so `Sigil` is `Send` and `Sync`
/// but serializes its calls: a second thread waits for the first. For
/// parallelism run several instances, which [`crate::Pool`] does. A host
/// function can't call back into the instance that runs it; that call fails
/// with an error instead of deadlocking.
///
/// An instance that stops (Go's runtime exits, the module traps, a call is
/// killed at its hard deadline) is dead for good: see [`Sigil::stopped`].
pub struct Sigil {
    shared: Arc<Shared>,
    limits: Limits,
}

/// What a `Sigil` and its policies share.
pub(crate) struct Shared {
    runtime: Mutex<Runtime>,
    /// The thread that holds `runtime`, to tell a host function's call back
    /// into the instance (an error) from another thread's (which waits).
    owner: Arc<Mutex<Option<ThreadId>>>,
    stopped: Arc<OnceLock<StoppedError>>,
    /// The `Sigil` is gone, so the instance goes when its last policy does: a
    /// policy dropped now skips its `release` op, which would only tidy memory
    /// about to be freed. It makes dropping a pool of many policies cheap.
    retiring: AtomicBool,
    /// `release` ops run, for tests.
    releases: AtomicUsize,
}

/// A compiled policy, held inside the module. Evaluating it sends only the
/// input across. It releases its handle when dropped; [`Policy::release`]
/// does it explicitly and reports a failure.
pub struct Policy {
    shared: Arc<Shared>,
    limits: Limits,
    handle: u32,
    name: String,
    diagnostics: Vec<Diagnostic>,
    functions: Arc<HashMap<String, HostFunction>>,
    shape: Option<ResultShape>,
}

#[derive(Deserialize)]
struct Empty {}

#[derive(Deserialize)]
struct DiagnosticsBody {
    #[serde(default)]
    diagnostics: Vec<Diagnostic>,
}

#[derive(Deserialize)]
struct CompiledBody {
    handle: u32,
    policy: String,
    #[serde(default)]
    diagnostics: Vec<Diagnostic>,
}

#[derive(Deserialize)]
struct ExplainBody {
    #[serde(default)]
    explanations: Vec<Explanation>,
}

#[derive(Deserialize)]
struct FormatBody {
    source: String,
}

impl Sigil {
    /// Instantiates the module and initializes the Go runtime.
    pub fn new(module: &Module) -> Result<Self, Error> {
        Self::with_limits(module, Limits::default())
    }

    /// [`Sigil::new`] with [`Limits`].
    pub fn with_limits(module: &Module, limits: Limits) -> Result<Self, Error> {
        let runtime = Runtime::new(module, &limits)?;
        let stopped = runtime.stopped_handle();
        let owner = runtime.owner_handle();
        let shared = Arc::new(Shared {
            runtime: Mutex::new(runtime),
            owner,
            stopped,
            retiring: AtomicBool::new(false),
            releases: AtomicUsize::new(0),
        });
        Ok(Self { shared, limits })
    }

    /// Compiles the bundled module and instantiates it. That compile takes
    /// seconds: for more than one instance, compile with [`Module::bundled`]
    /// once and use [`Sigil::new`].
    #[cfg(feature = "bundled")]
    pub fn bundled() -> Result<Self, Error> {
        Self::new(&Module::bundled()?)
    }

    /// The error every call returns once the instance has stopped, or `None`
    /// while it works. A host that keeps an instance for long checks this (or
    /// an error's [`Error::is_stopped`]) and builds a new one.
    pub fn stopped(&self) -> Option<StoppedError> {
        self.shared.stopped.get().cloned()
    }

    /// The module's version and build information.
    pub fn version(&self) -> Result<VersionInfo, Error> {
        self.op("version", serde_json::json!({}))
    }

    /// Checks the files like `sigil check`: syntax, types, the kind, the
    /// requirements and the lints. Errors and warnings alike come back as
    /// diagnostics; it fails only for a request the module can't handle.
    pub fn check(&self, files: &[SourceFile], options: &CheckOptions) -> Result<Vec<Diagnostic>, Error> {
        #[derive(Serialize)]
        struct Request<'a> {
            files: &'a [SourceFile],
            #[serde(flatten)]
            options: &'a CheckOptions,
        }
        Ok(self.op::<DiagnosticsBody>("check", Request { files, options })?.diagnostics)
    }

    /// Compiles one policy of the files into a [`Policy`] to evaluate. Every
    /// document of the policy's kind among the files and the trusted files
    /// must check, even one the policy never uses, as with Go's `Kind.Load`:
    /// the compile fails with the diagnostics `check` reports for them
    /// otherwise, and so does a document of a kind the files don't provide.
    pub fn compile(&self, files: &[SourceFile], options: CompileOptions) -> Result<Policy, Error> {
        #[derive(Serialize)]
        struct Request<'a> {
            files: &'a [SourceFile],
            #[serde(skip_serializing_if = "Option::is_none")]
            policy: &'a Option<String>,
            #[serde(skip_serializing_if = "Vec::is_empty")]
            require: &'a Vec<crate::types::CompileRequirement>,
            #[serde(skip_serializing_if = "Vec::is_empty")]
            trusted_files: &'a Vec<SourceFile>,
            #[serde(skip_serializing_if = "std::collections::BTreeMap::is_empty")]
            stubs: &'a std::collections::BTreeMap<String, crate::types::Stub>,
            #[serde(skip_serializing_if = "Vec::is_empty")]
            functions: Vec<&'a String>,
        }
        let mut names: Vec<&String> = options.functions.keys().collect();
        names.sort();
        let request = Request {
            files,
            policy: &options.policy,
            require: &options.require,
            trusted_files: &options.trusted_files,
            stubs: &options.stubs,
            functions: names,
        };
        let body: CompiledBody = self.op("compile", request)?;
        Ok(Policy {
            shared: Arc::clone(&self.shared),
            limits: self.limits,
            handle: body.handle,
            name: body.policy,
            diagnostics: body.diagnostics,
            functions: Arc::new(options.functions),
            shape: None,
        })
    }

    /// Explains the files' policies like `sigil explain`: every rule and
    /// assert each can reach, with its conditions and call chain.
    pub fn explain(&self, files: &[SourceFile], options: &ExplainOptions) -> Result<Vec<Explanation>, Error> {
        #[derive(Serialize)]
        struct Request<'a> {
            files: &'a [SourceFile],
            #[serde(flatten)]
            options: &'a ExplainOptions,
        }
        Ok(self.op::<ExplainBody>("explain", Request { files, options })?.explanations)
    }

    /// Formats a source in `sigil fmt`'s canonical style. Fails with the
    /// syntax errors as diagnostics when it doesn't parse.
    pub fn format(&self, source: &str, options: &FormatOptions) -> Result<String, Error> {
        #[derive(Serialize)]
        struct Request<'a> {
            source: &'a str,
            #[serde(skip_serializing_if = "Option::is_none")]
            path: &'a Option<String>,
        }
        Ok(self.op::<FormatBody>("format", Request { source, path: &options.path })?.source)
    }

    /// Bytes of linear memory the instance holds, for watching it over time.
    pub fn memory_size(&self) -> Result<usize, Error> {
        self.shared.with(|rt| Ok(rt.memory_size()))
    }

    fn op<R: serde::de::DeserializeOwned>(&self, op: &str, fields: impl Serialize) -> Result<R, Error> {
        let limits = CallLimits { deadline: self.limits.op_deadline, fuel: None };
        self.shared.with(|rt| rt.op(op, fields, None, limits))
    }
}

impl std::fmt::Debug for Sigil {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Sigil").field("stopped", &self.shared.stopped.get().is_some()).finish()
    }
}

impl Policy {
    /// The policy's name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Problems in documents the policy doesn't use. `compile` checks the
    /// whole bundle and fails on any error, so this holds warnings at most.
    pub fn diagnostics(&self) -> &[Diagnostic] {
        &self.diagnostics
    }

    /// The module's handle for the policy.
    pub fn handle(&self) -> u32 {
        self.handle
    }

    /// Evaluates the policy against one input, which is anything serde can
    /// serialize to the kind's inputs: a struct, or a `serde_json::Value`.
    ///
    /// A failed evaluation (a runtime error, a conflict, a failing assert)
    /// still returns a result, with `error` set and the kind's fallback as the
    /// outcome. It fails with an [`Error`] for an input that doesn't fit the
    /// kind, and with a stopped-kind error when the instance is dead.
    pub fn eval<I: Serialize + ?Sized>(&self, input: &I) -> Result<EvalResult, Error> {
        self.eval_with(input, &EvalOptions::default())
    }

    /// [`Policy::eval`] with a timeout, a grace period and fuel.
    ///
    /// The `timeout` is the ABI's: the module checks it while it evaluates and
    /// answers with a `canceled` failure. A call that runs past `timeout` plus
    /// the grace period is killed from outside with epoch interruption, which
    /// at the next epoch check, and fails with [`Error::Timeout`]; the instance
    /// is stopped from then on. The kill lands when control is back in
    /// WebAssembly: a host function blocked in native code is not interrupted,
    /// only stopped once it returns, so give one that does I/O its own timeout.
    pub fn eval_with<I: Serialize + ?Sized>(&self, input: &I, options: &EvalOptions) -> Result<EvalResult, Error> {
        #[derive(Serialize)]
        struct Request<'a, I: Serialize + ?Sized> {
            handle: u32,
            input: &'a I,
            #[serde(skip_serializing_if = "Option::is_none")]
            timeout_ms: Option<u64>,
        }
        let timeout_ms = options
            .timeout
            .map(|t| u64::try_from(t.as_millis() + u128::from(t.subsec_nanos() % 1_000_000 != 0)).unwrap_or(u64::MAX).max(1));
        let deadline = match options.timeout {
            Some(t) => Some(t + options.grace.unwrap_or(self.limits.grace)),
            None => self.limits.op_deadline,
        };
        let limits = CallLimits { deadline, fuel: options.fuel };
        let request = Request { handle: self.handle, input, timeout_ms };
        let mut result: EvalResult = self.shared.with(|rt| rt.op("eval", request, Some(Arc::clone(&self.functions)), limits))?;
        result.shape = self.shape;
        Ok(result)
    }

    /// Explains the policy like `sigil explain`.
    pub fn explain(&self) -> Result<Explanation, Error> {
        #[derive(Serialize)]
        struct Request {
            handle: u32,
        }
        let limits = CallLimits { deadline: self.limits.op_deadline, fuel: None };
        let body: ExplainBody = self.shared.with(|rt| rt.op("explain", Request { handle: self.handle }, None, limits))?;
        body.explanations.into_iter().next().ok_or_else(|| {
            Error::sigil(format!("the module returned no explanation for policy {}", self.name), "this is a bug in Sigil; please report it")
        })
    }

    /// Frees the policy inside the module and reports a failure; dropping it
    /// frees it too, and ignores one.
    pub fn release(self) -> Result<(), Error> {
        let result = self.release_handle();
        std::mem::forget(self);
        result
    }

    /// Reads the result of this policy's kind: set by [`crate::Kind::compile`].
    pub(crate) fn with_shape(mut self, shape: ResultShape) -> Self {
        self.shape = Some(shape);
        self
    }

    fn release_handle(&self) -> Result<(), Error> {
        if self.shared.stopped.get().is_some() || self.shared.retiring.load(Ordering::Relaxed) {
            // Nothing to free in a dead instance, and no point in tidying one
            // that is being dropped.
            return Ok(());
        }
        self.shared.releases.fetch_add(1, Ordering::Relaxed);
        #[derive(Serialize)]
        struct Request {
            handle: u32,
        }
        let limits = CallLimits { deadline: self.limits.op_deadline, fuel: None };
        self.shared.with(|rt| rt.op::<Empty>("release", Request { handle: self.handle }, None, limits)).map(|_| ())
    }
}

impl Drop for Sigil {
    fn drop(&mut self) {
        // Policies that outlive the `Sigil` keep the instance alive but can no
        // longer be joined by new ones, so their releases are moot.
        self.shared.retiring.store(true, Ordering::Relaxed);
    }
}

impl Drop for Policy {
    fn drop(&mut self) {
        // Dropped from inside a host function of the same instance, or while
        // the instance is busy on another thread: the release waits its turn
        // except when that would deadlock, in which case the handle stays
        // until the instance goes (a leak of one compiled policy).
        let _ = self.release_handle();
    }
}

impl std::fmt::Debug for Policy {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Policy").field("name", &self.name).field("handle", &self.handle).finish()
    }
}

impl Shared {
    /// Runs `f` against the runtime, on this thread, one call at a time.
    fn with<T>(&self, f: impl FnOnce(&mut Runtime) -> Result<T, Error>) -> Result<T, Error> {
        let me = thread::current().id();
        if *self.owner.lock().unwrap_or_else(|e| e.into_inner()) == Some(me) {
            return Err(Error::sigil(
                "the module is busy: a host function can't call back into the Sigil instance that runs it",
                "use another instance for the nested call, or compute the value before the evaluation",
            ));
        }
        let mut runtime = self.runtime.lock().unwrap_or_else(|e| e.into_inner());
        f(&mut runtime)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const KIND: &str = "kind Minimal version 1\n\ninput who: string\n\ndecision ok {\n  reason: yes\n}\n\ncollect one\nprecedence ok\n\ndefault ok(reason: yes)\n";
    const POLICY: &str = "policy m.p: Minimal@1\n\nwhen true {\n  ok(reason: yes)\n}\n";

    fn files() -> Vec<SourceFile> {
        vec![SourceFile::new("minimal.sigil", KIND), SourceFile::new("p.sigil", POLICY)]
    }

    #[test]
    fn dropping_a_policy_releases_it_but_dropping_the_sigil_first_skips_the_releases() {
        let module = Module::from_file(concat!(env!("CARGO_MANIFEST_DIR"), "/../../dist/wasm/sigil.wasm")).unwrap();
        let sigil = Sigil::new(&module).unwrap();
        let shared = Arc::clone(&sigil.shared);
        let options = || CompileOptions { policy: Some("m.p".into()), ..Default::default() };
        let (a, b, c) = (
            sigil.compile(&files(), options()).unwrap(),
            sigil.compile(&files(), options()).unwrap(),
            sigil.compile(&files(), options()).unwrap(),
        );

        drop(a);
        assert_eq!(shared.releases.load(Ordering::Relaxed), 1, "an ordinary drop releases");
        b.release().unwrap();
        assert_eq!(shared.releases.load(Ordering::Relaxed), 2);

        drop(sigil);
        // The last policy still evaluates, and its drop skips the release.
        assert!(c.eval(&serde_json::json!({"who": "x"})).unwrap().error.is_none());
        drop(c);
        assert_eq!(shared.releases.load(Ordering::Relaxed), 2, "a drop after the sigil's skips the op");
    }
}
