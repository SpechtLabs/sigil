//! The pool: parallel evaluation, rolling installs, replacement of stopped
//! instances, saturation.

mod common;

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::thread;
use std::time::{Duration, Instant};

use common::{deploy_gates, err_of, json, sigil_files, split};
use serde_json::{Value, json};
use sigil::{CompileOptions, Decision, Error, EvalOptions, Kind, Limits, Pool, PoolOptions, SourceFile, Type, host_fn};

fn sre() -> Value {
    json(&deploy_gates().join("teams/payments/testdata/sre.json"))
}

fn payments_options() -> CompileOptions {
    CompileOptions { policy: Some("payments.production".into()), functions: [("split".to_string(), split())].into(), ..Default::default() }
}

fn deploy_pool(size: usize) -> Pool {
    let pool = Pool::new(common::module(), size).unwrap();
    pool.compile("payments", &sigil_files(&deploy_gates()), payments_options()).unwrap();
    pool
}

/// A kind with one decision whose reason the policy picks.
fn versioned_kind() -> Kind {
    let d = Decision::new("d", ["a", "b"]);
    Kind::builder("Versioned").version(1).input("x", Type::string()).decisions([&d]).default_outcome(d.reason("a")).build().unwrap()
}

fn versioned_policy(reason: &str) -> Vec<SourceFile> {
    vec![SourceFile::new("v.sigil", format!("policy v.p: Versioned@1\n\nwhen true {{\n  d(reason: {reason})\n}}\n"))]
}

fn install_versioned(pool: &Pool, name: &str, reason: &str) -> Result<(), Error> {
    let (kind, files) = (versioned_kind(), versioned_policy(reason));
    pool.install(name, move |s| kind.compile(s, &files, Default::default()))
}

const SLOW_POLICY: &str =
    "policy slow.cube: Slow@1\n\nwhen all a in items: all b in items: all c in items: a != \"x\" {\n  ok(reason: yes)\n}\n";

fn install_slow(pool: &Pool) {
    let ok = Decision::new("ok", ["yes"]);
    let kind = Kind::builder("Slow")
        .version(1)
        .input("items", Type::list(Type::string()))
        .decisions([&ok])
        .default_outcome(ok.reason("yes"))
        .build()
        .unwrap();
    let files = vec![SourceFile::new("cube.sigil", SLOW_POLICY)];
    pool.install("slow", move |s| kind.compile(s, &files, Default::default())).unwrap();
}

/// Waits until every instance is idle again: no rebuild in flight.
fn settle(pool: &Pool) {
    let until = Instant::now() + Duration::from_secs(20);
    while Instant::now() < until {
        let stats = pool.stats();
        if stats.rebuilding == 0 && stats.idle == stats.size {
            return;
        }
        thread::sleep(Duration::from_millis(10));
    }
    panic!("the pool didn't settle: {:?}", pool.stats());
}

fn items(n: usize) -> Value {
    json!({ "items": (0..n).map(|i| format!("item-{i}")).collect::<Vec<_>>() })
}

#[test]
fn evaluates_in_parallel_with_the_answers_of_a_single_instance() {
    let pool = Arc::new(deploy_pool(3));
    let expected = pool.evaluate("payments", &sre(), &EvalOptions::default()).unwrap();
    assert!(expected.error.is_none());
    let done = Arc::new(AtomicUsize::new(0));
    let threads: Vec<_> = (0..8)
        .map(|_| {
            let (pool, expected, done) = (Arc::clone(&pool), expected.clone(), Arc::clone(&done));
            thread::spawn(move || {
                for _ in 0..25 {
                    assert_eq!(pool.evaluate("payments", &sre(), &EvalOptions::default()).unwrap(), expected);
                    done.fetch_add(1, Ordering::Relaxed);
                }
            })
        })
        .collect();
    for t in threads {
        t.join().unwrap();
    }
    assert_eq!(done.load(Ordering::Relaxed), 200);
    let stats = pool.stats();
    assert_eq!((stats.size, stats.idle, stats.installed, stats.replaced), (3, 3, 1, 0));
}

#[test]
fn a_policy_that_fails_to_compile_leaves_the_pool_as_it_was() {
    let pool = deploy_pool(2);
    let err = err_of(pool.compile("payments", &[SourceFile::new("bad.sigil", "policy x {")], CompileOptions::default()));
    assert!(matches!(err, Error::Sigil(_)), "{err:?}");
    assert_eq!(pool.names(), ["payments"]);
    // The old policy still serves on every instance.
    for _ in 0..4 {
        assert!(pool.evaluate("payments", &sre(), &EvalOptions::default()).unwrap().error.is_none());
    }
    // And nothing half-installed appeared under a new name.
    assert!(pool.compile("fresh", &[SourceFile::new("bad.sigil", "policy x {")], CompileOptions::default()).is_err());
    assert_eq!(pool.names(), ["payments"]);
}

#[test]
fn a_reinstall_reaches_every_instance_before_it_returns() {
    let pool = Pool::new(common::module(), 3).unwrap();
    install_versioned(&pool, "v", "a").unwrap();
    let reason = |pool: &Pool| pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap();
    for _ in 0..6 {
        assert_eq!(reason(&pool), "a");
    }
    install_versioned(&pool, "v", "b").unwrap();
    for _ in 0..12 {
        assert_eq!(reason(&pool), "b");
    }
    assert_eq!(pool.names(), ["v"]);
}

#[test]
fn remove_drops_a_policy_from_every_instance() {
    let pool = Pool::new(common::module(), 2).unwrap();
    install_versioned(&pool, "one", "a").unwrap();
    install_versioned(&pool, "two", "b").unwrap();
    assert_eq!(pool.names(), ["one", "two"]);
    assert!(pool.remove("one"));
    assert!(!pool.remove("one"));
    assert_eq!(pool.names(), ["two"]);
    for _ in 0..4 {
        assert!(matches!(pool.evaluate("one", &json!({"x": "."}), &EvalOptions::default()), Err(Error::NoPolicy(n)) if n == "one"));
        assert!(pool.evaluate("two", &json!({"x": "."}), &EvalOptions::default()).is_ok());
    }
}

#[test]
fn an_unknown_policy_is_reported_by_name() {
    let pool = Pool::new(common::module(), 1).unwrap();
    let err = err_of(pool.evaluate("nope", &json!({}), &EvalOptions::default()));
    assert!(matches!(&err, Error::NoPolicy(n) if n == "nope"), "{err:?}");
    assert!(!err.is_stopped());
    assert!(err.to_string().contains("Pool::install"));
}

#[test]
fn a_stopped_instance_is_replaced_with_every_policy_compiled_again() {
    let options = PoolOptions {
        size: 2,
        limits: Limits { op_deadline: Some(Duration::from_millis(100)), ..Limits::default() },
        acquire_timeout: None,
    };
    let pool = Pool::with_options(common::module(), options).unwrap();
    install_slow(&pool);
    install_versioned(&pool, "v", "a").unwrap();
    pool.compile("payments", &sigil_files(&deploy_gates()), payments_options()).unwrap();

    // The runaway call is killed from outside: its caller gets the error.
    let err = err_of(pool.evaluate("slow", &items(400), &EvalOptions::default()));
    assert!(matches!(err, Error::Timeout(_)), "{err:?}");
    assert!(err.is_stopped());
    assert_eq!(pool.stats().replaced, 1);
    settle(&pool);
    assert_eq!((pool.stats().idle, pool.stats().rebuilding), (2, 0));

    // Everything still serves, on the replacement too, which took every policy.
    for _ in 0..8 {
        assert_eq!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "a");
        assert!(pool.evaluate("payments", &sre(), &EvalOptions::default()).unwrap().error.is_none());
        assert!(pool.evaluate("slow", &items(2), &EvalOptions::default()).unwrap().error.is_none());
    }
    // Killing both instances in turn is survivable too.
    for _ in 0..2 {
        assert!(pool.evaluate("slow", &items(400), &EvalOptions::default()).is_err());
        settle(&pool);
    }
    assert_eq!(pool.stats().replaced, 3);
    assert!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).is_ok());
}

/// A gate a test opens by hand: whoever waits on it blocks until `open`, however
/// slowly the rest of the test runs, so no assertion depends on timing.
#[derive(Default)]
struct Latch {
    open: std::sync::Mutex<bool>,
    changed: std::sync::Condvar,
}

impl Latch {
    fn open(&self) {
        *self.open.lock().unwrap() = true;
        self.changed.notify_all();
    }

    fn wait(&self) {
        let mut open = self.open.lock().unwrap();
        while !*open {
            open = self.changed.wait(open).unwrap();
        }
    }
}

#[test]
fn a_saturated_pool_fails_fast_with_busy_when_asked_to() {
    let options = PoolOptions { size: 1, limits: Limits::default(), acquire_timeout: Some(Duration::from_millis(50)) };
    let pool = Arc::new(Pool::with_options(common::module(), options).unwrap());
    let real = split();
    let (started_tx, started_rx) = std::sync::mpsc::channel::<()>();
    let started_tx = std::sync::Mutex::new(started_tx);
    let hold = Arc::new(Latch::default());
    let held = Arc::clone(&hold);
    let opts = CompileOptions {
        functions: [(
            "split".to_string(),
            host_fn(move |args| {
                let _ = started_tx.lock().unwrap().send(());
                held.wait();
                real(args)
            }),
        )]
        .into(),
        ..payments_options()
    };
    pool.compile("payments", &sigil_files(&deploy_gates()), opts).unwrap();
    let holder = {
        let pool = Arc::clone(&pool);
        thread::spawn(move || pool.evaluate("payments", &sre(), &EvalOptions::default()))
    };
    // The holder is inside the evaluation, holding the only instance.
    started_rx.recv_timeout(Duration::from_secs(120)).expect("the holder never started");
    let err = err_of(pool.evaluate("payments", &sre(), &EvalOptions::default()));
    assert!(matches!(err, Error::Busy(d) if d == Duration::from_millis(50)), "{err:?}");
    // The holder's call isn't disturbed, and the pool serves again once it's done.
    hold.open();
    assert!(holder.join().unwrap().unwrap().error.is_none());
    assert!(pool.evaluate("payments", &sre(), &EvalOptions::default()).is_ok());
}

#[test]
fn a_pool_needs_an_instance() {
    let err = err_of(Pool::new(common::module(), 0));
    assert!(err.to_string().contains("PoolOptions::size"), "{err}");
}

#[test]
fn explain_and_with_policy_use_a_free_instance() {
    let pool = deploy_pool(2);
    let explanation = pool.explain("payments").unwrap();
    assert_eq!(explanation.policy, "payments.production");
    let name = pool.with_policy("payments", |p| Ok(p.name().to_string())).unwrap();
    assert_eq!(name, "payments.production");
    assert!(matches!(pool.explain("nope"), Err(Error::NoPolicy(_))));
}

#[test]
fn a_panic_in_with_policy_replaces_the_instance_it_held() {
    let pool = Pool::new(common::module(), 1).unwrap();
    install_versioned(&pool, "v", "a").unwrap();
    let pool = Arc::new(pool);
    let p = Arc::clone(&pool);
    let crashed = thread::spawn(move || p.with_policy("v", |_| -> Result<(), Error> { panic!("the caller's closure panics") })).join();
    assert!(crashed.is_err());
    // The pool got its instance back, dead, and rebuilds it in the background.
    assert_eq!(pool.stats().replaced, 1);
    settle(&pool);
    assert_eq!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "a");
    assert_eq!(pool.stats().replaced, 1);
}

#[test]
fn the_handles_cross_threads() {
    fn send_sync<T: Send + Sync>() {}
    send_sync::<Pool>();
    send_sync::<sigil::Sigil>();
    send_sync::<sigil::Policy>();
    send_sync::<sigil::Module>();
    send_sync::<sigil::Kind>();
    send_sync::<sigil::Error>();
}

/// A pool whose recipe blocks on the returned latch once it runs for the third
/// time or later: the two instances of the pool and the first install pass, a
/// rebuild waits until the test opens the latch.
fn pool_with_slow_rebuilds() -> (Pool, Arc<AtomicUsize>, Arc<Latch>) {
    let options = PoolOptions {
        size: 2,
        limits: Limits { op_deadline: Some(Duration::from_millis(100)), ..Limits::default() },
        acquire_timeout: None,
    };
    let pool = Pool::with_options(common::module(), options).unwrap();
    let calls = Arc::new(AtomicUsize::new(0));
    let ok = Decision::new("ok", ["yes"]);
    let kind = Kind::builder("Slow")
        .version(1)
        .input("items", Type::list(Type::string()))
        .decisions([&ok])
        .default_outcome(ok.reason("yes"))
        .build()
        .unwrap();
    let files = vec![SourceFile::new("cube.sigil", SLOW_POLICY)];
    let counted = Arc::clone(&calls);
    let gate = Arc::new(Latch::default());
    let gated = Arc::clone(&gate);
    pool.install("slow", move |s| {
        if counted.fetch_add(1, Ordering::SeqCst) >= 2 {
            gated.wait();
        }
        kind.compile(s, &files, Default::default())
    })
    .unwrap();
    (pool, calls, gate)
}

#[test]
fn a_timed_out_caller_returns_without_waiting_for_the_rebuild() {
    let (pool, calls, rebuild_gate) = pool_with_slow_rebuilds();
    let err = err_of(pool.evaluate("slow", &items(400), &EvalOptions::default()));
    assert!(matches!(err, Error::Timeout(_)), "{err:?}");

    // The caller is back, and the rebuild can't have finished: it is blocked on the
    // gate, which only this test opens. No clock involved.
    let stats = pool.stats();
    assert_eq!((stats.replaced, stats.rebuilding, stats.idle), (1, 1, 1), "{stats:?}");
    // The pool serves with one instance fewer in the meantime.
    assert!(pool.evaluate("slow", &items(2), &EvalOptions::default()).unwrap().error.is_none());
    assert_eq!(pool.stats().rebuilding, 1);

    rebuild_gate.open();
    settle(&pool);
    assert_eq!(calls.load(Ordering::SeqCst), 3, "the rebuild ran the recipe once");
    assert_eq!(pool.stats().replaced, 1);
}

#[test]
fn an_install_during_a_rebuild_reaches_the_rebuilt_instance() {
    let (pool, _, rebuild_gate) = pool_with_slow_rebuilds();
    install_versioned(&pool, "v", "a").unwrap();
    assert!(pool.evaluate("slow", &items(400), &EvalOptions::default()).is_err());
    assert_eq!(pool.stats().rebuilding, 1);

    // The rebuild is stuck in its recipe; the new version must still arrive.
    install_versioned(&pool, "v", "b").unwrap();
    rebuild_gate.open();
    settle(&pool);
    for _ in 0..12 {
        assert_eq!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "b");
    }
}

#[test]
fn a_remove_during_a_rebuild_reaches_the_rebuilt_instance() {
    let (pool, _, rebuild_gate) = pool_with_slow_rebuilds();
    install_versioned(&pool, "v", "a").unwrap();
    assert!(pool.evaluate("slow", &items(400), &EvalOptions::default()).is_err());
    assert_eq!(pool.stats().rebuilding, 1);

    assert!(pool.remove("v"));
    rebuild_gate.open();
    settle(&pool);
    for _ in 0..8 {
        assert!(matches!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()), Err(Error::NoPolicy(_))));
        assert!(pool.evaluate("slow", &items(2), &EvalOptions::default()).is_ok());
    }
}

#[test]
fn dropping_the_pool_during_a_rebuild_ends_the_rebuild_and_frees_the_recipes() {
    let (pool, _, rebuild_gate) = pool_with_slow_rebuilds();
    let marker = Arc::new(());
    let held = Arc::clone(&marker);
    install_versioned(&pool, "held", "a").unwrap();
    pool.install("marked", move |s| {
        let _ = &held;
        let (kind, files) = (versioned_kind(), versioned_policy("a"));
        kind.compile(s, &files, Default::default())
    })
    .unwrap();
    assert!(pool.evaluate("slow", &items(400), &EvalOptions::default()).is_err());
    assert_eq!(pool.stats().rebuilding, 1);
    assert_eq!(Arc::strong_count(&marker), 2);

    drop(pool);
    // The rebuild thread is still blocked in its recipe, holding the pool. Let it
    // go: it notices the pool is gone, drops what it built, and lets go of the recipes.
    rebuild_gate.open();
    let until = Instant::now() + Duration::from_secs(120);
    while Arc::strong_count(&marker) > 1 && Instant::now() < until {
        thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(Arc::strong_count(&marker), 1, "a rebuild thread still holds the pool");
}

#[test]
fn a_cloned_pool_is_the_same_pool() {
    let pool = Pool::new(common::module(), 2).unwrap();
    let clone = pool.clone();
    install_versioned(&clone, "v", "a").unwrap();
    assert_eq!(pool.names(), ["v"]);
    drop(pool);
    // The clone keeps it open.
    assert_eq!(clone.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "a");
}

#[test]
fn a_recipe_that_stops_fresh_instances_is_left_off_after_a_few_tries_and_recorded() {
    let pool = Pool::new(common::module(), 1).unwrap();
    let calls = Arc::new(AtomicUsize::new(0));
    let counted = Arc::clone(&calls);
    let (kind, files) = (versioned_kind(), versioned_policy("a"));
    pool.install("bad", move |s| {
        // The install compiles it; every rebuild after finds it stopping the fresh instance.
        if counted.fetch_add(1, Ordering::SeqCst) >= 1 { Err(Error::OutOfFuel) } else { kind.compile(s, &files, Default::default()) }
    })
    .unwrap();
    install_versioned(&pool, "good", "b").unwrap();

    // Stop the instance from outside, as a killed call would.
    assert!(matches!(pool.with_policy("good", |_| -> Result<(), Error> { Err(Error::OutOfFuel) }), Err(Error::OutOfFuel)));

    // The rebuild backs off between attempts instead of spinning, gives up on
    // the recipe after three, and returns the slot to service without it.
    let started = Instant::now();
    settle(&pool);
    assert!(started.elapsed() >= Duration::from_millis(100), "no backoff: {:?}", started.elapsed());
    let stats = pool.stats();
    assert_eq!(stats.rebuilding, 0);
    assert_eq!(stats.replaced, 1 + 3, "every stopped fresh instance counts: {stats:?}");
    let failures = pool.rebuild_failures();
    assert_eq!(failures.len(), 1, "{failures:?}");
    assert_eq!(failures[0].0, "bad");
    assert!(failures[0].1.contains("out of fuel"), "{}", failures[0].1);
    // The good policy serves, and an install of the bad one's name clears the record.
    assert_eq!(pool.evaluate("good", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "b");
    install_versioned(&pool, "bad", "a").unwrap();
    assert!(pool.rebuild_failures().is_empty());
}

#[test]
fn a_roll_out_waiting_for_a_slot_stops_waiting_when_the_slot_goes_to_rebuild() {
    let (pool, _, rebuild_gate) = pool_with_slow_rebuilds();
    let pool = Arc::new(pool);
    let (leased_tx, leased_rx) = std::sync::mpsc::channel::<()>();
    let release = Arc::new(Latch::default());
    let released = Arc::clone(&release);
    let p = Arc::clone(&pool);
    let killer = thread::spawn(move || {
        p.with_policy("slow", |_| -> Result<(), Error> {
            leased_tx.send(()).unwrap();
            released.wait();
            Err(Error::OutOfFuel)
        })
    });
    leased_rx.recv_timeout(Duration::from_secs(120)).expect("the killer never leased a slot");
    // The install's roll-out waits for the leased slot. A pause makes it likely
    // to be waiting before the slot is released; if it isn't yet, the test still
    // passes (it can't tell), it just guards less.
    let (done_tx, done_rx) = std::sync::mpsc::channel();
    let installer = {
        let pool = Arc::clone(&pool);
        thread::spawn(move || done_tx.send(install_versioned(&pool, "v", "a")))
    };
    thread::sleep(Duration::from_millis(300));
    release.open();
    // The slot goes to rebuild, which is blocked on its gate: the install must
    // finish anyway, without waiting for the rebuild.
    done_rx.recv_timeout(Duration::from_secs(120)).expect("the install waited for the rebuild").unwrap();
    installer.join().unwrap().unwrap();
    assert!(killer.join().unwrap().is_err());
    rebuild_gate.open();
    settle(&pool);
    assert_eq!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "a");
}
