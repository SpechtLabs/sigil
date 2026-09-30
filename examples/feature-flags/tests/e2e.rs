//! The e2e suite tests featuregate from the outside, over HTTP, against the
//! docker compose stack in examples/feature-flags/. It never imports the
//! service's code beyond the request fixtures: every assertion is about the
//! wire contract, metrics in Mimir, logs in Loki, traces in Tempo, profiles in
//! Pyroscope and the dashboard in Grafana, which is what an OpenFeature SDK or
//! an operator sees.
//!
//! It is compiled only with `--features e2e`: start the stack and run it with
//! `mise run e2e`. The endpoints come from FEATUREGATE_URL, MIMIR_URL,
//! TEMPO_URL, LOKI_URL, PYROSCOPE_URL and GRAFANA_URL (the defaults match the
//! ports docker-compose.yaml publishes). The hot reload specs edit the flag
//! policies under FEATUREGATE_POLICIES_DIR, the host side of the bind mount,
//! and always restore them.
#![cfg(feature = "e2e")]

use std::future::Future;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use featuregate::cases::Case;
use reqwest::Client;
use serde_json::{Value, json};
use tokio::sync::{Mutex, MutexGuard};

/// The specs that edit the mounted policies take this, so they run one at a time.
static POLICIES: Mutex<()> = Mutex::const_new(());

fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).ok().filter(|v| !v.is_empty()).unwrap_or_else(|| fallback.to_owned())
}

fn featuregate() -> String {
    env_or("FEATUREGATE_URL", "http://localhost:8080")
}

fn policies_dir() -> PathBuf {
    std::env::var("FEATUREGATE_POLICIES_DIR").map_or_else(|_| Path::new(env!("CARGO_MANIFEST_DIR")).join("policies/flags"), PathBuf::from)
}

fn client() -> Client {
    Client::builder().timeout(Duration::from_secs(10)).build().unwrap()
}

/// Alloy scrapes and batches asynchronously, and the profiler uploads every 10s.
const BACKEND: (Duration, Duration) = (Duration::from_secs(90), Duration::from_secs(1));

/// Retries `check` until it passes or `timeout` runs out, then fails with its last error.
async fn eventually<F, Fut>(timeout: Duration, interval: Duration, mut check: F)
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Result<(), String>>,
{
    let deadline = Instant::now() + timeout;
    loop {
        match check().await {
            Ok(()) => return,
            Err(e) if Instant::now() >= deadline => panic!("still failing after {timeout:?}: {e}"),
            Err(_) => tokio::time::sleep(interval).await,
        }
    }
}

async fn wait_ready() {
    let c = client();
    eventually(Duration::from_secs(120), Duration::from_secs(1), || async {
        let r =
            c.get(format!("{}/readyz", featuregate())).send().await.map_err(|e| format!("{e}; is the compose stack up? (mise run up)"))?;
        if r.status() == 200 { Ok(()) } else { Err(format!("/readyz answered {}", r.status())) }
    })
    .await;
}

async fn evaluate(flag: &str, context: Value) -> Value {
    let r = client()
        .post(format!("{}/ofrep/v1/evaluate/flags/{flag}", featuregate()))
        .body(json!({ "context": context }).to_string())
        .send()
        .await
        .unwrap();
    assert_eq!(r.status(), 200);
    r.json().await.unwrap()
}

async fn get_json(base: &str, path_and_query: &str) -> Result<Value, String> {
    let r = client().get(format!("{base}{path_and_query}")).send().await.map_err(|e| e.to_string())?;
    let status = r.status();
    let text = r.text().await.map_err(|e| e.to_string())?;
    if !status.is_success() {
        return Err(format!("{path_and_query} answered {status}: {text}"));
    }
    serde_json::from_str(&text).map_err(|e| format!("{path_and_query}: {e}: {text}"))
}

fn encode(s: &str) -> String {
    s.bytes()
        .map(|b| if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) { (b as char).to_string() } else { format!("%{b:02X}") })
        .collect()
}

/// Generates enough traffic for every backend to have something to show.
async fn send_traffic() {
    let c = client();
    for case in Case::all() {
        let _ = c.post(format!("{}{}", featuregate(), case.path())).body(case.body()).send().await;
    }
}

// ---- the wire contract ----

#[tokio::test]
async fn every_fixture_case_answers_as_expected() {
    wait_ready().await;
    let c = client();
    let mut failures = Vec::new();
    for case in Case::all() {
        let r = c.post(format!("{}{}", featuregate(), case.path())).body(case.body()).send().await.unwrap();
        let status = r.status().as_u16();
        let body: Value = r.json().await.unwrap_or(Value::Null);
        let diff = case.mismatches(status, &body);
        if !diff.is_empty() {
            failures.push(format!("{}: {}", case.name, diff.join("; ")));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

#[tokio::test]
async fn bulk_honours_if_none_match() {
    wait_ready().await;
    let c = client();
    let url = format!("{}/ofrep/v1/evaluate/flags", featuregate());
    let body = json!({"context": {"targetingKey": "e2e-etag", "plan": "pro", "region": "eu-1"}}).to_string();
    let first = c.post(&url).body(body.clone()).send().await.unwrap();
    assert_eq!(first.status(), 200);
    let etag = first.headers()["etag"].to_str().unwrap().to_owned();
    let second = c.post(&url).header("if-none-match", &etag).body(body).send().await.unwrap();
    assert_eq!(second.status(), 304);
}

#[tokio::test]
async fn the_operator_endpoints_describe_what_is_served() {
    wait_ready().await;
    let flags = get_json(&featuregate(), "/api/v1/flags").await.unwrap();
    let keys: Vec<&str> = flags["flags"].as_array().unwrap().iter().map(|f| f["key"].as_str().unwrap()).collect();
    for want in ["beta-api", "dark-mode", "new-checkout", "search-v2"] {
        assert!(keys.contains(&want), "{want} not in {keys:?}");
    }
    let policies = get_json(&featuregate(), "/api/v1/policies").await.unwrap();
    assert_eq!(policies["source"], "directory");
    assert_eq!(get_json(&featuregate(), "/healthz").await.unwrap()["status"], "ok");
}

#[tokio::test]
async fn metrics_count_evaluations() {
    wait_ready().await;
    let scrape = || async { client().get(format!("{}/metrics", featuregate())).send().await.unwrap().text().await.unwrap() };
    let series = "featuregate_evaluations_total{decision=\"enable\",flag=\"dark-mode\",reason=\"rollout\"}";
    let value = |text: &str| text.lines().find_map(|l| l.strip_prefix(series).and_then(|r| r.trim().parse::<f64>().ok())).unwrap_or(0.0);
    let before = value(&scrape().await);
    evaluate("dark-mode", json!({"targetingKey": "e2e-metrics", "region": "eu-1"})).await;
    // Other specs evaluate dark-mode concurrently, so the series can grow by
    // more than this spec's one evaluation.
    let after = value(&scrape().await);
    assert!(after >= before + 1.0, "{series} went from {before} to {after}");
}

// ---- hot reload, against the mounted directory ----

/// Restores a policy file to what it held, even when the spec fails.
struct Restore {
    path: PathBuf,
    original: String,
    _lock: MutexGuard<'static, ()>,
}

impl Restore {
    async fn new(file: &str) -> Self {
        let lock = POLICIES.lock().await;
        let path = policies_dir().join(file);
        let original = std::fs::read_to_string(&path)
            .unwrap_or_else(|e| panic!("can't read {}: {e}; set FEATUREGATE_POLICIES_DIR to the mounted directory", path.display()));
        Self { path, original, _lock: lock }
    }

    /// Replaces the file the way an editor's save or `mv` does: write beside
    /// it, then rename, so the service never reads half a file.
    fn write(&self, text: &str) {
        let tmp = self.path.with_extension("tmp");
        std::fs::write(&tmp, text).unwrap();
        std::fs::rename(&tmp, &self.path).unwrap();
    }
}

impl Drop for Restore {
    fn drop(&mut self) {
        self.write(&self.original);
    }
}

const DARK_MODE_OFF: &str =
    "policy flags.dark_mode: FeatureRollout@1\n\nuse platform.guardrails\n\nguardrails()\n\nwhen false {\n  enable(reason: rollout)\n}\n";

async fn dark_mode_value() -> Value {
    evaluate("dark-mode", json!({"targetingKey": "e2e-reload", "region": "eu-1"})).await["value"].clone()
}

async fn wait_for_dark_mode(want: bool) {
    eventually(Duration::from_secs(60), Duration::from_millis(500), || async {
        let got = dark_mode_value().await;
        if got == json!(want) { Ok(()) } else { Err(format!("dark-mode is {got}, want {want}")) }
    })
    .await;
}

#[tokio::test]
async fn an_edited_policy_is_picked_up_and_a_broken_one_is_not() {
    wait_ready().await;
    let file = Restore::new("dark_mode.sigil").await;
    assert_eq!(dark_mode_value().await, json!(true));

    file.write(DARK_MODE_OFF);
    wait_for_dark_mode(false).await;

    // A broken edit keeps the last known good bundle and says why.
    file.write("policy flags.dark_mode: FeatureRollout@1\nwhen <");
    eventually(Duration::from_secs(60), Duration::from_millis(500), || async {
        let p = get_json(&featuregate(), "/api/v1/policies").await?;
        if p["lastReloadError"].is_string() { Ok(()) } else { Err("no reload error reported yet".into()) }
    })
    .await;
    assert_eq!(dark_mode_value().await, json!(false), "the last known good bundle still serves");

    // Restoring the original is a good reload again.
    file.write(&file.original);
    wait_for_dark_mode(true).await;
    let p = get_json(&featuregate(), "/api/v1/policies").await.unwrap();
    assert_eq!(p["lastReloadError"], json!(null));
}

#[tokio::test]
async fn a_policy_that_drops_the_guardrail_is_refused() {
    wait_ready().await;
    let file = Restore::new("dark_mode.sigil").await;
    file.write("policy flags.dark_mode: FeatureRollout@1\n\nwhen true {\n  enable(reason: enterprise)\n}\n");
    eventually(Duration::from_secs(60), Duration::from_millis(500), || async {
        let p = get_json(&featuregate(), "/api/v1/policies").await?;
        match p["lastReloadError"].as_str() {
            Some(e) if e.contains("platform.guardrails") => Ok(()),
            other => Err(format!("lastReloadError is {other:?}")),
        }
    })
    .await;
    // The flag still obeys the guardrail: an unready region stays off.
    let off = evaluate("dark-mode", json!({"targetingKey": "e2e-guardrail", "region": "ap-1"})).await;
    assert_eq!(off["metadata"]["sigil.reason"], "region_not_ready");
}

// ---- the observability backends ----

fn mimir() -> String {
    env_or("MIMIR_URL", "http://localhost:9009")
}

async fn prom(query: &str) -> Result<Value, String> {
    get_json(&mimir(), &format!("/prometheus/api/v1/query?query={}", encode(query))).await
}

#[tokio::test]
async fn mimir_has_featuregates_metrics() {
    wait_ready().await;
    send_traffic().await;
    for (what, query) in [
        ("the scrape target", "up{job=\"featuregate\"} == 1"),
        ("the evaluations", "sum by (flag, decision, reason) (featuregate_evaluations_total)"),
        ("the loaded bundle", "featuregate_loaded_info"),
        ("the request latencies", "featuregate_http_request_duration_seconds_count"),
    ] {
        eventually(BACKEND.0, BACKEND.1, || async {
            let res = prom(query).await?;
            if res["data"]["result"].as_array().is_some_and(|r| !r.is_empty()) {
                Ok(())
            } else {
                Err(format!("{what}: {query} has no series yet"))
            }
        })
        .await;
    }
}

#[tokio::test]
async fn loki_has_featuregates_logs() {
    wait_ready().await;
    send_traffic().await;
    let loki = env_or("LOKI_URL", "http://localhost:3100");
    eventually(BACKEND.0, BACKEND.1, || async {
        let q = encode("{service_name=\"featuregate\", flag=\"dark-mode\"} | json | msg=\"flag evaluated\" | trace_id!=\"\"");
        let res = get_json(&loki, &format!("/loki/api/v1/query_range?query={q}&limit=5")).await?;
        if res["data"]["result"].as_array().is_some_and(|r| !r.is_empty()) { Ok(()) } else { Err("no evaluation log line yet".into()) }
    })
    .await;
}

#[tokio::test]
async fn tempo_has_request_and_evaluation_spans() {
    wait_ready().await;
    send_traffic().await;
    let tempo = env_or("TEMPO_URL", "http://localhost:3200");
    eventually(BACKEND.0, BACKEND.1, || async {
        let q = encode("{ resource.service.name = \"featuregate\" && name = \"evaluate flag\" }");
        let found = get_json(&tempo, &format!("/api/search?q={q}&limit=5")).await?;
        let id = found["traces"][0]["traceID"].as_str().ok_or("no trace found yet")?.to_owned();
        let trace = get_json(&tempo, &format!("/api/traces/{id}")).await?;
        let text = trace.to_string();
        for needed in ["http request", "evaluate flag", "sigil.decision", "ofrep.reason", "http.route"] {
            if !text.contains(needed) {
                return Err(format!("trace {id} has no {needed}"));
            }
        }
        Ok(())
    })
    .await;
}

#[tokio::test]
async fn pyroscope_has_featuregates_cpu_profiles() {
    wait_ready().await;
    send_traffic().await;
    let pyroscope = env_or("PYROSCOPE_URL", "http://localhost:4040");
    eventually(BACKEND.0, BACKEND.1, || async {
        let now = now_ms();
        let r = client()
            .post(format!("{pyroscope}/querier.v1.QuerierService/LabelValues"))
            .json(&json!({"name": "__profile_type__", "matchers": ["{service_name=\"featuregate\"}"], "start": now - 600_000, "end": now}))
            .send()
            .await
            .map_err(|e| e.to_string())?;
        let body: Value = r.json().await.map_err(|e| e.to_string())?;
        let names: Vec<&str> = body["names"].as_array().map(|a| a.iter().filter_map(Value::as_str).collect()).unwrap_or_default();
        if names.iter().any(|n| n.contains("cpu")) { Ok(()) } else { Err(format!("profile types: {names:?}")) }
    })
    .await;
}

fn now_ms() -> u64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis() as u64
}

#[tokio::test]
async fn grafana_has_the_datasources_and_the_dashboard() {
    wait_ready().await;
    let grafana = env_or("GRAFANA_URL", "http://localhost:3000");
    for uid in ["mimir", "tempo", "loki", "pyroscope"] {
        eventually(BACKEND.0, BACKEND.1, || async {
            let res = get_json(&grafana, &format!("/api/datasources/uid/{uid}/health")).await?;
            if res["status"] == "OK" { Ok(()) } else { Err(format!("{uid}: {res}")) }
        })
        .await;
    }
    eventually(BACKEND.0, BACKEND.1, || async {
        let res = get_json(&grafana, "/api/dashboards/uid/featuregate").await?;
        let panels = res["dashboard"]["panels"].as_array().map_or(0, Vec::len);
        if res["dashboard"]["uid"] == "featuregate" && panels > 0 { Ok(()) } else { Err("dashboard not provisioned yet".into()) }
    })
    .await;
}
