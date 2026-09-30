//! A call the engine has to kill: with one unit of fuel every evaluation runs
//! out at once, which stops the instance the way a runaway loop killed by the
//! deadline would. The service must answer the flag off with reason ERROR,
//! count the failure by kind, replace the instance, and keep serving.

mod common;

use common::{harness, metric};
use serde_json::json;
use sigil::EvalOptions;

const CONTEXT: &str = r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#;

#[tokio::test]
async fn a_killed_evaluation_fails_closed_and_the_instance_is_replaced() {
    let h = harness(&[("FEATUREGATE_EVALUATION_FUEL", "1"), ("FEATUREGATE_WORKERS", "1")]).await;

    for round in 1..=3 {
        let reply = h.post("/ofrep/v1/evaluate/flags/dark-mode", CONTEXT).await;
        assert_eq!(reply.status, 200, "{}", reply.text);
        let body = reply.json();
        assert_eq!((&body["reason"], &body["value"], &body["variant"]), (&json!("ERROR"), &json!(false), &json!("off")));
        assert_eq!(body["metadata"]["sigil.error"], "stopped");

        let m = h.metrics().await;
        assert_eq!(metric(&m, "featuregate_evaluation_errors_total{kind=\"stopped\"}"), Some(round as f64), "{m}");
        assert_eq!(metric(&m, "featuregate_pool_replacements_total"), Some(round as f64));
    }

    // With one instance in the pool, the replacement is what served the later
    // rounds. It works: the same policy, without the fuel limit, decides.
    let bundle = h.state.engine.store().current();
    assert_eq!(bundle.pool.stats().replaced, 3);
    let input = json!({"flag": "dark-mode", "user": {"id": "u", "plan": "free", "region": "eu-1", "beta": false, "attributes": {}}, "bucket": 1, "killed": false});
    let result = bundle.pool.evaluate("flags.dark_mode", &input, &EvalOptions::default()).unwrap();
    assert_eq!((result.decision.as_deref(), result.reason.as_deref()), (Some("enable"), Some("rollout")));

    // The service stays ready, and the bulk endpoint answers every flag as ERROR.
    assert_eq!(h.get("/readyz").await.status, 200);
    let bulk = h.post("/ofrep/v1/evaluate/flags", CONTEXT).await.json();
    assert!(bulk["flags"].as_array().unwrap().iter().all(|f| f["reason"] == "ERROR"), "{bulk}");
}
