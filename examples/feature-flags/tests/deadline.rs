//! A call the engine has to kill: with one unit of fuel every evaluation runs
//! out at once, which stops the instance the way a runaway loop killed by the
//! deadline would. The service must answer the flag off with reason ERROR,
//! count the failure by kind, rebuild the instance in the background, and go
//! on serving.

mod common;

use std::time::{Duration, Instant};

use common::{Harness, harness, metric};
use serde_json::json;
use sigil::EvalOptions;

const CONTEXT: &str = r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#;

/// Waits for the pool's background rebuilds to finish.
async fn settle(h: &Harness) {
    let pool = h.state.engine.store().current();
    let deadline = Instant::now() + Duration::from_secs(30);
    while pool.pool.stats().rebuilding > 0 {
        assert!(Instant::now() < deadline, "the rebuild never finished");
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
}

#[tokio::test]
async fn a_killed_evaluation_fails_closed_and_the_instance_is_rebuilt() {
    let h = harness(&[("FEATUREGATE_EVALUATION_FUEL", "1"), ("FEATUREGATE_WORKERS", "1")]).await;

    for round in 1..=3 {
        let reply = h.post("/ofrep/v1/evaluate/flags/dark-mode", CONTEXT).await;
        assert_eq!(reply.status, 200, "{}", reply.text);
        let body = reply.json();
        assert_eq!((&body["reason"], &body["value"], &body["variant"]), (&json!("ERROR"), &json!(false), &json!("off")));
        assert_eq!(body["metadata"]["sigil.error"], "stopped");
        assert_eq!(metric(&h.metrics().await, "featuregate_evaluation_errors_total{kind=\"stopped\"}"), Some(round as f64));
        // The pool has one instance, so the next request waits for the rebuild.
        settle(&h).await;
    }

    // Every kill was followed by a rebuild, and the rebuilt instance works: the
    // same policy, without the fuel limit, decides.
    let bundle = h.state.engine.store().current();
    assert_eq!(bundle.pool.stats().replaced, 3);
    let input = json!({"flag": "dark-mode", "user": {"id": "u", "plan": "free", "region": "eu-1", "beta": false, "attributes": {}}, "bucket": 1, "killed": false});
    let result = bundle.pool.evaluate_async("flags.dark_mode", input, EvalOptions::default()).await.unwrap();
    assert_eq!((result.decision.as_deref(), result.reason.as_deref()), (Some("enable"), Some("rollout")));

    let m = h.metrics().await;
    assert_eq!(metric(&m, "featuregate_pool_replacements_total"), Some(3.0), "{m}");
    assert_eq!(metric(&m, "featuregate_pool_rebuilding"), Some(0.0), "{m}");
    assert_eq!(h.get("/readyz").await.status, 200);
    let bulk = h.post("/ofrep/v1/evaluate/flags", CONTEXT).await.json();
    assert!(bulk["flags"].as_array().unwrap().iter().all(|f| f["reason"] == "ERROR"), "{bulk}");
}

/// An input whose serialization panics, standing in for any panic on the
/// evaluation thread.
struct Panics;

impl serde::Serialize for Panics {
    fn serialize<S: serde::Serializer>(&self, _: S) -> Result<S::Ok, S::Error> {
        panic!("boom")
    }
}

#[tokio::test]
async fn a_panic_on_the_evaluation_thread_is_an_internal_failure() {
    let h = harness(&[]).await;
    let pool = h.state.engine.store().current().pool.clone();
    let outcome = pool.evaluate_async("flags.dark_mode", Panics, EvalOptions::default()).await;
    let failure = featuregate::engine::read(outcome).unwrap_err();
    assert_eq!(failure.kind, "internal");
    assert!(failure.detail.contains("boom"), "{}", failure.detail);
    // The pool is unharmed.
    assert_eq!(h.evaluate("dark-mode", json!({"targetingKey": "u", "region": "eu-1"})).await["value"], true);
}
