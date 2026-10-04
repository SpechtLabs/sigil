//! [`Pool`]: several instances of the engine, each holding the same policies,
//! for parallel evaluation.
//!
//! An instance handles one call at a time, so parallelism takes several
//! instances. The pool hands a free one to each caller and puts it back. An
//! instance that stops (the module trapped, a call was killed at its hard
//! deadline) is rebuilt on a thread of its own, with every policy compiled
//! again, while its caller gets the error at once: one runaway evaluation costs
//! one instance for a moment and one failed call, not the service.
//!
//! The API blocks. With the `tokio` feature, [`Pool::evaluate_async`] does the
//! `spawn_blocking` for you; without it, call on a blocking thread:
//!
//! ```ignore
//! let pool = pool.clone();
//! let result = tokio::task::spawn_blocking(move || pool.evaluate("flags.checkout", &input, &EvalOptions::default())).await;
//! ```

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread;
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

/// The recipes a rebuild or a sync works through: name, id, recipe.
type Recipes = Vec<(String, u64, Arc<Recipe>)>;

/// How long a rebuild waits before it tries to instantiate again, at first;
/// it doubles up to [`REBUILD_BACKOFF_MAX`].
const REBUILD_BACKOFF: Duration = Duration::from_millis(50);
const REBUILD_BACKOFF_MAX: Duration = Duration::from_secs(2);
/// How many fresh instances in a row one recipe may stop before a rebuild
/// leaves it off the slot.
const MAX_RECIPE_STOPS: u32 = 3;

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
        Self { size: thread::available_parallelism().map_or(2, usize::from), limits: Limits::default(), acquire_timeout: None }
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
    /// Instances being rebuilt right now: the pool serves with `size - rebuilding`
    /// of them until they are back.
    pub rebuilding: usize,
}

/// A pool of Sigil instances.
///
/// An instance handles one call at a time, so parallelism takes several
/// instances. The pool hands a free one to each caller and puts it back. An
/// instance that stops (the module trapped, a call was killed at its hard
/// deadline) is rebuilt on a thread of its own, with every policy compiled
/// again, so one runaway evaluation costs one instance for a moment and one
/// failed call, not the service. The call that stopped it returns its error
/// without waiting for the rebuild; meanwhile the pool is one instance smaller,
/// which [`PoolStats::rebuilding`] shows.
///
/// `Pool` is a cheap handle: clone it to share it, or share it with an `Arc`.
/// It is `Send + Sync`. Its API blocks; from async code call it on a blocking
/// thread, or use the `tokio` feature's `evaluate_async`. Dropping the last
/// handle lets rebuilds in flight finish and discard their instance.
#[derive(Clone)]
pub struct Pool {
    inner: Arc<Inner>,
    /// Tells the rebuild threads, which hold `inner` too, that no handle is left.
    _closer: Arc<Closer>,
}

struct Closer(Arc<Inner>);

struct Inner {
    module: Module,
    options: PoolOptions,
    state: Mutex<State>,
    available: Condvar,
    /// Serializes `install` and `remove`, which read and then change the recipes.
    installs: Mutex<()>,
    next_recipe: AtomicU64,
    replaced: AtomicU64,
    /// No `Pool` handle is left: rebuilds stop and drop what they build.
    closed: AtomicBool,
    /// `State::generation`, readable without the lock: a lease whose slot has
    /// already compiled this generation has nothing to sync, which is nearly
    /// every lease.
    generation: AtomicU64,
}

struct State {
    idle: Vec<Slot>,
    recipes: BTreeMap<String, (u64, Arc<Recipe>)>,
    /// Bumped with every change of `recipes`, so a rebuild that finishes can
    /// tell whether it compiled the current ones.
    generation: u64,
    /// The ids of the slots being rebuilt, which are neither idle nor leased.
    rebuilding: HashSet<usize>,
    /// Policies a rebuild left off its slot because they kept stopping fresh
    /// instances: name to the last error.
    skipped: BTreeMap<String, String>,
}

/// One instance and the policies compiled in it.
struct Slot {
    id: usize,
    /// Declared before `policies` on purpose: fields drop in order, and a dropped
    /// `Sigil` makes its policies skip their releases, so dropping a pool of many
    /// policies costs no ops.
    sigil: Sigil,
    policies: HashMap<String, (u64, Policy)>,
    /// The instance stopped: it's rebuilt instead of going back to idle.
    dead: bool,
    /// The generation of the recipes the policies were last synced to, when
    /// every recipe compiled; 0 before any.
    synced: u64,
}

/// A slot checked out of the pool, returned (or sent to be rebuilt) on drop.
struct Lease {
    pool: Arc<Inner>,
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
            idle.push(Slot { id, sigil: Sigil::with_limits(module, options.limits)?, policies: HashMap::new(), dead: false, synced: 0 });
        }
        let inner = Arc::new(Inner {
            module: module.clone(),
            options,
            state: Mutex::new(State {
                idle,
                recipes: BTreeMap::new(),
                generation: 0,
                rebuilding: HashSet::new(),
                skipped: BTreeMap::new(),
            }),
            available: Condvar::new(),
            installs: Mutex::new(()),
            next_recipe: AtomicU64::new(1),
            replaced: AtomicU64::new(0),
            closed: AtomicBool::new(false),
            generation: AtomicU64::new(0),
        });
        Ok(Self { _closer: Arc::new(Closer(Arc::clone(&inner))), inner })
    }

    /// Installs a policy under `name`, replacing one of that name: `recipe`
    /// compiles it in an instance, and runs once in each. The first instance
    /// validates it, so a recipe that fails leaves the pool as it was and
    /// returns its error. The other instances follow one at a time while the
    /// rest keep serving, so for a moment some evaluations see the old policy
    /// and some the new; none sees a half-installed one. An instance being
    /// rebuilt picks the new recipe up before it returns to service.
    pub fn install<F>(&self, name: &str, recipe: F) -> Result<(), Error>
    where
        F: Fn(&Sigil) -> Result<Policy, Error> + Send + Sync + 'static,
    {
        let inner = &self.inner;
        let _serial = lock(&inner.installs);
        let recipe: Arc<Recipe> = Arc::new(recipe);
        let id = inner.next_recipe.fetch_add(1, Ordering::Relaxed);

        let mut first = inner.acquire(None)?;
        let policy = recipe(&first.slot().sigil);
        let policy = match policy {
            Ok(policy) => policy,
            Err(err) => {
                first.note(&err);
                return Err(err);
            }
        };
        {
            let mut state = lock(&inner.state);
            state.recipes.insert(name.to_string(), (id, recipe));
            state.skipped.remove(name);
            state.generation += 1;
            inner.generation.store(state.generation, Ordering::Release);
        }
        first.slot_mut().policies.insert(name.to_string(), (id, policy));
        let first_id = first.slot().id;
        drop(first);
        inner.roll_out(Some(first_id));
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
        let inner = &self.inner;
        let _serial = lock(&inner.installs);
        let removed = {
            let mut state = lock(&inner.state);
            let removed = state.recipes.remove(name).is_some();
            state.skipped.remove(name);
            if removed {
                state.generation += 1;
                inner.generation.store(state.generation, Ordering::Release);
            }
            removed
        };
        if removed {
            inner.roll_out(None);
        }
        removed
    }

    /// The installed policies' names, sorted.
    pub fn names(&self) -> Vec<String> {
        lock(&self.inner.state).recipes.keys().cloned().collect()
    }

    /// Evaluates the policy `name` on a free instance, waiting for one.
    ///
    /// When the call stops the instance (see [`Error::is_stopped`]), this
    /// returns its error at once and the instance is rebuilt in the background;
    /// the pool serves with one instance fewer until it is back.
    pub fn evaluate<I: Serialize + ?Sized>(&self, name: &str, input: &I, options: &EvalOptions) -> Result<EvalResult, Error> {
        self.with_policy(name, |policy| policy.eval_with(input, options))
    }

    /// Explains the policy `name`, like [`Policy::explain`].
    pub fn explain(&self, name: &str) -> Result<Explanation, Error> {
        self.with_policy(name, Policy::explain)
    }

    /// Runs `f` with the policy `name` of a free instance, waiting for one. An
    /// error of `f` that stops the instance (see [`Error::is_stopped`]) sends
    /// the instance to be rebuilt.
    pub fn with_policy<T>(&self, name: &str, f: impl FnOnce(&Policy) -> Result<T, Error>) -> Result<T, Error> {
        let mut lease = self.inner.acquire(self.inner.options.acquire_timeout)?;
        let failures = lease.sync();
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

    /// The policies a rebuild left off a rebuilt instance because compiling them
    /// kept stopping fresh instances (they exhaust `Limits::max_memory`, run into
    /// a deadline, trap): the policy's name and the last error. Empty when every
    /// rebuild compiled everything. An install or remove of the name clears it.
    pub fn rebuild_failures(&self) -> Vec<(String, String)> {
        lock(&self.inner.state).skipped.iter().map(|(n, e)| (n.clone(), e.clone())).collect()
    }

    /// A point-in-time view of the pool.
    pub fn stats(&self) -> PoolStats {
        let state = lock(&self.inner.state);
        PoolStats {
            size: self.inner.options.size,
            idle: state.idle.len(),
            installed: state.recipes.len(),
            replaced: self.inner.replaced.load(Ordering::Relaxed),
            rebuilding: state.rebuilding.len(),
        }
    }
}

#[cfg(feature = "tokio")]
impl Pool {
    /// [`Pool::evaluate`] on tokio's blocking pool: the call waits for a free
    /// instance and evaluates on a blocking thread, so an async task never
    /// blocks a worker. The input is moved into the task.
    ///
    /// # Panics
    ///
    /// Never: a panic in the blocking task (a host function's is caught inside
    /// and fails the evaluation; this is the input's `Serialize`, say) comes back
    /// as an [`Error::Sigil`], so the call needs no task of its own to survive it.
    pub async fn evaluate_async<I>(&self, name: impl Into<String>, input: I, options: EvalOptions) -> Result<EvalResult, Error>
    where
        I: Serialize + Send + 'static,
    {
        let (pool, name) = (self.clone(), name.into());
        joined(tokio::task::spawn_blocking(move || pool.evaluate(&name, &input, &options)).await)
    }

    /// [`Pool::compile`] on tokio's blocking pool: compiling takes milliseconds
    /// to seconds in each instance.
    pub async fn compile_async(&self, name: impl Into<String>, files: Vec<SourceFile>, options: CompileOptions) -> Result<(), Error> {
        let (pool, name) = (self.clone(), name.into());
        joined(tokio::task::spawn_blocking(move || pool.compile(&name, &files, options)).await)
    }
}

/// The result of a blocking task: its own, an error for a panic in it (the
/// async calls never unwind into the caller, which has its own task to protect),
/// or an error for a task the runtime cancelled by shutting down.
#[cfg(feature = "tokio")]
pub(crate) fn joined<T>(joined: Result<Result<T, Error>, tokio::task::JoinError>) -> Result<T, Error> {
    match joined {
        Ok(result) => result,
        Err(err) if err.is_panic() => {
            let panic = err.into_panic();
            let what = panic.downcast_ref::<&str>().map(|s| (*s).to_string()).or_else(|| panic.downcast_ref::<String>().cloned());
            Err(Error::sigil(
                format!("the evaluation panicked: {}", what.unwrap_or_else(|| "without a message".into())),
                "a host function's panic is caught and fails the evaluation instead; this one came from somewhere else, such as a Serialize impl of the input",
            ))
        }
        Err(_) => Err(Error::sigil("the blocking task was cancelled", "the tokio runtime is shutting down")),
    }
}

impl std::fmt::Debug for Pool {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Pool").field("stats", &self.stats()).finish()
    }
}

impl Drop for Closer {
    fn drop(&mut self) {
        self.0.closed.store(true, Ordering::Relaxed);
        self.0.available.notify_all();
    }
}

impl Inner {
    /// Brings every instance but `skip` up to date, one at a time. An instance
    /// being rebuilt is skipped: it compiles the current recipes before it
    /// returns.
    fn roll_out(self: &Arc<Self>, skip: Option<usize>) {
        for id in 0..self.options.size {
            if Some(id) == skip {
                continue;
            }
            if let Some(mut lease) = self.acquire_id(id) {
                // A failure here is retried when the instance is next used.
                let _ = lease.sync();
            }
        }
    }

    /// Waits for any free instance.
    fn acquire(self: &Arc<Self>, timeout: Option<Duration>) -> Result<Lease, Error> {
        let start = Instant::now();
        let mut state = lock(&self.state);
        loop {
            if let Some(slot) = state.idle.pop() {
                drop(state);
                return self.lease(slot);
            }
            state = self.wait(state, timeout, start)?;
        }
    }

    /// Waits for the instance numbered `id`; `None` when it's being rebuilt.
    fn acquire_id(self: &Arc<Self>, id: usize) -> Option<Lease> {
        let start = Instant::now();
        let mut state = lock(&self.state);
        loop {
            if let Some(at) = state.idle.iter().position(|s| s.id == id) {
                let slot = state.idle.swap_remove(at);
                drop(state);
                return self.lease(slot).ok();
            }
            if state.rebuilding.contains(&id) {
                return None;
            }
            state = self.wait(state, None, start).ok()?;
        }
    }

    /// A lease on `slot`, rebuilt first if it was parked dead (see
    /// `rebuild_in_background`); a slot that can't be rebuilt goes back to idle.
    fn lease(self: &Arc<Self>, slot: Slot) -> Result<Lease, Error> {
        if !slot.dead {
            return Ok(Lease { pool: Arc::clone(self), slot: Some(slot) });
        }
        let id = slot.id;
        match self.revive(id) {
            Ok(fresh) => {
                drop(slot);
                Ok(Lease { pool: Arc::clone(self), slot: Some(fresh) })
            }
            Err(err) => {
                lock(&self.state).idle.push(slot);
                self.available.notify_all();
                Err(err)
            }
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

    /// The current recipes, with the generation they belong to.
    fn recipes(&self) -> (u64, Recipes) {
        let state = lock(&self.state);
        (state.generation, state.recipes.iter().map(|(name, (id, recipe))| (name.clone(), *id, Arc::clone(recipe))).collect())
    }

    /// Takes a stopped instance off the caller's path: its slot is rebuilt on a
    /// thread of its own, and the pool serves without it meanwhile. Never
    /// rebuilds inline, because it runs from `Lease::drop`, which may be
    /// unwinding from a panic: if no thread can be started the slot goes back
    /// to idle marked dead, and is rebuilt by the next call that takes it.
    fn rebuild_in_background(self: &Arc<Self>, stopped: Slot) {
        let id = stopped.id;
        if self.closed.load(Ordering::Relaxed) {
            return;
        }
        lock(&self.state).rebuilding.insert(id);
        // Waiters for this slot (a roll-out holding the install lock) skip it now.
        self.available.notify_all();
        let parked = Arc::new(Mutex::new(Some(stopped)));
        let (pool, handed) = (Arc::clone(self), Arc::clone(&parked));
        let spawned = thread::Builder::new().name("sigil-pool-rebuild".into()).spawn(move || {
            drop(lock(&handed).take());
            pool.rebuild(id);
        });
        if spawned.is_err()
            && let Some(slot) = lock(&parked).take()
        {
            let mut state = lock(&self.state);
            state.rebuilding.remove(&id);
            state.idle.push(slot);
            drop(state);
            self.available.notify_all();
        }
    }

    /// Replaces a stopped instance by a fresh one with the current recipes
    /// compiled, and puts it back in service.
    ///
    /// Retries with a growing pause when instantiating fails (out of memory,
    /// most likely) and when compiling a recipe stops the fresh instance (it
    /// exhausts `max_memory`, runs into a deadline, traps): each such instance
    /// counts as replaced. A recipe that stops [`MAX_RECIPE_STOPS`] fresh
    /// instances in a row is left off the slot and recorded, see
    /// [`Pool::rebuild_failures`], so one bad policy can't keep a slot out of
    /// service for good. Gives up when the pool is gone.
    fn rebuild(self: &Arc<Self>, id: usize) {
        let mut backoff = REBUILD_BACKOFF;
        let mut stops: HashMap<u64, u32> = HashMap::new();
        let mut skipped: HashSet<u64> = HashSet::new();
        'fresh: loop {
            if !self.pause(Duration::ZERO) {
                lock(&self.state).rebuilding.remove(&id);
                return;
            }
            let sigil = match Sigil::with_limits(&self.module, self.options.limits) {
                Ok(sigil) => sigil,
                Err(_) => {
                    self.pause(backoff);
                    backoff = (backoff * 2).min(REBUILD_BACKOFF_MAX);
                    continue 'fresh;
                }
            };
            let mut slot = Slot { id, sigil, policies: HashMap::new(), dead: false, synced: 0 };
            loop {
                let (generation, recipes) = self.recipes();
                let usable: Recipes = recipes.iter().filter(|(_, rid, _)| !skipped.contains(rid)).cloned().collect();
                let failures = slot.sync_to(&usable);
                if slot.dead || slot.sigil.stopped().is_some() {
                    self.replaced.fetch_add(1, Ordering::Relaxed);
                    for (name, err) in failures.iter().filter(|(_, err)| err.is_stopped()) {
                        let Some(rid) = recipes.iter().find(|(n, _, _)| n == name).map(|(_, rid, _)| *rid) else { continue };
                        let count = stops.entry(rid).or_insert(0);
                        *count += 1;
                        if *count >= MAX_RECIPE_STOPS {
                            skipped.insert(rid);
                            lock(&self.state).skipped.insert(name.clone(), err.to_string());
                        }
                    }
                    self.pause(backoff);
                    backoff = (backoff * 2).min(REBUILD_BACKOFF_MAX);
                    continue 'fresh;
                }
                if self.closed.load(Ordering::Relaxed) {
                    lock(&self.state).rebuilding.remove(&id);
                    return;
                }
                let mut state = lock(&self.state);
                // An install or a remove since the snapshot: compile again.
                slot.synced = generation;
                if state.generation == generation {
                    state.rebuilding.remove(&id);
                    state.idle.push(slot);
                    drop(state);
                    self.available.notify_all();
                    return;
                }
            }
        }
    }

    /// Sleeps for `d`, in slices, so a closing pool isn't waited for; whether
    /// the pool is still open.
    fn pause(&self, d: Duration) -> bool {
        let until = Instant::now() + d;
        loop {
            if self.closed.load(Ordering::Relaxed) {
                return false;
            }
            let left = until.saturating_duration_since(Instant::now());
            if left.is_zero() {
                return true;
            }
            thread::sleep(left.min(Duration::from_millis(20)));
        }
    }

    /// Rebuilds a slot that was parked dead (no thread could be started) in
    /// the caller's thread, which is not a drop.
    fn revive(&self, id: usize) -> Result<Slot, Error> {
        let sigil = Sigil::with_limits(&self.module, self.options.limits)?;
        let mut slot = Slot { id, sigil, policies: HashMap::new(), dead: false, synced: 0 };
        let (_, recipes) = self.recipes();
        slot.sync_to(&recipes);
        if slot.dead {
            return Err(Error::sigil(
                "the pool couldn't rebuild a stopped instance",
                "a policy keeps stopping fresh instances; see Pool::rebuild_failures",
            ));
        }
        Ok(slot)
    }
}

impl Slot {
    /// Makes the instance's policies match `recipes`; the failures of the ones
    /// that didn't compile.
    fn sync_to(&mut self, recipes: &Recipes) -> Vec<(String, Error)> {
        self.policies.retain(|name, _| recipes.iter().any(|(n, _, _)| n == name));
        let mut failures = Vec::new();
        for (name, id, recipe) in recipes {
            if self.policies.get(name).is_some_and(|(have, _)| have == id) {
                continue;
            }
            match recipe(&self.sigil) {
                Ok(policy) => {
                    self.policies.insert(name.clone(), (*id, policy));
                }
                Err(err) => {
                    self.policies.remove(name);
                    if err.is_stopped() {
                        self.dead = true;
                    }
                    failures.push((name.clone(), err));
                }
            }
        }
        failures
    }
}

impl Lease {
    fn slot(&self) -> &Slot {
        self.slot.as_ref().expect("a lease holds its slot until dropped")
    }

    fn slot_mut(&mut self) -> &mut Slot {
        self.slot.as_mut().expect("a lease holds its slot until dropped")
    }

    /// Marks the instance dead when `err` stopped it: it's rebuilt when the
    /// lease ends.
    fn note(&mut self, err: &Error) {
        if err.is_stopped() && !self.slot().dead {
            self.slot_mut().dead = true;
            self.pool.replaced.fetch_add(1, Ordering::Relaxed);
        }
    }

    /// Makes the instance's policies match the pool's recipes; the failures of
    /// recipes that didn't compile.
    fn sync(&mut self) -> Vec<(String, Error)> {
        // The common case, with no lock and no allocation: nothing has been
        // installed or removed since this instance last compiled everything.
        if !self.slot().dead && self.slot().synced == self.pool.generation.load(Ordering::Acquire) {
            return Vec::new();
        }
        let (generation, recipes) = self.pool.recipes();
        let before = self.slot().dead;
        let failures = self.slot_mut().sync_to(&recipes);
        if failures.is_empty() {
            self.slot_mut().synced = generation;
        }
        if !before && self.slot().dead {
            self.pool.replaced.fetch_add(1, Ordering::Relaxed);
        }
        failures
    }
}

impl Drop for Lease {
    fn drop(&mut self) {
        let Some(mut slot) = self.slot.take() else { return };
        // A panic mid-call (a host function's, say, outside the catch) may have
        // left the instance anywhere, and an instance that stopped without a
        // call noticing (a policy dropped after it died) is dead just the same.
        if !slot.dead && (thread::panicking() || slot.sigil.stopped().is_some()) {
            slot.dead = true;
            self.pool.replaced.fetch_add(1, Ordering::Relaxed);
        }
        if slot.dead {
            self.pool.rebuild_in_background(slot);
            return;
        }
        lock(&self.pool.state).idle.push(slot);
        self.pool.available.notify_all();
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}
