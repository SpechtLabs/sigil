//! The HTTP API, end to end in process: the OFREP fixtures, OFREP semantics
//! (errors, ETags), the operator endpoints, probes and the kill switch.

mod common;

use axum::http::StatusCode;
use featuregate::cases::Case;
use serde_json::json;

#[tokio::test]
async fn every_fixture_case_answers_as_expected() {
    let h = common::harness(&[]).await;
    let mut failures = Vec::new();
    for case in Case::all() {
        let reply = h.post(&case.path(), &case.body()).await;
        let diff = case.mismatches(reply.status.as_u16(), &reply.json());
        if !diff.is_empty() {
            failures.push(format!("{}: {}", case.name, diff.join("; ")));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

#[tokio::test]
async fn responses_are_json_with_ofrep_fields() {
    let h = common::harness(&[]).await;
    let reply = h.post("/ofrep/v1/evaluate/flags/dark-mode", r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#).await;
    assert_eq!(reply.headers["content-type"], "application/json");
    let body = reply.json();
    assert_eq!(body["key"], "dark-mode");
    assert_eq!(body["metadata"]["sigil.policy"], "flags.dark_mode");
    assert_eq!(body["metadata"]["sigil.decision"], "enable");
}

#[tokio::test]
async fn a_context_attribute_reaches_the_policy() {
    let h = common::harness(&[]).await;
    let on = h.evaluate("new-checkout", json!({"targetingKey": "user-5", "region": "eu-1", "cohort": "checkout-pilot"})).await;
    let off = h.evaluate("new-checkout", json!({"targetingKey": "user-5", "region": "eu-1", "cohort": "other"})).await;
    assert_eq!((on["value"].clone(), off["value"].clone()), (json!(true), json!(false)));
}

#[tokio::test]
async fn the_same_user_always_gets_the_same_answer() {
    let h = common::harness(&[]).await;
    let ctx = json!({"targetingKey": "user-33", "region": "us-1"});
    let first = h.evaluate("new-checkout", ctx.clone()).await;
    for _ in 0..5 {
        assert_eq!(h.evaluate("new-checkout", ctx.clone()).await, first);
    }
}

#[tokio::test]
async fn the_rollout_percentage_holds_across_many_users() {
    let h = common::harness(&[]).await;
    let mut on = 0;
    for i in 0..400 {
        let r = h.evaluate("new-checkout", json!({"targetingKey": format!("load-user-{i}"), "region": "eu-1"})).await;
        on += usize::from(r["value"] == json!(true));
    }
    // 25% of 400 is 100; the hash is fair, so stay well inside 15%..35%.
    assert!((60..=140).contains(&on), "{on} of 400 users were in a 25% rollout");
}

#[tokio::test]
async fn ofrep_errors_carry_the_key_when_there_is_one() {
    let h = common::harness(&[]).await;
    let unknown = h.post("/ofrep/v1/evaluate/flags/nope", r#"{"context":{"targetingKey":"u"}}"#).await;
    assert_eq!(unknown.status, StatusCode::NOT_FOUND);
    assert_eq!(unknown.json()["key"], "nope");
    assert!(unknown.json()["errorDetails"].as_str().unwrap().contains("/api/v1/flags"));

    let bad_context = h.post("/ofrep/v1/evaluate/flags/dark-mode", r#"{"context":{}}"#).await;
    assert_eq!(bad_context.json()["key"], "dark-mode");

    let bad_json = h.post("/ofrep/v1/evaluate/flags/dark-mode", "nope").await;
    assert_eq!(bad_json.status, StatusCode::BAD_REQUEST);
    assert!(bad_json.json().get("key").is_none());
    assert_eq!(bad_json.json()["errorCode"], "PARSE_ERROR");
}

#[tokio::test]
async fn a_body_without_a_context_is_a_missing_targeting_key() {
    let h = common::harness(&[]).await;
    let reply = h.post("/ofrep/v1/evaluate/flags", "{}").await;
    assert_eq!(reply.status, StatusCode::BAD_REQUEST);
    assert_eq!(reply.json()["errorCode"], "TARGETING_KEY_MISSING");
}

#[tokio::test]
async fn an_oversized_body_is_refused_as_json() {
    let h = common::harness(&[]).await;
    let big = format!(r#"{{"context":{{"targetingKey":"u","blob":"{}"}}}}"#, "x".repeat(70_000));
    let reply = h.post("/ofrep/v1/evaluate/flags/dark-mode", &big).await;
    assert_eq!(reply.status, StatusCode::PAYLOAD_TOO_LARGE);
    assert_eq!(reply.json()["errorCode"], "PARSE_ERROR");
}

#[tokio::test]
async fn bulk_carries_an_etag_and_honours_if_none_match() {
    let h = common::harness(&[]).await;
    let body = r#"{"context":{"targetingKey":"user-1","plan":"pro","region":"eu-1"}}"#;
    let first = h.post("/ofrep/v1/evaluate/flags", body).await;
    assert_eq!(first.status, StatusCode::OK);
    let etag = first.headers["etag"].to_str().unwrap().to_owned();
    assert!(etag.starts_with('"') && etag.ends_with('"'), "{etag}");

    for header in [etag.clone(), format!("W/{etag}"), format!("\"other\", {etag}"), "*".to_owned()] {
        let second = h.send("POST", "/ofrep/v1/evaluate/flags", Some(body), &[("if-none-match", &header)]).await;
        assert_eq!(second.status, StatusCode::NOT_MODIFIED, "If-None-Match: {header}");
        assert!(second.text.is_empty());
        assert_eq!(second.headers["etag"].to_str().unwrap(), etag);
    }
    let stale = h.send("POST", "/ofrep/v1/evaluate/flags", Some(body), &[("if-none-match", "\"stale\"")]).await;
    assert_eq!(stale.status, StatusCode::OK);

    // Another context is another answer, and another tag.
    let other = h.post("/ofrep/v1/evaluate/flags", r#"{"context":{"targetingKey":"user-1","plan":"enterprise","region":"eu-1"}}"#).await;
    assert_ne!(other.headers["etag"].to_str().unwrap(), etag);
}

#[tokio::test]
async fn bulk_lists_every_flag_sorted_by_key() {
    let h = common::harness(&[]).await;
    let reply = h.post("/ofrep/v1/evaluate/flags", r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#).await;
    let keys: Vec<_> = reply.json()["flags"].as_array().unwrap().iter().map(|f| f["key"].as_str().unwrap().to_owned()).collect();
    assert_eq!(keys, ["beta-api", "dark-mode", "new-checkout", "search-v2"]);
}

#[tokio::test]
async fn the_kill_switch_turns_a_flag_off_for_everyone() {
    let h = common::harness(&[("FEATUREGATE_KILLED_FLAGS", "dark-mode")]).await;
    let killed = h.evaluate("dark-mode", json!({"targetingKey": "u", "region": "eu-1"})).await;
    assert_eq!(killed["reason"], "DISABLED");
    assert_eq!(killed["value"], false);
    assert_eq!(killed["metadata"]["sigil.reason"], "kill_switch");
    // Another flag is unaffected.
    let other = h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "eu-1"})).await;
    assert_eq!(other["value"], "semantic");
}

#[tokio::test]
async fn the_flag_and_policy_listings_name_what_is_served() {
    let h = common::harness(&[]).await;
    let flags = h.get("/api/v1/flags").await.json();
    assert_eq!(flags["flags"][0], json!({"key": "beta-api", "policy": "flags.beta_api"}));
    let policies = h.get("/api/v1/policies").await.json();
    assert_eq!(policies["source"], "embedded");
    assert_eq!(policies["digest"].as_str().unwrap().len(), 12);
    assert_eq!(policies["lastReloadError"], json!(null));
    assert_eq!(policies["flags"].as_array().unwrap().len(), 4);
}

#[tokio::test]
async fn probes_answer_and_readiness_follows_shutdown() {
    let h = common::harness(&[]).await;
    assert_eq!(h.get("/healthz").await.status, StatusCode::OK);
    let ready = h.get("/readyz").await;
    assert_eq!(ready.status, StatusCode::OK);
    assert_eq!(ready.json()["flags"], 4);

    h.state.draining.store(true, std::sync::atomic::Ordering::Release);
    assert_eq!(h.get("/readyz").await.status, StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(h.get("/healthz").await.status, StatusCode::OK, "liveness doesn't follow the drain");
}

#[tokio::test]
async fn unknown_routes_and_wrong_methods_are_refused() {
    let h = common::harness(&[]).await;
    let missing = h.get("/nope").await;
    assert_eq!(missing.status, StatusCode::NOT_FOUND);
    assert!(missing.json()["error"].as_str().is_some());
    let wrong = h.send("GET", "/ofrep/v1/evaluate/flags", None, &[]).await;
    assert_eq!(wrong.status, StatusCode::METHOD_NOT_ALLOWED);
    let wrong = h.send("PUT", "/healthz", None, &[]).await;
    assert_eq!(wrong.status, StatusCode::METHOD_NOT_ALLOWED);
}

#[tokio::test]
async fn concurrent_evaluations_all_answer_correctly() {
    let h = std::sync::Arc::new(common::harness(&[]).await);
    let mut tasks = tokio::task::JoinSet::new();
    for i in 0..64 {
        let h = std::sync::Arc::clone(&h);
        tasks.spawn(async move {
            let r = h.evaluate("search-v2", json!({"targetingKey": format!("u{i}"), "plan": "enterprise", "region": "eu-1"})).await;
            assert_eq!(r["value"], "semantic");
            let b = h.post("/ofrep/v1/evaluate/flags", r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#).await;
            assert_eq!(b.status, StatusCode::OK);
        });
    }
    while let Some(done) = tasks.join_next().await {
        done.unwrap();
    }
}

#[tokio::test]
async fn a_string_flag_answers_strings_when_off_killed_or_failed() {
    // search-v2 is `type: string, off: control` in policies/flags/flags.yaml.
    let h = common::harness(&[("FEATUREGATE_KILLED_FLAGS", "search-v2")]).await;
    let killed = h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "eu-1"})).await;
    assert_eq!(
        (killed["value"].clone(), killed["variant"].clone(), killed["reason"].clone()),
        (json!("control"), json!("control"), json!("DISABLED"))
    );
    let region = h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "ap-1"})).await;
    assert_eq!(region["value"], "control");
    // A boolean flag is unaffected.
    let dark = h.evaluate("dark-mode", json!({"targetingKey": "u", "region": "ap-1"})).await;
    assert_eq!(dark["value"], false);
}
