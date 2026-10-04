//! Deadlines and cost bounds: the ABI's `timeout_ms`, epoch interruption that
//! kills a call from outside, fuel, and what a stopped instance does.

mod common;

use std::sync::OnceLock;
use std::thread;
use std::time::{Duration, Instant};

use common::{deploy_gates, err_of, json, sigil, sigil_files, split};
use serde_json::{Value, json};
use sigil::{
    CompileOptions, Decision, Error, EvalOptions, FailureKind, Kind, Limits, Module, ModuleConfig, Policy, Sigil, SourceFile, Type, host_fn,
};

/// A kind whose one policy loops cubically over its input: slow by design.
fn slow_kind() -> Kind {
    let ok = Decision::new("ok", ["yes"]);
    Kind::builder("Slow")
        .version(1)
        .input("items", Type::list(Type::string()))
        .decisions([&ok])
        .default_outcome(ok.reason("yes"))
        .build()
        .unwrap()
}

const SLOW_POLICY: &str =
    "policy slow.cube: Slow@1\n\nwhen all a in items: all b in items: all c in items: a != \"x\" {\n  ok(reason: yes)\n}\n";

fn items(n: usize) -> Value {
    json!({ "items": (0..n).map(|i| format!("item-{i}")).collect::<Vec<_>>() })
}

fn slow(sigil: &Sigil) -> Policy {
    slow_kind().compile(sigil, &[SourceFile::new("cube.sigil", SLOW_POLICY)], Default::default()).unwrap()
}

fn sre() -> Value {
    json(&deploy_gates().join("teams/payments/testdata/sre.json"))
}

/// The payments policy whose `split` sleeps first.
fn sleepy(sigil: &Sigil, nap: Duration) -> Policy {
    let real = split();
    let files = sigil_files(&deploy_gates());
    let sleeping = host_fn(move |args| {
        thread::sleep(nap);
        real(args)
    });
    sigil
        .compile(
            &files,
            CompileOptions {
                policy: Some("payments.production".into()),
                functions: [("split".to_string(), sleeping)].into(),
                ..Default::default()
            },
        )
        .unwrap()
}

fn fuel_module() -> &'static Module {
    static MODULE: OnceLock<Module> = OnceLock::new();
    MODULE.get_or_init(|| Module::bundled_with(ModuleConfig { fuel: true }).unwrap())
}

#[test]
fn the_timeout_cancels_a_slow_evaluation_with_a_canceled_failure() {
    let s = sigil();
    let policy = sleepy(&s, Duration::from_millis(100));
    let result = policy
        .eval_with(&sre(), &EvalOptions { timeout: Some(Duration::from_millis(20)), grace: Some(Duration::from_secs(60)), fuel: None })
        .unwrap();
    let error = result.error.expect("the evaluation is canceled");
    assert_eq!(error.kind, FailureKind::Canceled);
    assert_eq!(error.message, "the evaluation was stopped: context deadline exceeded");
    assert!(result.trace.is_empty());
    assert_eq!((result.decision.as_deref(), result.reason.as_deref()), (Some("deny"), Some("no_rule_matched")));
    // Canceled by the module, long before the (generous) hard deadline: the instance lives.
    assert!(s.stopped().is_none());
    assert!(policy.eval(&sre()).is_ok());
}

#[test]
fn the_timeout_cancels_an_engine_loop_before_the_hard_deadline() {
    let s = sigil();
    let policy = slow(&s);
    let start = Instant::now();
    let result = policy
        .eval_with(&items(400), &EvalOptions { timeout: Some(Duration::from_millis(50)), grace: Some(Duration::from_secs(60)), fuel: None })
        .unwrap();
    assert_eq!(result.error.unwrap().kind, FailureKind::Canceled);
    assert!(start.elapsed() < Duration::from_secs(120));
    assert!(s.stopped().is_none());
    assert!(policy.eval(&items(2)).unwrap().error.is_none());
}

#[test]
fn epoch_interruption_kills_a_runaway_engine_loop_and_stops_the_instance() {
    let limits = Limits { op_deadline: Some(Duration::from_millis(100)), ..Limits::default() };
    let s = Sigil::with_limits(common::module(), limits).unwrap();
    let policy = slow(&s);
    let start = Instant::now();
    // No timeout: only the hard deadline can stop it.
    let err = err_of(policy.eval(&items(400)));
    assert!(matches!(err, Error::Timeout(d) if d == Duration::from_millis(100)), "{err:?}");
    assert!(err.is_stopped());
    assert!(start.elapsed() < Duration::from_secs(120), "took {:?}", start.elapsed());

    // The instance is dead for good, and says why every time.
    let stopped = s.stopped().expect("a killed call stops the instance");
    assert!(stopped.message.contains("hard deadline"), "{}", stopped.message);
    assert!(matches!(s.version(), Err(Error::Stopped(e)) if e == stopped));
    assert!(matches!(policy.eval(&items(2)), Err(Error::Stopped(e)) if e == stopped));
    assert!(matches!(s.check(&[], &Default::default()), Err(Error::Stopped(_))));
    // Dropping a policy of a dead instance is quiet.
    drop(policy);
}

#[test]
fn epoch_interruption_kills_a_call_that_outlives_its_grace_after_a_slow_host_function() {
    let s = sigil();
    let policy = sleepy(&s, Duration::from_millis(400));
    let options = EvalOptions { timeout: Some(Duration::from_millis(20)), grace: Some(Duration::from_millis(30)), fuel: None };
    let err = err_of(policy.eval_with(&sre(), &options));
    // The host function can't be interrupted, but the engine is, the moment it returns.
    assert!(matches!(err, Error::Timeout(d) if d == Duration::from_millis(50)), "{err:?}");
    assert!(s.stopped().is_some());
    assert!(matches!(policy.eval(&sre()), Err(Error::Stopped(_))));
}

#[test]
fn a_call_within_its_hard_deadline_is_not_killed() {
    let s = sigil();
    let policy = sleepy(&s, Duration::from_millis(10));
    let options = EvalOptions { timeout: Some(Duration::from_secs(60)), grace: Some(Duration::from_secs(60)), fuel: None };
    assert!(policy.eval_with(&sre(), &options).unwrap().error.is_none());
    assert!(s.stopped().is_none());
}

#[test]
fn a_deadline_that_one_instance_hits_leaves_the_others_running() {
    let module = common::module();
    let short = Limits { op_deadline: Some(Duration::from_millis(100)), ..Limits::default() };
    let victim = Sigil::with_limits(module, short).unwrap();
    let bystander = sigil();
    let slow_victim = slow(&victim);
    let quick = slow(&bystander);
    assert!(slow_victim.eval(&items(400)).is_err());
    // The engine clock is shared: the bystander's own calls carry on.
    assert!(quick.eval(&items(3)).unwrap().error.is_none());
    assert!(bystander.stopped().is_none());
}

#[test]
fn fuel_stops_a_call_that_does_too_much_work_and_the_instance_with_it() {
    let s = Sigil::new(fuel_module()).unwrap();
    let policy = slow(&s);
    let err = err_of(policy.eval_with(&items(400), &EvalOptions { fuel: Some(50_000_000), ..Default::default() }));
    assert!(matches!(err, Error::OutOfFuel), "{err:?}");
    assert!(err.is_stopped());
    assert!(s.stopped().is_some());
}

#[test]
fn enough_fuel_lets_the_call_finish_and_the_instance_carries_on() {
    let s = Sigil::new(fuel_module()).unwrap();
    let policy = slow(&s);
    let options = EvalOptions { fuel: Some(u64::MAX / 2), ..Default::default() };
    assert!(policy.eval_with(&items(4), &options).unwrap().error.is_none());
    // A call without a fuel limit isn't metered.
    assert!(policy.eval(&items(4)).unwrap().error.is_none());
    assert!(s.stopped().is_none());
}

#[test]
fn a_fuel_limit_needs_a_module_built_for_it() {
    let s = sigil();
    let policy = slow(&s);
    let err = err_of(policy.eval_with(&items(2), &EvalOptions { fuel: Some(1000), ..Default::default() }));
    assert!(matches!(err, Error::Sigil(_)), "{err:?}");
    assert!(err.to_string().contains("ModuleConfig"), "{err}");
    assert!(s.stopped().is_none());
}

#[test]
fn a_memory_limit_below_what_the_module_starts_with_fails_the_start_with_advice() {
    let limits = Limits { max_memory: Some(1024 * 1024), ..Limits::default() };
    let err = err_of(Sigil::with_limits(common::module(), limits));
    assert!(err.is_stopped(), "{err}");
    assert!(err.to_string().contains("max_memory"), "{err}");
}

#[test]
fn running_out_of_memory_stops_the_instance_and_the_error_says_what_go_said() {
    let limits = Limits { max_memory: Some(16 * 1024 * 1024), ..Limits::default() };
    let s = Sigil::with_limits(common::module(), limits).unwrap();
    let ok = Decision::new("ok", ["yes"]);
    let kind = Kind::builder("Big")
        .version(1)
        .input("items", Type::list(Type::string()))
        .decisions([&ok])
        .default_outcome(ok.reason("yes"))
        .build()
        .unwrap();
    let policy = SourceFile::new("p.sigil", "policy big.p: Big@1\n\nwhen any a in items: a == \"x\" {\n  ok(reason: yes)\n}\n");
    let compiled = kind.compile(&s, &[policy], Default::default()).unwrap();
    let big: Vec<String> = (0..400_000).map(|i| format!("item-number-{i}")).collect();
    let err = err_of(compiled.eval(&json!({ "items": big })));
    let Error::Stopped(stopped) = &err else { panic!("{err:?}") };
    assert!(stopped.message.contains("fatal error: out of memory"), "{}", stopped.message);
    assert!(err.is_stopped());
    assert!(matches!(s.version(), Err(Error::Stopped(e)) if e == *stopped));
    // The message is bounded, not a whole goroutine dump.
    assert!(stopped.message.len() < 6000, "{}", stopped.message.len());
}

#[test]
fn a_call_without_a_deadline_runs_unwatched_and_finishes() {
    // No deadline: no epoch watch starts, the call runs unbounded and finishes.
    let limits = Limits { op_deadline: None, ..Limits::default() };
    let s = Sigil::with_limits(common::module(), limits).unwrap();
    let policy = slow(&s);
    assert!(policy.eval(&items(3)).unwrap().error.is_none());
}
