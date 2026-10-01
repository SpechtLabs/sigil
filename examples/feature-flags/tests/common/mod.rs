//! Shared setup for the integration suites: one compiled Sigil module per
//! test binary, and an in-process service behind its real router.
#![allow(dead_code)]

pub mod observe;

use std::path::Path;
use std::sync::OnceLock;

use axum::Router;
use axum::body::Body;
use axum::http::{HeaderMap, Request, StatusCode};
use featuregate::api::{self, AppState};
use featuregate::config::Config;
use featuregate::metrics::Metrics;
use featuregate::service::Service;
use serde_json::Value;
use sigil::Module;
use tower::ServiceExt;

/// The Sigil module, compiled once per test binary: compiling takes seconds,
/// and instances made from it are cheap.
pub fn module() -> Module {
    static MODULE: OnceLock<Module> = OnceLock::new();
    MODULE.get_or_init(|| Module::bundled().expect("the bundled sigil.wasm loads; run `mise run wasm-build` first")).clone()
}

/// The module with fuel metering, for the suites that set an evaluation fuel.
pub fn fuel_module() -> Module {
    static MODULE: OnceLock<Module> = OnceLock::new();
    MODULE.get_or_init(|| Module::bundled_with(sigil::ModuleConfig { fuel: true }).expect("the bundled sigil.wasm loads")).clone()
}

/// A service and its router, built from environment-style settings.
pub struct Harness {
    pub router: Router,
    pub state: AppState,
}

/// Builds the service the way `serve` does, without listening. `env` is the
/// `FEATUREGATE_*` settings; the policies are the embedded samples unless it
/// names a directory, and polling is off.
pub async fn harness(env: &[(&str, &str)]) -> Harness {
    try_harness(env).await.expect("the service starts")
}

/// Like [`harness`], with the startup error for a bundle that doesn't load. Sigil
/// instances are built on a blocking thread: wasmtime-wasi starts its own
/// runtime and can't do that on an async one.
pub async fn try_harness(env: &[(&str, &str)]) -> Result<Harness, String> {
    let env: Vec<(String, String)> = env.iter().map(|(k, v)| ((*k).to_owned(), (*v).to_owned())).collect();
    tokio::task::spawn_blocking(move || build(&env)).await.unwrap()
}

fn build(env: &[(String, String)]) -> Result<Harness, String> {
    let lookup = |name: &str| {
        env.iter().find(|(k, _)| k == name).map(|(_, v)| v.clone()).or_else(|| match name {
            "FEATUREGATE_RELOAD_INTERVAL" => Some("0".to_owned()),
            "FEATUREGATE_WORKERS" => Some("2".to_owned()),
            "FEATUREGATE_EVALUATION_TIMEOUT" => Some("2s".to_owned()),
            _ => None,
        })
    };
    let config = Config::from_lookup(lookup).map_err(|e| e.to_string())?;
    let metrics = Metrics::new(&config.version);
    let module = if config.evaluation_fuel.is_some() { fuel_module() } else { module() };
    let service = Service::new(config, module, metrics)?;
    Ok(Harness { router: api::router(service.state.clone()), state: service.state })
}

/// A response, read whole.
pub struct Reply {
    pub status: StatusCode,
    pub headers: HeaderMap,
    pub text: String,
}

impl Reply {
    pub fn json(&self) -> Value {
        serde_json::from_str(&self.text).unwrap_or_else(|e| panic!("not JSON ({e}): {}", self.text))
    }
}

impl Harness {
    /// Sends one request through the router.
    pub async fn send(&self, method: &str, path: &str, body: Option<&str>, headers: &[(&str, &str)]) -> Reply {
        let mut request = Request::builder().method(method).uri(path);
        for (k, v) in headers {
            request = request.header(*k, *v);
        }
        let request = request.body(body.map_or_else(Body::empty, |b| Body::from(b.to_owned()))).unwrap();
        let response = self.router.clone().oneshot(request).await.unwrap();
        let (parts, body) = response.into_parts();
        let bytes = axum::body::to_bytes(body, usize::MAX).await.unwrap();
        Reply { status: parts.status, headers: parts.headers, text: String::from_utf8_lossy(&bytes).into_owned() }
    }

    pub async fn post(&self, path: &str, body: &str) -> Reply {
        self.send("POST", path, Some(body), &[]).await
    }

    pub async fn get(&self, path: &str) -> Reply {
        self.send("GET", path, None, &[]).await
    }

    /// Evaluates one flag for a context given as JSON.
    pub async fn evaluate(&self, flag: &str, context: Value) -> Value {
        let reply = self.post(&format!("/ofrep/v1/evaluate/flags/{flag}"), &serde_json::json!({ "context": context }).to_string()).await;
        assert_eq!(reply.status, StatusCode::OK, "{}", reply.text);
        reply.json()
    }

    /// The exposition text of `/metrics`.
    pub async fn metrics(&self) -> String {
        self.get("/metrics").await.text
    }
}

/// One line of the exposition text as a number, e.g. a counter's value.
pub fn metric(text: &str, series: &str) -> Option<f64> {
    text.lines().find_map(|l| l.strip_prefix(series).and_then(|rest| rest.strip_prefix(' ')).and_then(|v| v.parse().ok()))
}

/// Copies the sample flag policies into a fresh directory a test may edit.
pub fn sample_dir() -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    let flags = Path::new(env!("CARGO_MANIFEST_DIR")).join("policies/flags");
    for entry in std::fs::read_dir(flags).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_some_and(|e| e == "sigil") || path.file_name().is_some_and(|n| n == "flags.yaml") {
            std::fs::copy(&path, dir.path().join(path.file_name().unwrap())).unwrap();
        }
    }
    dir
}

/// Replaces a file the way a ConfigMap update or `mv` does: write beside it,
/// then rename over it, so a reader sees the old text or the new.
pub fn write_atomically(path: &Path, text: &str) {
    let tmp = path.with_extension("tmp");
    std::fs::write(&tmp, text).unwrap();
    std::fs::rename(tmp, path).unwrap();
}
