//! Loading and reloading policies: last known good, the platform guardrail
//! that no flag policy can drop, and what a failing evaluation answers.

mod common;

use axum::http::StatusCode;
use common::{Harness, harness, metric, sample_dir, try_harness, write_atomically};
use rstest::rstest;
use serde_json::{Value, json};

/// A flag policy that invokes the guardrails, with `rules` as its body.
fn flag(name: &str, rules: &str) -> String {
    format!("policy flags.{name}: FeatureRollout@1\n\nuse platform.guardrails\n\nguardrails()\n\n{rules}\n")
}

async fn serving(dir: &std::path::Path) -> Harness {
    harness(&[("FEATUREGATE_POLICIES", dir.to_str().unwrap())]).await
}

async fn reload(h: &Harness) -> (StatusCode, Value) {
    let r = h.post("/api/v1/policies/reload", "").await;
    (r.status, r.json())
}

const EU: fn(&str) -> Value = |user| json!({"targetingKey": user, "plan": "free", "region": "eu-1"});

#[tokio::test]
async fn an_edited_policy_serves_after_a_reload() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    assert_eq!(h.evaluate("dark-mode", EU("u")).await["value"], true);
    let digest_before = h.get("/api/v1/policies").await.json()["digest"].clone();

    write_atomically(
        &dir.path().join("dark_mode.sigil"),
        &flag("dark_mode", "param percent: int = 0, min: 0, max: 100\n\nwhen bucket < percent {\n  enable(reason: rollout)\n}"),
    );
    let (status, body) = reload(&h).await;
    assert_eq!((status, &body["result"]), (StatusCode::OK, &json!("loaded")));
    assert_ne!(body["digest"], digest_before);
    assert_eq!(h.evaluate("dark-mode", EU("u")).await["value"], false);

    let metrics = h.metrics().await;
    assert_eq!(metric(&metrics, "featuregate_reloads_total{result=\"loaded\"}"), Some(2.0), "startup and this reload");
}

#[tokio::test]
async fn a_reload_of_unchanged_files_changes_nothing_unless_forced() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    let outcome = h.state.reloader.reload(featuregate::reload::Trigger::Poll, false).await;
    assert!(matches!(outcome, featuregate::store::ReloadOutcome::Unchanged { .. }), "{outcome:?}");
    let metrics = h.metrics().await;
    assert_eq!(metric(&metrics, "featuregate_reloads_total{result=\"unchanged\"}"), Some(1.0));
    // The endpoint forces: an operator who asks gets a fresh compile.
    assert_eq!(reload(&h).await.1["result"], "loaded");
}

#[tokio::test]
async fn flags_come_and_go_with_their_files() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    std::fs::write(dir.path().join("fresh.sigil"), flag("fresh", "when user.beta {\n  enable(reason: beta_tester)\n}")).unwrap();
    std::fs::remove_file(dir.path().join("beta_api.sigil")).unwrap();
    assert_eq!(reload(&h).await.0, StatusCode::OK);

    let keys: Vec<String> =
        h.get("/api/v1/flags").await.json()["flags"].as_array().unwrap().iter().map(|f| f["key"].as_str().unwrap().into()).collect();
    assert_eq!(keys, ["dark-mode", "fresh", "new-checkout", "search-v2"]);
    let fresh = h.evaluate("fresh", json!({"targetingKey": "u", "region": "eu-1", "beta": true})).await;
    assert_eq!(fresh["value"], true);
    let gone = h.post("/ofrep/v1/evaluate/flags/beta-api", r#"{"context":{"targetingKey":"u"}}"#).await;
    assert_eq!(gone.status, StatusCode::NOT_FOUND);
    assert_eq!(h.get("/api/v1/policies").await.json()["flags"].as_array().unwrap().len(), 4);
}

#[tokio::test]
async fn a_broken_reload_keeps_the_last_known_good_bundle() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    let good_digest = h.get("/api/v1/policies").await.json()["digest"].clone();

    write_atomically(&dir.path().join("dark_mode.sigil"), "policy flags.dark_mode: FeatureRollout@1\nwhen bucket <");
    let (status, body) = reload(&h).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["result"], "failed");
    assert_eq!(body["serving"], good_digest);
    assert!(body["error"].as_str().unwrap().contains("dark_mode.sigil"), "{body}");

    // Still serving the old policy, and saying why the new one isn't.
    assert_eq!(h.evaluate("dark-mode", EU("u")).await["value"], true);
    let policies = h.get("/api/v1/policies").await.json();
    assert_eq!(policies["digest"], good_digest);
    assert!(policies["lastReloadError"].as_str().unwrap().contains("dark_mode.sigil"));
    let metrics = h.metrics().await;
    assert_eq!(metric(&metrics, "featuregate_reloads_total{result=\"failed\"}"), Some(1.0));

    // The next good reload clears the error.
    write_atomically(&dir.path().join("dark_mode.sigil"), &flag("dark_mode", "when true {\n  enable(reason: rollout)\n}"));
    assert_eq!(reload(&h).await.0, StatusCode::OK);
    assert_eq!(h.get("/api/v1/policies").await.json()["lastReloadError"], json!(null));
}

/// Every way a flag policy could try to shake off the platform's guardrail.
#[rstest]
#[case::omitted("omits the guardrail", "policy flags.sneaky: FeatureRollout@1\n\nwhen true {\n  enable(reason: enterprise)\n}\n")]
#[case::gated(
    "gates the guardrail",
    "policy flags.sneaky: FeatureRollout@1\n\nuse platform.guardrails\n\nwhen user.beta {\n  guardrails()\n}\n\nwhen true {\n  enable(reason: enterprise)\n}\n"
)]
#[case::redefined("redefines it", "policy platform.guardrails: FeatureRollout@1\n\nwhen false {\n  disable(reason: kill_switch)\n}\n")]
#[tokio::test]
async fn a_guardrail_violation_is_refused(#[case] what: &str, #[case] sneaky: &str) {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    std::fs::write(dir.path().join("sneaky.sigil"), sneaky).unwrap();
    let (status, body) = reload(&h).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "a flag policy that {what} must not load: {body}");
    assert!(body["error"].as_str().unwrap().contains("platform.guardrails"), "{body}");
    assert_eq!(h.evaluate("dark-mode", EU("u")).await["value"], true, "the last known good bundle still serves");
}

#[tokio::test]
async fn a_flag_policy_cannot_outrank_the_guardrail() {
    // This policy does everything a policy can to enable the flag: the
    // highest-ranked enable reason, unconditionally. disable outranks enable,
    // and kill_switch outranks every other disable, so the guardrail still wins.
    let dir = sample_dir();
    std::fs::write(dir.path().join("greedy.sigil"), flag("greedy", "when true {\n  enable(reason: enterprise)\n}")).unwrap();
    let h = harness(&[("FEATUREGATE_POLICIES", dir.path().to_str().unwrap()), ("FEATUREGATE_KILLED_FLAGS", "greedy")]).await;

    let ready = h.evaluate("greedy", json!({"targetingKey": "u", "plan": "enterprise", "region": "eu-1", "beta": true})).await;
    assert_eq!(ready["metadata"]["sigil.reason"], "kill_switch");
    let unready_and_killed = h.evaluate("greedy", json!({"targetingKey": "u", "plan": "enterprise", "region": "ap-1"})).await;
    assert_eq!(unready_and_killed["metadata"]["sigil.reason"], "kill_switch", "kill_switch ranks first among the disables");

    let h = harness(&[("FEATUREGATE_POLICIES", dir.path().to_str().unwrap())]).await;
    let unready = h.evaluate("greedy", json!({"targetingKey": "u", "plan": "enterprise", "region": "ap-1"})).await;
    assert_eq!((unready["value"].clone(), unready["metadata"]["sigil.reason"].clone()), (json!(false), json!("region_not_ready")));
    let ok = h.evaluate("greedy", EU("u")).await;
    assert_eq!(ok["value"], true, "without a guardrail firing, the flag's own rule decides");
}

#[tokio::test]
async fn a_policy_in_the_flag_namespace_must_name_a_flag() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    std::fs::write(dir.path().join("odd.sigil"), flag("Odd", "when true {\n  enable(reason: rollout)\n}")).unwrap();
    let (status, body) = reload(&h).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    assert!(body["error"].as_str().unwrap().contains("flags.Odd"), "{body}");
}

#[tokio::test]
async fn startup_refuses_a_directory_that_does_not_load() {
    let dir = sample_dir();
    std::fs::write(dir.path().join("broken.sigil"), "policy flags.broken: FeatureRollout@1\nwhen bucket <").unwrap();
    let err = try_harness(&[("FEATUREGATE_POLICIES", dir.path().to_str().unwrap())]).await.err().expect("startup fails");
    assert!(err.contains("broken.sigil"), "{err}");

    let err = try_harness(&[("FEATUREGATE_POLICIES", "/nonexistent/flags")]).await.err().expect("startup fails");
    assert!(err.contains("FEATUREGATE_POLICIES"), "{err}");
}

#[tokio::test]
async fn an_empty_directory_is_refused_at_startup_and_on_reload() {
    let dir = sample_dir();
    let empty = tempfile::tempdir().unwrap();
    let err = try_harness(&[("FEATUREGATE_POLICIES", empty.path().to_str().unwrap())]).await.err().expect("startup fails");
    assert!(err.contains("holds no *.sigil files"), "{err}");

    // A mount that goes empty under a running service keeps the flags serving.
    let h = serving(dir.path()).await;
    for entry in std::fs::read_dir(dir.path()).unwrap() {
        std::fs::remove_file(entry.unwrap().path()).unwrap();
    }
    assert_eq!(reload(&h).await.0, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(h.evaluate("dark-mode", EU("u")).await["value"], true);
}

/// A policy that fails when evaluated answers off with reason ERROR, is
/// counted by kind, and doesn't fail the request.
#[rstest]
#[case::conflict(
    "conflict",
    "when true {\n  enable(reason: rollout, variant: \"a\")\n}\n\nwhen true {\n  enable(reason: rollout, variant: \"b\")\n}"
)]
#[case::assertion("assertion", "assert(\"never_beta\", not user.beta)\n\nwhen true {\n  enable(reason: rollout)\n}")]
#[tokio::test]
async fn a_failing_evaluation_fails_closed(#[case] kind: &str, #[case] rules: &str) {
    let dir = sample_dir();
    std::fs::write(dir.path().join("failing.sigil"), flag("failing", rules)).unwrap();
    let h = serving(dir.path()).await;

    let reply = h.post("/ofrep/v1/evaluate/flags/failing", r#"{"context":{"targetingKey":"u","region":"eu-1","beta":true}}"#).await;
    assert_eq!(reply.status, StatusCode::OK, "{}", reply.text);
    let body = reply.json();
    assert_eq!((&body["value"], &body["reason"], &body["variant"]), (&json!(false), &json!("ERROR"), &json!("off")));
    assert_eq!(body["metadata"]["sigil.error"], kind);

    let metrics = h.metrics().await;
    assert_eq!(metric(&metrics, &format!("featuregate_evaluation_errors_total{{kind=\"{kind}\"}}")), Some(1.0), "{metrics}");
    assert_eq!(metric(&metrics, "featuregate_evaluations_total{decision=\"disable\",flag=\"failing\",reason=\"error\"}"), Some(1.0));

    // The bulk answer carries the failed flag like any other, and the rest are fine.
    let bulk = h.post("/ofrep/v1/evaluate/flags", r#"{"context":{"targetingKey":"u","region":"eu-1","beta":true}}"#).await.json();
    let flags = bulk["flags"].as_array().unwrap();
    assert_eq!(flags.iter().find(|f| f["key"] == "failing").unwrap()["reason"], "ERROR");
    assert_eq!(flags.iter().find(|f| f["key"] == "dark-mode").unwrap()["reason"], "SPLIT");
}

#[tokio::test]
async fn the_manifest_must_name_served_flags_and_be_valid() {
    let dir = sample_dir();
    let h = serving(dir.path()).await;
    let manifest = dir.path().join("flags.yaml");
    for (text, hint) in [
        ("flags:\n  search-v3:\n    type: string\n    off: control\n", "search-v3"),
        ("flags:\n  search-v2:\n    type: string\n", "needs an `off:`"),
        ("flags:\n  search-v2:\n    type: number\n", "doesn't parse"),
    ] {
        write_atomically(&manifest, text);
        let (status, body) = reload(&h).await;
        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "{text}");
        assert!(body["error"].as_str().unwrap().contains(hint), "{body}");
    }
    // Removing a flag's policy while the manifest still declares it is refused too.
    std::fs::copy(common::sample_dir().path().join("flags.yaml"), &manifest).unwrap();
    std::fs::remove_file(dir.path().join("search_v2.sigil")).unwrap();
    let (status, body) = reload(&h).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    assert!(body["error"].as_str().unwrap().contains("flags.yaml") && body["error"].as_str().unwrap().contains("search-v2"), "{body}");
    assert_eq!(h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "pro", "region": "eu-1"})).await["value"], "hybrid");
}

#[tokio::test]
async fn a_failing_string_flag_fails_closed_to_its_off_value() {
    let dir = sample_dir();
    std::fs::write(
        dir.path().join("failing.sigil"),
        flag("failing", "assert(\"never\", false)\n\nwhen true {\n  enable(reason: rollout, variant: \"a\")\n}"),
    )
    .unwrap();
    std::fs::write(dir.path().join("flags.yaml"), "flags:\n  failing:\n    type: string\n    off: fallback\n").unwrap();
    let h = serving(dir.path()).await;
    let body = h.evaluate("failing", json!({"targetingKey": "u", "region": "eu-1"})).await;
    assert_eq!((&body["reason"], &body["value"], &body["variant"]), (&json!("ERROR"), &json!("fallback"), &json!("fallback")));
}

#[tokio::test]
async fn a_kill_switch_name_no_flag_has_is_a_startup_error_and_a_reload_warning() {
    let dir = sample_dir();
    let dir_path = dir.path().to_str().unwrap();
    let err = try_harness(&[("FEATUREGATE_POLICIES", dir_path), ("FEATUREGATE_KILLED_FLAGS", "dark-mode,dark-mood")])
        .await
        .err()
        .expect("startup fails");
    assert!(err.contains("dark-mood") && !err.contains("dark-mode,"), "{err}");

    let h = harness(&[("FEATUREGATE_POLICIES", dir_path), ("FEATUREGATE_KILLED_FLAGS", "beta-api")]).await;
    assert_eq!(metric(&h.metrics().await, "featuregate_killed_flags_unmatched"), Some(0.0));
    // A reload may remove the flag the kill switch names: serving goes on, and the gauge says so.
    std::fs::remove_file(dir.path().join("beta_api.sigil")).unwrap();
    assert_eq!(reload(&h).await.0, StatusCode::OK);
    assert_eq!(metric(&h.metrics().await, "featuregate_killed_flags_unmatched"), Some(1.0));
}
