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
    let stats = pool.stats();
    assert_eq!((stats.replaced, stats.idle), (1, 2));

    // Everything still serves, on the replacement too, which took every policy.
    for _ in 0..8 {
        assert_eq!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).unwrap().reason.unwrap(), "a");
        assert!(pool.evaluate("payments", &sre(), &EvalOptions::default()).unwrap().error.is_none());
        assert!(pool.evaluate("slow", &items(2), &EvalOptions::default()).unwrap().error.is_none());
    }
    // Killing both instances in turn is survivable too.
    for _ in 0..2 {
        assert!(pool.evaluate("slow", &items(400), &EvalOptions::default()).is_err());
    }
    assert_eq!(pool.stats().replaced, 3);
    assert!(pool.evaluate("v", &json!({"x": "."}), &EvalOptions::default()).is_ok());
}

#[test]
fn a_saturated_pool_fails_fast_with_busy_when_asked_to() {
    let options = PoolOptions { size: 1, limits: Limits::default(), acquire_timeout: Some(Duration::from_millis(50)) };
    let pool = Arc::new(Pool::with_options(common::module(), options).unwrap());
    let real = split();
    let opts = CompileOptions {
        functions: [(
            "split".to_string(),
            host_fn(move |args| {
                thread::sleep(Duration::from_millis(400));
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
    thread::sleep(Duration::from_millis(100));
    let start = Instant::now();
    let err = err_of(pool.evaluate("payments", &sre(), &EvalOptions::default()));
    assert!(matches!(err, Error::Busy(d) if d == Duration::from_millis(50)), "{err:?}");
    assert!(start.elapsed() < Duration::from_millis(300), "waited {:?}", start.elapsed());
    // The holder's call isn't disturbed, and the pool serves again once it's done.
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
    // The pool got its instance back, dead, and replaces it at the next use.
    assert_eq!(pool.stats().idle, 1);
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
