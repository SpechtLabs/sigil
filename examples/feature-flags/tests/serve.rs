//! The running service over a real socket: polling reloads, SIGHUP, and the
//! graceful drain.

mod common;

use std::time::Duration;

use featuregate::config::Config;
use featuregate::metrics::Metrics;
use featuregate::service::Service;
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tokio::sync::oneshot;

struct Running {
    base: String,
    stop: oneshot::Sender<()>,
    done: tokio::task::JoinHandle<std::io::Result<()>>,
    client: reqwest::Client,
}

async fn start(dir: &std::path::Path, interval: &str) -> Running {
    let env = [
        ("FEATUREGATE_POLICIES", dir.to_str().unwrap()),
        ("FEATUREGATE_RELOAD_INTERVAL", interval),
        ("FEATUREGATE_WORKERS", "2"),
        ("FEATUREGATE_SHUTDOWN_TIMEOUT", "5s"),
    ];
    let config = Config::from_lookup(|n| env.iter().find(|(k, _)| *k == n).map(|(_, v)| (*v).to_owned())).unwrap();
    let metrics = Metrics::new("test");
    let service = tokio::task::spawn_blocking(move || Service::new(config, common::module(), metrics)).await.unwrap().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let (stop, stopped) = oneshot::channel();
    let done = tokio::spawn(service.serve_until(listener, async move {
        stopped.await.ok();
    }));
    Running { base, stop, done, client: reqwest::Client::new() }
}

impl Running {
    async fn dark_mode(&self) -> Value {
        let r = self
            .client
            .post(format!("{}/ofrep/v1/evaluate/flags/dark-mode", self.base))
            .body(json!({"context": {"targetingKey": "u", "region": "eu-1"}}).to_string())
            .send()
            .await
            .unwrap();
        r.json().await.unwrap()
    }

    /// Waits for `dark-mode` to answer `want`, or fails after `within`.
    async fn dark_mode_becomes(&self, want: bool, within: Duration) {
        let deadline = tokio::time::Instant::now() + within;
        loop {
            if self.dark_mode().await["value"] == json!(want) {
                return;
            }
            assert!(tokio::time::Instant::now() < deadline, "dark-mode never became {want} within {within:?}");
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    }

    async fn shut_down(self) {
        self.stop.send(()).unwrap();
        tokio::time::timeout(Duration::from_secs(10), self.done).await.expect("the drain finishes").unwrap().unwrap();
    }
}

const OFF: &str =
    "policy flags.dark_mode: FeatureRollout@1\n\nuse platform.guardrails\n\nguardrails()\n\nwhen false {\n  enable(reason: rollout)\n}\n";

#[tokio::test]
async fn polling_and_sighup_reload_the_policies_and_shutdown_drains() {
    // Polling: an edit shows up by itself.
    let dir = common::sample_dir();
    let polling = start(dir.path(), "200ms").await;
    assert_eq!(polling.dark_mode().await["value"], true);
    common::write_atomically(&dir.path().join("dark_mode.sigil"), OFF);
    polling.dark_mode_becomes(false, Duration::from_secs(10)).await;

    // A broken edit is noticed and skipped; the good bundle keeps serving.
    common::write_atomically(&dir.path().join("dark_mode.sigil"), "policy flags.dark_mode: FeatureRollout@1\nwhen <");
    tokio::time::sleep(Duration::from_millis(800)).await;
    assert_eq!(polling.dark_mode().await["value"], false);
    let policies: Value = polling.client.get(format!("{}/api/v1/policies", polling.base)).send().await.unwrap().json().await.unwrap();
    assert!(policies["lastReloadError"].is_string(), "{policies}");
    polling.shut_down().await;

    // SIGHUP: with polling off, only the signal reloads.
    let dir = common::sample_dir();
    let hup = start(dir.path(), "0").await;
    assert_eq!(hup.dark_mode().await["value"], true);
    common::write_atomically(&dir.path().join("dark_mode.sigil"), OFF);
    tokio::time::sleep(Duration::from_millis(500)).await;
    assert_eq!(hup.dark_mode().await["value"], true, "nothing reloads without a poll or a signal");
    let status = std::process::Command::new("kill").args(["-HUP", &std::process::id().to_string()]).status().unwrap();
    assert!(status.success());
    hup.dark_mode_becomes(false, Duration::from_secs(10)).await;
    hup.shut_down().await;
}

#[tokio::test]
async fn shutdown_lets_requests_finish_then_closes_the_listener() {
    let dir = common::sample_dir();
    let running = start(dir.path(), "0").await;
    let ready = running.client.get(format!("{}/readyz", running.base)).send().await.unwrap();
    assert_eq!(ready.status(), 200);
    // A request that is in flight when the drain begins still finishes: the
    // stop signal and the request race, and the answer is either a full
    // response or a refused connection, never a torn one.
    let base = running.base.clone();
    let client = running.client.clone();
    let inflight = tokio::spawn(async move { client.get(format!("{base}/healthz")).send().await.map(|r| r.status().as_u16()) });
    let client = running.client.clone();
    let base = running.base.clone();
    running.shut_down().await;
    if let Ok(status) = inflight.await.unwrap() {
        assert_eq!(status, 200);
    }
    assert!(client.get(format!("{base}/healthz")).send().await.is_err(), "the listener is closed after the drain");
}

fn run(args: &[&str], env: &[(&str, &str)]) -> (i32, String) {
    let out = std::process::Command::new(env!("CARGO_BIN_EXE_featuregate")).args(args).envs(env.iter().copied()).output().unwrap();
    (out.status.code().unwrap(), String::from_utf8_lossy(&[out.stdout, out.stderr].concat()).into_owned())
}

#[tokio::test]
async fn the_healthcheck_subcommand_follows_readiness() {
    let dir = common::sample_dir();
    let running = start(dir.path(), "0").await;
    let port = running.base.rsplit(':').next().unwrap().to_owned();
    let addr = format!("127.0.0.1:{port}");
    let probe = |addr: String| tokio::task::spawn_blocking(move || run(&["healthcheck"], &[("FEATUREGATE_ADDR", &addr)]));

    assert_eq!(probe(addr.clone()).await.unwrap().0, 0, "a ready service is healthy");
    running.shut_down().await;
    let (code, out) = probe(addr).await.unwrap();
    assert_eq!(code, 1, "{out}");
    assert!(out.contains("can't reach"), "{out}");
}

#[tokio::test]
async fn the_subcommands_report_problems_with_exit_codes() {
    let (code, out) = tokio::task::spawn_blocking(|| run(&["healthcheck"], &[("FEATUREGATE_ADDR", "nonsense")])).await.unwrap();
    assert_eq!(code, 2, "{out}");
    assert!(out.contains("FEATUREGATE_ADDR"), "{out}");

    let (code, out) =
        tokio::task::spawn_blocking(|| run(&["serve"], &[("FEATUREGATE_WORKERS", "0"), ("FEATUREGATE_EVALUATION_TIMEOUT", "soon")]))
            .await
            .unwrap();
    assert_eq!(code, 2);
    assert!(out.contains("FEATUREGATE_WORKERS") && out.contains("FEATUREGATE_EVALUATION_TIMEOUT"), "every problem at once: {out}");

    let kind_file = concat!(env!("CARGO_MANIFEST_DIR"), "/policies/feature_rollout.sigil");
    let (code, out) = tokio::task::spawn_blocking(move || run(&["export-kind", "--check", "--out", kind_file], &[])).await.unwrap();
    assert_eq!(code, 0, "{out}");

    let stale = tempfile::NamedTempFile::new().unwrap();
    std::fs::write(stale.path(), "kind Old version 1\n").unwrap();
    let path = stale.path().to_str().unwrap().to_owned();
    let (code, out) = tokio::task::spawn_blocking(move || run(&["export-kind", "--check", "--out", &path], &[])).await.unwrap();
    assert_eq!(code, 1);
    assert!(out.contains("stale"), "{out}");

    let (code, out) = tokio::task::spawn_blocking(|| run(&["version"], &[])).await.unwrap();
    assert_eq!(code, 0, "{out}");
    assert!(out.contains("featuregate ") && out.contains("sigil "), "{out}");
}

#[tokio::test]
async fn export_kind_writes_the_file() {
    let dir = tempfile::tempdir().unwrap();
    let out = dir.path().join("feature_rollout.sigil").to_str().unwrap().to_owned();
    let path = out.clone();
    let (code, text) = tokio::task::spawn_blocking(move || run(&["export-kind", "--out", &path], &[])).await.unwrap();
    assert_eq!(code, 0, "{text}");
    assert_eq!(std::fs::read_to_string(out).unwrap(), featuregate::kind::schema());
}

#[tokio::test]
async fn export_kind_prints_the_file_without_out() {
    let (code, text) = tokio::task::spawn_blocking(|| run(&["export-kind"], &[])).await.unwrap();
    assert_eq!(code, 0);
    assert_eq!(text, featuregate::kind::schema(), "nothing but the kind file on stdout, so it can be redirected");
}
