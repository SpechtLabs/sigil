//! What a request leaves in the telemetry: its server span, the evaluation
//! span under it (`flag`, `sigil.decision`, `sigil.reason`, `ofrep.reason`),
//! and JSON log lines that carry the trace and span ids once.

mod common;

use common::observe::{attr, observed};
use common::{harness, sample_dir};
use serde_json::{Value, json};

const TRACE_ID: &str = "4bf92f3577b34da6a3ce929d0e0e4736";
const TRACEPARENT: &str = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

#[tokio::test]
async fn a_request_leaves_a_server_span_and_an_evaluation_span_in_the_callers_trace() {
    let obs = observed();
    let h = harness(&[]).await;
    let reply = h
        .send(
            "POST",
            "/ofrep/v1/evaluate/flags/search-v2",
            Some(r#"{"context":{"targetingKey":"user-1","plan":"enterprise","region":"eu-1"}}"#),
            &[("traceparent", TRACEPARENT)],
        )
        .await;
    assert_eq!(reply.status, 200);

    let ours: Vec<_> = obs.spans().into_iter().filter(|s| s.span_context.trace_id().to_string() == TRACE_ID).collect();
    let request = ours.iter().find(|s| s.name == "http request").expect("a server span in the caller's trace");
    assert_eq!(attr(request, "http.route").as_deref(), Some("/ofrep/v1/evaluate/flags/{key}"));
    assert_eq!(attr(request, "http.request.method").as_deref(), Some("POST"));
    assert_eq!(attr(request, "http.response.status_code").as_deref(), Some("200"));
    assert_eq!(request.parent_span_id.to_string(), "00f067aa0ba902b7", "it continues the caller's span");

    let eval = ours.iter().find(|s| s.name == "evaluate flag").expect("an evaluation span in the same trace");
    assert_eq!(eval.parent_span_id, request.span_context.span_id(), "the evaluation is a child of the request");
    assert_eq!(attr(eval, "flag").as_deref(), Some("search-v2"));
    assert_eq!(attr(eval, "sigil.policy").as_deref(), Some("flags.search_v2"));
    assert_eq!(attr(eval, "sigil.decision").as_deref(), Some("enable"));
    assert_eq!(attr(eval, "sigil.reason").as_deref(), Some("enterprise"));
    assert_eq!(attr(eval, "ofrep.reason").as_deref(), Some("TARGETING_MATCH"));
}

#[tokio::test]
async fn a_bulk_request_has_one_evaluation_span_per_flag() {
    let obs = observed();
    let h = harness(&[]).await;
    let traceparent = "00-11111111111111111111111111111111-2222222222222222-01";
    h.send(
        "POST",
        "/ofrep/v1/evaluate/flags",
        Some(r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#),
        &[("traceparent", traceparent)],
    )
    .await;
    let mut flags: Vec<_> = obs
        .spans()
        .into_iter()
        .filter(|s| s.span_context.trace_id().to_string() == "11111111111111111111111111111111" && s.name == "evaluate flag")
        .filter_map(|s| attr(&s, "flag"))
        .collect();
    flags.sort();
    assert_eq!(flags, ["beta-api", "dark-mode", "new-checkout", "search-v2"]);
}

#[tokio::test]
async fn an_invalid_traceparent_starts_a_fresh_trace() {
    let obs = observed();
    let h = harness(&[]).await;
    h.send("GET", "/api/v1/policies", None, &[("traceparent", "garbage")]).await;
    let spans = obs.spans();
    let request = spans
        .iter()
        .find(|s| s.name == "http request" && attr(s, "http.route").as_deref() == Some("/api/v1/policies"))
        .expect("the request still gets a span");
    assert!(!request.parent_span_id.to_string().chars().any(|c| c != '0'), "no parent: {}", request.parent_span_id);
}

#[tokio::test]
async fn a_failed_evaluation_marks_its_span_and_logs_an_error() {
    let obs = observed();
    let dir = sample_dir();
    std::fs::write(
        dir.path().join("failing.sigil"),
        "policy flags.failing: FeatureRollout@1\n\nuse platform.guardrails\n\nguardrails()\n\nassert(\"never\", false)\n\nwhen true {\n  enable(reason: rollout)\n}\n",
    )
    .unwrap();
    let h = harness(&[("FEATUREGATE_POLICIES", dir.path().to_str().unwrap())]).await;
    let traceparent = "00-33333333333333333333333333333333-4444444444444444-01";
    h.send(
        "POST",
        "/ofrep/v1/evaluate/flags/failing",
        Some(r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#),
        &[("traceparent", traceparent)],
    )
    .await;

    let eval = obs
        .spans()
        .into_iter()
        .find(|s| s.span_context.trace_id().to_string() == "33333333333333333333333333333333" && s.name == "evaluate flag")
        .unwrap();
    assert_eq!(attr(&eval, "ofrep.reason").as_deref(), Some("ERROR"));
    assert_eq!(attr(&eval, "sigil.error").as_deref(), Some("assertion"));
    assert_eq!(attr(&eval, "sigil.decision"), None, "a failed evaluation decided nothing");

    let line = obs
        .lines()
        .into_iter()
        .find(|l| l["trace_id"] == "33333333333333333333333333333333" && l["level"] == "error")
        .expect("an error line");
    assert_eq!(
        (&line["msg"], &line["flag"], &line["kind"]),
        (&json!("evaluation failed, the flag answers off"), &json!("failing"), &json!("assertion"))
    );
}

#[tokio::test]
async fn log_lines_are_json_with_the_ids_once() {
    let obs = observed();
    let h = harness(&[]).await;
    let traceparent = "00-55555555555555555555555555555555-6666666666666666-01";
    h.send(
        "POST",
        "/ofrep/v1/evaluate/flags/dark-mode",
        Some(r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#),
        &[("traceparent", traceparent)],
    )
    .await;

    let mine: Vec<Value> = obs.lines().into_iter().filter(|l| l["trace_id"] == "55555555555555555555555555555555").collect();
    let evaluated = mine.iter().find(|l| l["msg"] == "flag evaluated").expect("an evaluation line");
    assert_eq!(evaluated["level"], "info");
    assert_eq!(
        (&evaluated["flag"], &evaluated["policy"], &evaluated["decision"], &evaluated["reason"], &evaluated["ofrep_reason"]),
        (&json!("dark-mode"), &json!("flags.dark_mode"), &json!("enable"), &json!("rollout"), &json!("SPLIT"))
    );
    assert!(evaluated["duration_ms"].is_number());
    assert!(evaluated["time"].as_str().unwrap().ends_with('Z'));
    assert_eq!(evaluated["span_id"].as_str().unwrap().len(), 16);
    // trace_id and span_id are each one key of the object: JSON can't hold one twice, so
    // count them in the raw text instead.
    let raw = evaluated.to_string();
    assert_eq!(raw.matches("\"trace_id\"").count(), 1);
    assert_eq!(raw.matches("\"span_id\"").count(), 1);

    let access = mine.iter().find(|l| l["msg"] == "request").expect("an access line");
    assert_eq!(
        (&access["method"], &access["route"], &access["status"]),
        (&json!("POST"), &json!("/ofrep/v1/evaluate/flags/{key}"), &json!(200))
    );
    assert_ne!(access["span_id"], evaluated["span_id"], "the access line is the request span's, the evaluation line the evaluation span's");
}

#[tokio::test]
async fn lines_outside_a_request_have_no_ids() {
    let obs = observed();
    let _h = harness(&[]).await;
    let startup = obs.lines().into_iter().find(|l| l["msg"] == "policies loaded").expect("the startup line");
    assert!(startup.get("trace_id").is_none() && startup.get("span_id").is_none(), "{startup}");
    assert_eq!(startup["flags"], 4);
}

#[tokio::test]
async fn probes_and_scrapes_log_at_debug_only() {
    let obs = observed();
    let h = harness(&[]).await;
    h.send("GET", "/healthz", None, &[("traceparent", "00-77777777777777777777777777777777-8888888888888888-01")]).await;
    assert!(!obs.lines().iter().any(|l| l["trace_id"] == "77777777777777777777777777777777"), "no info line for a probe");
}
