//! [`Pool`]: several instances of the engine, each holding the same policies,
//! for parallel evaluation.
//!
//! An instance handles one call at a time, so parallelism takes several
//! instances. The pool hands a free one to each caller and puts it back. An
//! instance that stops (the module trapped, a call was killed at its hard
//! deadline) is replaced by a fresh one with every policy compiled again, so
//! one runaway evaluation costs one instance and one failed call, not the
//! service.
//!
//! The API blocks. From async code, call it on a blocking thread:
//!
//! ```ignore
//! let pool = Arc::clone(&pool);
//! let result = tokio::task::spawn_blocking(move || pool.evaluate("flags.checkout", &input, &EvalOptions::default())).await??;
//! ```

use std::collections::{BTreeMap, HashMap};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use serde::Serialize;

use crate::error::Error;
use crate::module::{Limits, Module};
use crate::sigil::{Policy, Sigil};
use crate::types::{CompileOptions, EvalOptions, EvalResult, Explanation, SourceFile};

/// How a policy is compiled in an instance: run once per instance, and again
/// in every replacement. It may capture files and options; a host function it
/// passes is shared by all instances, so it must be `Send + Sync`.
type Recipe = dyn Fn(&Sigil) -> Result<Policy, Error> + Send + Sync;

/// Options of [`Pool::with_options`].
#[derive(Debug, Clone, Copy)]
pub struct PoolOptions {
    /// How many instances. Each holds its own copy of every policy and up to
    /// the module's memory, so size it for the cores the service has.
    pub size: usize,
    /// The limits of every instance.
    pub limits: Limits,
    /// How long a call waits for a free instance before it fails with
    /// [`Error::Busy`]; forever when `None`.
    pub acquire_timeout: Option<Duration>,
}

impl Default for PoolOptions {
    fn default() -> Self {
        Self { size: std::thread::available_parallelism().map_or(2, usize::from), limits: Limits::default(), acquire_timeout: None }
    }
}

/// A point-in-time view of a [`Pool`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PoolStats {
    pub size: usize,
    /// Instances waiting for a call.
    pub idle: usize,
    /// Policies installed.
    pub installed: usize,
    /// Instances replaced so far because they stopped.
    pub replaced: u64,
}

/// A pool of Sigil instances.
///
/// An instance handles one call at a time, so parallelism takes several
/// instances. The pool hands a free one to each caller and puts it back. An
/// instance that stops (the module trapped, a call was killed at its hard
/// deadline) is replaced by a fresh one with every policy compiled again, so
/// one runaway evaluation costs one instance and one failed call, not the
/// service.
///
/// `Pool` is `Send + Sync`: share it with an `Arc`. Its API blocks; from async
/// code call it on a blocking thread, with `tokio::task::spawn_blocking`.
pub struct Pool {
    module: Module,
    options: PoolOptions,
    state: Mutex<State>,
    available: Condvar,
    /// Serializes `install` and `remove`, which read and then change the recipes.
    installs: Mutex<()>,
    next_recipe: AtomicU64,
    replaced: AtomicU64,
}

struct State {
    idle: Vec<Slot>,
    recipes: BTreeMap<String, (u64, Arc<Recipe>)>,
}

/// One instance and the policies compiled in it.
struct Slot {
    id: usize,
    /// Declared before `policies` on purpose: fields drop in order, and a dropped
    /// `Sigil` makes its policies skip their releases, so dropping a pool of many
    /// policies costs no ops.
    sigil: Sigil,
    policies: HashMap<String, (u64, Policy)>,
    /// The instance stopped: it's replaced before its next use.
    dead: bool,
}

/// A slot checked out of the pool, returned on drop.
struct Lease<'a> {
    pool: &'a Pool,
    slot: Option<Slot>,
}

impl Pool {
    /// A pool of `size` instances of `module` with the default limits.
    pub fn new(module: &Module, size: usize) -> Result<Self, Error> {
        Self::with_options(module, PoolOptions { size, ..PoolOptions::default() })
    }

    /// A pool with [`PoolOptions`].
    pub fn with_options(module: &Module, options: PoolOptions) -> Result<Self, Error> {
        if options.size == 0 {
            return Err(Error::sigil("a pool needs at least one instance", "set PoolOptions::size to 1 or more"));
        }
        let mut idle = Vec::with_capacity(options.size);
        for id in 0..options.size {
            idle.push(Slot { id, sigil: Sigil::with_limits(module, options.limits)?, policies: HashMap::new(), dead: false });
        }
        Ok(Self {
            module: module.clone(),
            options,
            state: Mutex::new(State { idle, recipes: BTreeMap::new() }),
            available: Condvar::new(),
            installs: Mutex::new(()),
            next_recipe: AtomicU64::new(1),
            replaced: AtomicU64::new(0),
        })
    }

    /// Installs a policy under `name`, replacing one of that name: `recipe`
    /// compiles it in an instance, and runs once in each. The first instance
    /// validates it, so a recipe that fails leaves the pool as it was and
    /// returns its error. The other instances follow one at a time while the
    /// rest keep serving, so for a moment some evaluations see the old policy
    /// and some the new; none sees a half-installed one.
    pub fn install<F>(&self, name: &str, recipe: F) -> Result<(), Error>
    where
        F: Fn(&Sigil) -> Result<Policy, Error> + Send + Sync + 'static,
    {
        let _serial = lock(&self.installs);
        let recipe: Arc<Recipe> = Arc::new(recipe);
        let id = self.next_recipe.fetch_add(1, Ordering::Relaxed);

        let mut first = self.acquire(None)?;
        first.ensure_alive();
        let policy = recipe(&first.slot().sigil);
        let policy = match policy {
            Ok(policy) => policy,
            Err(err) => {
                first.note(&err);
                return Err(err);
            }
        };
        lock(&self.state).recipes.insert(name.to_string(), (id, recipe));
        first.slot_mut().policies.insert(name.to_string(), (id, policy));
        let first_id = first.slot().id;
        drop(first);
        self.roll_out(Some(first_id));
        Ok(())
    }

    /// Compiles `policy` of `files` in every instance: [`Pool::install`] with
    /// [`Sigil::compile`] as the recipe.
    pub fn compile(&self, name: &str, files: &[SourceFile], options: CompileOptions) -> Result<(), Error> {
        let files = files.to_vec();
        self.install(name, move |sigil| sigil.compile(&files, options.clone()))
    }

    /// Removes a policy from every instance; whether there was one.
    pub fn remove(&self, name: &str) -> bool {
        let _serial = lock(&self.installs);
        let removed = lock(&self.state).recipes.remove(name).is_some();
        if removed {
            self.roll_out(None);
        }
        removed
    }

    /// The installed policies' names, sorted.
    pub fn names(&self) -> Vec<String> {
        lock(&self.state).recipes.keys().cloned().collect()
    }

    /// Evaluates the policy `name` on a free instance, waiting for one.
    ///
    /// When the call stops the instance (see [`Error::is_stopped`]), the
    /// instance is replaced before this returns, and the error is this
    /// caller's; the next call runs on a fresh instance.
    pub fn evaluate<I: Serialize + ?Sized>(&self, name: &str, input: &I, options: &EvalOptions) -> Result<EvalResult, Error> {
        self.with_policy(name, |policy| policy.eval_with(input, options))
    }

    /// Explains the policy `name`, like [`Policy::explain`].
    pub fn explain(&self, name: &str) -> Result<Explanation, Error> {
        self.with_policy(name, Policy::explain)
    }

    /// Runs `f` with the policy `name` of a free instance, waiting for one. An
    /// error of `f` that stops the instance (see [`Error::is_stopped`]) gets the
    /// instance replaced before this returns.
    pub fn with_policy<T>(&self, name: &str, f: impl FnOnce(&Policy) -> Result<T, Error>) -> Result<T, Error> {
        let mut lease = self.acquire(self.options.acquire_timeout)?;
        lease.ensure_alive();
        let failures = lease.sync(self);
        let result = match lease.slot().policies.get(name) {
            Some((_, policy)) => f(policy),
            // A recipe that fails here failed for the first instance too only
            // if it is nondeterministic; report what this instance says.
            None => Err(failures.into_iter().find(|(n, _)| n == name).map_or_else(|| Error::NoPolicy(name.to_string()), |(_, e)| e)),
        };
        if let Err(err) = &result {
            lease.note(err);
        }
        result
    }

    /// A point-in-time view of the pool.
    pub fn stats(&self) -> PoolStats {
        let state = lock(&self.state);
        PoolStats {
            size: self.options.size,
            idle: state.idle.len(),
            installed: state.recipes.len(),
            replaced: self.replaced.load(Ordering::Relaxed),
        }
    }

    /// Brings every instance but `skip` up to date, one at a time.
    fn roll_out(&self, skip: Option<usize>) {
        for id in 0..self.options.size {
            if Some(id) == skip {
                continue;
            }
            if let Ok(mut lease) = self.acquire_id(id) {
                lease.ensure_alive();
                // A failure here is retried when the instance is next used.
                let _ = lease.sync(self);
            }
        }
    }

    /// Waits for any free instance.
    fn acquire(&self, timeout: Option<Duration>) -> Result<Lease<'_>, Error> {
        let start = Instant::now();
        let mut state = lock(&self.state);
        loop {
            if let Some(slot) = state.idle.pop() {
                return Ok(Lease { pool: self, slot: Some(slot) });
            }
            state = self.wait(state, timeout, start)?;
        }
    }

    /// Waits for the instance numbered `id`.
    fn acquire_id(&self, id: usize) -> Result<Lease<'_>, Error> {
        let start = Instant::now();
        let mut state = lock(&self.state);
        loop {
            if let Some(at) = state.idle.iter().position(|s| s.id == id) {
                let slot = state.idle.swap_remove(at);
                return Ok(Lease { pool: self, slot: Some(slot) });
            }
            state = self.wait(state, None, start)?;
        }
    }

    fn wait<'a>(&'a self, state: MutexGuard<'a, State>, timeout: Option<Duration>, start: Instant) -> Result<MutexGuard<'a, State>, Error> {
        match timeout {
            None => Ok(self.available.wait(state).unwrap_or_else(|e| e.into_inner())),
            Some(limit) => {
                let left = limit.checked_sub(start.elapsed()).ok_or(Error::Busy(limit))?;
                Ok(self.available.wait_timeout(state, left).unwrap_or_else(|e| e.into_inner()).0)
            }
        }
    }
}

impl std::fmt::Debug for Pool {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Pool").field("stats", &self.stats()).finish()
    }
}

impl Lease<'_> {
    fn slot(&self) -> &Slot {
        self.slot.as_ref().expect("a lease holds its slot until dropped")
    }

    fn slot_mut(&mut self) -> &mut Slot {
        self.slot.as_mut().expect("a lease holds its slot until dropped")
    }

    /// Marks the instance dead when `err` stopped it, and replaces it now, so
    /// the next call finds a working one.
    fn note(&mut self, err: &Error) {
        if err.is_stopped() {
            self.slot_mut().dead = true;
            self.pool.replaced.fetch_add(1, Ordering::Relaxed);
            self.ensure_alive();
        }
    }

    /// Replaces a dead instance by a fresh one with every policy compiled.
    fn ensure_alive(&mut self) {
        let pool = self.pool;
        let slot = self.slot.as_mut().expect("a lease holds its slot until dropped");
        if !slot.dead && slot.sigil.stopped().is_none() {
            return;
        }
        // An instance that stopped without a call noticing (a policy dropped
        // after it died, say) is replaced just the same, but counted here.
        if !slot.dead {
            pool.replaced.fetch_add(1, Ordering::Relaxed);
        }
        match Sigil::with_limits(&pool.module, pool.options.limits) {
            Ok(sigil) => {
                // The old policies belong to the dead instance: dropping them
                // skips the release, there's nothing left to free.
                slot.policies.clear();
                slot.sigil = sigil;
                slot.dead = false;
            }
            // Out of memory, most likely: stay dead, try again at the next use.
            Err(_) => slot.dead = true,
        }
    }

    /// Makes the instance's policies match the pool's recipes; the failures
    /// of recipes that didn't compile.
    fn sync(&mut self, pool: &Pool) -> Vec<(String, Error)> {
        if self.slot().dead {
            return Vec::new();
        }
        let recipes: Vec<(String, u64, Arc<Recipe>)> =
            lock(&pool.state).recipes.iter().map(|(name, (id, recipe))| (name.clone(), *id, Arc::clone(recipe))).collect();
        let slot = self.slot_mut();
        slot.policies.retain(|name, _| recipes.iter().any(|(n, _, _)| n == name));
        let mut failures = Vec::new();
        for (name, id, recipe) in recipes {
            if slot.policies.get(&name).is_some_and(|(have, _)| *have == id) {
                continue;
            }
            match recipe(&slot.sigil) {
                Ok(policy) => {
                    slot.policies.insert(name, (id, policy));
                }
                Err(err) => {
                    slot.policies.remove(&name);
                    if err.is_stopped() {
                        slot.dead = true;
                        pool.replaced.fetch_add(1, Ordering::Relaxed);
                    }
                    failures.push((name, err));
                }
            }
        }
        failures
    }
}

impl Drop for Lease<'_> {
    fn drop(&mut self) {
        let Some(mut slot) = self.slot.take() else { return };
        // A panic mid-call (a host function's, say, outside the catch) may
        // have left the instance anywhere.
        if std::thread::panicking() && !slot.dead {
            slot.dead = true;
            self.pool.replaced.fetch_add(1, Ordering::Relaxed);
        }
        lock(&self.pool.state).idle.push(slot);
        self.pool.available.notify_all();
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}
