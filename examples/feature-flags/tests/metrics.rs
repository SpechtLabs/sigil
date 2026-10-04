//! The metric contract: series names, labels and their bounds, as the
//! dashboard and the load test rely on them.

mod common;

use common::{harness, metric};
use serde_json::json;

#[tokio::test]
async fn evaluations_are_counted_by_flag_decision_and_reason() {
    let h = harness(&[]).await;
    h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "eu-1"})).await;
    h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "eu-1"})).await;
    h.evaluate("search-v2", json!({"targetingKey": "u", "plan": "enterprise", "region": "ap-1"})).await;
    h.evaluate("dark-mode", json!({"targetingKey": "u", "region": "eu-1"})).await;

    let m = h.metrics().await;
    assert_eq!(metric(&m, "featuregate_evaluations_total{decision=\"enable\",flag=\"search-v2\",reason=\"enterprise\"}"), Some(2.0), "{m}");
    assert_eq!(metric(&m, "featuregate_evaluations_total{decision=\"disable\",flag=\"search-v2\",reason=\"region_not_ready\"}"), Some(1.0));
    assert_eq!(metric(&m, "featuregate_evaluations_total{decision=\"enable\",flag=\"dark-mode\",reason=\"rollout\"}"), Some(1.0));
    assert_eq!(metric(&m, "featuregate_evaluation_duration_seconds_count{flag=\"search-v2\"}"), Some(3.0));
    assert!(metric(&m, "featuregate_evaluation_duration_seconds_sum{flag=\"search-v2\"}").unwrap() > 0.0);
}

#[tokio::test]
async fn a_bulk_request_counts_every_flag() {
    let h = harness(&[]).await;
    h.post("/ofrep/v1/evaluate/flags", r#"{"context":{"targetingKey":"u","region":"eu-1"}}"#).await;
    let m = h.metrics().await;
    for flag in ["beta-api", "dark-mode", "new-checkout", "search-v2"] {
        assert_eq!(metric(&m, &format!("featuregate_evaluation_duration_seconds_count{{flag=\"{flag}\"}}")), Some(1.0), "{flag}");
    }
}

#[tokio::test]
async fn http_metrics_use_route_templates_and_unknown_paths_share_one_label() {
    let h = harness(&[]).await;
    for i in 0..30 {
        // Unknown flags, and paths nothing serves: neither may grow the label set.
        h.post(&format!("/ofrep/v1/evaluate/flags/unknown-{i}"), r#"{"context":{"targetingKey":"u"}}"#).await;
        h.get(&format!("/scan/{i}")).await;
    }
    h.get("/healthz").await;
    let m = h.metrics().await;
    assert_eq!(
        metric(&m, "featuregate_http_requests_total{method=\"POST\",route=\"/ofrep/v1/evaluate/flags/{key}\",status=\"404\"}"),
        Some(30.0),
        "{m}"
    );
    assert_eq!(metric(&m, "featuregate_http_requests_total{method=\"GET\",route=\"-\",status=\"404\"}"), Some(30.0));
    assert_eq!(metric(&m, "featuregate_http_requests_total{method=\"GET\",route=\"/healthz\",status=\"200\"}"), Some(1.0));
    assert!(!m.contains("unknown-1"), "a client-chosen name reached a label");
    assert!(!m.contains("/scan/"), "a client-chosen path reached a label");
    assert!(!m.contains("flag=\"-\"") || m.contains("flag=\"-\"}"), "the no-flag label is the bare dash");
    assert_eq!(metric(&m, "featuregate_http_requests_in_flight"), Some(1.0), "the scrape itself is the one request in flight");
    assert!(
        metric(&m, "featuregate_http_request_duration_seconds_count{method=\"POST\",route=\"/ofrep/v1/evaluate/flags/{key}\"}").unwrap()
            >= 30.0
    );
}

#[tokio::test]
async fn the_loaded_bundle_is_described() {
    let h = harness(&[]).await;
    let digest = h.get("/api/v1/policies").await.json()["digest"].as_str().unwrap().to_owned();
    let m = h.metrics().await;
    assert_eq!(metric(&m, &format!("featuregate_loaded_info{{digest=\"{digest}\",source=\"embedded\"}}")), Some(1.0), "{m}");
    assert_eq!(metric(&m, "featuregate_flags_loaded"), Some(4.0));
    assert_eq!(metric(&m, "featuregate_reloads_total{result=\"loaded\"}"), Some(1.0));
    assert_eq!(metric(&m, "featuregate_build_info{version=\"dev\"}"), Some(1.0));
}

#[tokio::test]
async fn the_metrics_endpoint_serves_the_text_format() {
    let h = harness(&[]).await;
    h.evaluate("dark-mode", json!({"targetingKey": "u", "region": "eu-1"})).await;
    let reply = h.get("/metrics").await;
    assert!(reply.headers["content-type"].to_str().unwrap().starts_with("text/plain; version=0.0.4"));
    assert!(reply.text.contains("# TYPE featuregate_evaluations_total counter"));
}

#[cfg(target_os = "linux")]
#[tokio::test]
async fn process_metrics_are_exported_on_linux() {
    let h = harness(&[]).await;
    let m = h.metrics().await;
    assert!(m.contains("process_cpu_seconds_total"), "{m}");
    assert!(m.contains("process_resident_memory_bytes"));
}

#[tokio::test]
async fn the_method_label_is_a_fixed_set() {
    let h = harness(&[]).await;
    for method in ["PROPFIND", "MKCOL", "BREW"] {
        h.send(method, "/healthz", None, &[]).await;
        h.send(method, "/nowhere", None, &[]).await;
    }
    h.get("/healthz").await;
    let m = h.metrics().await;
    assert_eq!(metric(&m, "featuregate_http_requests_total{method=\"OTHER\",route=\"/healthz\",status=\"405\"}"), Some(3.0), "{m}");
    assert_eq!(metric(&m, "featuregate_http_requests_total{method=\"OTHER\",route=\"-\",status=\"404\"}"), Some(3.0));
    assert!(!m.contains("PROPFIND") && !m.contains("MKCOL") && !m.contains("BREW"), "{m}");
}

#[tokio::test]
async fn the_module_gauge_says_whether_it_loaded_precompiled() {
    let h = harness(&[]).await;
    let want = f64::from(u8::from(common::module().is_precompiled()));
    assert_eq!(metric(&h.metrics().await, "featuregate_module_precompiled"), Some(want));
    // Fuel metering isn't in the precompiled artifact, so that module compiles.
    let fuel = harness(&[("FEATUREGATE_EVALUATION_FUEL", "1000000")]).await;
    assert_eq!(metric(&fuel.metrics().await, "featuregate_module_precompiled"), Some(0.0));
}
