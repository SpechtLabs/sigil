//! The HTTP API: OFREP evaluation, the operator endpoints, probes and metrics.
//! [`router`] builds it from an [`AppState`]; tests call it directly with
//! `tower::ServiceExt`, so they run the code production runs.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Instant;

use axum::Router;
use axum::body::{Body, to_bytes};
use axum::extract::{MatchedPath, Path, Request, State};
use axum::http::{HeaderMap, HeaderValue, StatusCode, header};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use opentelemetry::propagation::{Extractor, TextMapPropagator};
use opentelemetry_sdk::propagation::TraceContextPropagator;
use serde::Serialize;
use serde_json::json;
use sha2::{Digest, Sha256};
use tracing::Instrument;
use tracing::field::Empty;
use tracing_opentelemetry::OpenTelemetrySpanExt;

use crate::engine::Engine;
use crate::metrics::Metrics;
use crate::ofrep::{self, BulkResponse, ContextError, ErrorBody, ErrorCode, EvaluationRequest};
use crate::reload::{Reloader, Trigger};
use crate::store::ReloadOutcome;

/// The most a request body may hold: a context is a handful of attributes.
pub const MAX_BODY_BYTES: usize = 64 * 1024;

/// Everything a handler needs, built once by the composition root.
#[derive(Clone)]
pub struct AppState {
    pub engine: Arc<Engine>,
    pub metrics: Metrics,
    pub reloader: Reloader,
    /// Set when shutdown begins, so `/readyz` turns 503 while requests drain.
    pub draining: Arc<AtomicBool>,
    pub version: String,
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/ofrep/v1/evaluate/flags/{key}", post(evaluate_one))
        .route("/ofrep/v1/evaluate/flags", post(evaluate_bulk))
        .route("/api/v1/flags", get(list_flags))
        .route("/api/v1/policies", get(policies))
        .route("/api/v1/policies/reload", post(reload))
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics))
        .route_layer(middleware::from_fn_with_state(state.clone(), instrument))
        .fallback(not_found)
        .with_state(state)
}

// ---- OFREP ----

async fn evaluate_one(State(app): State<AppState>, Path(key): Path<String>, request: Request) -> Response {
    let context = match read_request(request).await {
        Ok(c) => c,
        Err(resp) => return *resp,
    };
    if !app.engine.store().current().flags.contains_key(&key) {
        return ofrep_error(
            StatusCode::NOT_FOUND,
            Some(&key),
            ErrorCode::FlagNotFound,
            format!("no policy serves the flag {key:?}; GET /api/v1/flags lists them"),
        );
    }
    let user = match ofrep::user_from_context(&context.context) {
        Ok(u) => u,
        Err(e) => return context_error(Some(&key), e),
    };
    match app.engine.evaluate(&key, &user).await {
        Some(evaluation) => respond(StatusCode::OK, &evaluation),
        // The bundle was swapped between the check and the evaluation, and
        // the new one no longer serves the flag.
        None => ofrep_error(
            StatusCode::NOT_FOUND,
            Some(&key),
            ErrorCode::FlagNotFound,
            format!("no policy serves the flag {key:?}; GET /api/v1/flags lists them"),
        ),
    }
}

async fn evaluate_bulk(State(app): State<AppState>, headers: HeaderMap, request: Request) -> Response {
    let context = match read_request(request).await {
        Ok(c) => c,
        Err(resp) => return *resp,
    };
    let user = match ofrep::user_from_context(&context.context) {
        Ok(u) => u,
        Err(e) => return context_error(None, e),
    };
    let flags = app.engine.evaluate_all(&user).await;
    let body = serde_json::to_vec(&BulkResponse { flags }).expect("an evaluation serializes");
    let etag = format!("\"{}\"", hex(&Sha256::digest(&body)[..16]));
    let mut response = if matches_etag(&headers, &etag) {
        StatusCode::NOT_MODIFIED.into_response()
    } else {
        (StatusCode::OK, [(header::CONTENT_TYPE, "application/json")], body).into_response()
    };
    response.headers_mut().insert(header::ETAG, HeaderValue::from_str(&etag).expect("an ETag is ASCII"));
    response
}

/// Whether `If-None-Match` names `etag`: a list of entity tags, weak or strong, or `*`.
fn matches_etag(headers: &HeaderMap, etag: &str) -> bool {
    headers
        .get_all(header::IF_NONE_MATCH)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .map(|t| t.trim().trim_start_matches("W/"))
        .any(|t| t == "*" || t == etag)
}

async fn read_request(request: Request) -> Result<EvaluationRequest, Box<Response>> {
    let bytes = to_bytes(request.into_body(), MAX_BODY_BYTES).await.map_err(|_| {
        Box::new(ofrep_error(
            StatusCode::PAYLOAD_TOO_LARGE,
            None,
            ErrorCode::ParseError,
            format!("the request body is larger than {MAX_BODY_BYTES} bytes; send only the evaluation context"),
        ))
    })?;
    serde_json::from_slice(&bytes).map_err(|e| {
        Box::new(ofrep_error(
            StatusCode::BAD_REQUEST,
            None,
            ErrorCode::ParseError,
            format!("the body isn't JSON of the form {{\"context\": {{\"targetingKey\": \"...\"}}}}: {e}"),
        ))
    })
}

fn context_error(key: Option<&str>, e: ContextError) -> Response {
    ofrep_error(StatusCode::BAD_REQUEST, key, e.code, e.details)
}

fn ofrep_error(status: StatusCode, key: Option<&str>, code: ErrorCode, details: String) -> Response {
    respond(status, &ErrorBody { key: key.map(str::to_owned), error_code: code, error_details: details })
}

// ---- operator endpoints ----

#[derive(Serialize)]
struct FlagEntry<'a> {
    key: &'a str,
    policy: &'a str,
}

async fn list_flags(State(app): State<AppState>) -> Response {
    let bundle = app.engine.store().current();
    let flags: Vec<_> = bundle.flags.values().map(|f| FlagEntry { key: &f.key, policy: &f.policy }).collect();
    respond(StatusCode::OK, &json!({ "flags": flags }))
}

async fn policies(State(app): State<AppState>) -> Response {
    let store = app.engine.store();
    let bundle = store.current();
    let loaded_at = bundle.loaded_at.duration_since(std::time::UNIX_EPOCH).map_or(0, |d| d.as_secs());
    respond(
        StatusCode::OK,
        &json!({
            "source": bundle.source.label(),
            "digest": bundle.digest,
            "loadedAt": loaded_at,
            "flags": bundle.flags.values().map(|f| FlagEntry { key: &f.key, policy: &f.policy }).collect::<Vec<_>>(),
            "lastReloadError": store.last_error(),
        }),
    )
}

async fn reload(State(app): State<AppState>) -> Response {
    match app.reloader.reload(Trigger::Endpoint, true).await {
        ReloadOutcome::Loaded { digest, flags } => {
            respond(StatusCode::OK, &json!({ "result": "loaded", "digest": digest, "flags": flags }))
        }
        ReloadOutcome::Unchanged { digest } => respond(StatusCode::OK, &json!({ "result": "unchanged", "digest": digest })),
        ReloadOutcome::Failed { error } => respond(
            StatusCode::UNPROCESSABLE_ENTITY,
            &json!({ "result": "failed", "error": error, "serving": app.engine.store().current().digest }),
        ),
    }
}

// ---- probes and metrics ----

async fn healthz() -> Response {
    respond(StatusCode::OK, &json!({ "status": "ok" }))
}

async fn readyz(State(app): State<AppState>) -> Response {
    if app.draining.load(Ordering::Acquire) {
        return respond(StatusCode::SERVICE_UNAVAILABLE, &json!({ "status": "shutting down" }));
    }
    let bundle = app.engine.store().current();
    let stats = bundle.pool.stats();
    respond(StatusCode::OK, &json!({ "status": "ready", "flags": bundle.flags.len(), "instances": stats.size }))
}

async fn metrics(State(app): State<AppState>) -> Response {
    ([(header::CONTENT_TYPE, "text/plain; version=0.0.4; charset=utf-8")], app.metrics.render()).into_response()
}

async fn not_found(State(app): State<AppState>, request: Request) -> Response {
    // Unmatched paths are one route label, "-", so a scanner can't grow the label set.
    let started = Instant::now();
    let method = method_label(request.method());
    let response = respond(StatusCode::NOT_FOUND, &json!({ "error": "no such route; see the README for the API" }));
    access_log(&method, "-", response.status(), started);
    record(&app, &method, "-", response.status(), started);
    response
}

// ---- instrumentation ----

/// Wraps every route: a server span (continuing the caller's trace), the
/// access log line and the request metrics, all labelled by route template.
async fn instrument(State(app): State<AppState>, request: Request, next: Next) -> Response {
    let route = request.extensions().get::<MatchedPath>().map_or("-", MatchedPath::as_str).to_owned();
    observe_with(&app, request, &route, next).await
}

async fn observe_with(app: &AppState, request: Request, route: &str, next: Next) -> Response {
    let method = method_label(request.method());
    let span = tracing::info_span!(
        "http request",
        "otel.kind" = "server",
        "http.request.method" = %method,
        "http.route" = %route,
        "http.response.status_code" = Empty,
    );
    let parent = TraceContextPropagator::new().extract(&HeaderCarrier(request.headers()));
    // An invalid traceparent leaves a fresh trace; only a valid one is adopted.
    if opentelemetry::trace::TraceContextExt::span(&parent).span_context().is_valid()
        && let Err(e) = span.set_parent(parent)
    {
        tracing::debug!(error = %e, "couldn't continue the caller's trace");
    }
    let started = Instant::now();
    app.metrics.http_in_flight.inc();
    let response = next.run(request).instrument(span.clone()).await;
    app.metrics.http_in_flight.dec();
    let status = response.status();
    span.record("http.response.status_code", status.as_u16());
    span.in_scope(|| access_log(&method, route, status, started));
    record(app, &method, route, status, started);
    response
}

fn access_log(method: &str, route: &str, status: StatusCode, started: Instant) {
    let duration_ms = started.elapsed().as_secs_f64() * 1000.0;
    // Probes and scrapes run every few seconds; they log at debug.
    if matches!(route, "/healthz" | "/readyz" | "/metrics") {
        tracing::debug!(method, route, status = status.as_u16(), duration_ms, "request");
    } else {
        tracing::info!(method, route, status = status.as_u16(), duration_ms, "request");
    }
}

fn record(app: &AppState, method: &str, route: &str, status: StatusCode, started: Instant) {
    app.metrics.http_requests.with_label_values(&[method, route, status.as_str()]).inc();
    app.metrics.http_duration.with_label_values(&[method, route]).observe(started.elapsed().as_secs_f64());
}

struct HeaderCarrier<'a>(&'a HeaderMap);

impl Extractor for HeaderCarrier<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|v| v.to_str().ok())
    }
    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(|k| k.as_str()).collect()
    }
}

// ---- small helpers ----

/// A JSON response with a status.
fn respond<T: Serialize>(status: StatusCode, body: &T) -> Response {
    let bytes = serde_json::to_vec(body).expect("response bodies serialize");
    (status, [(header::CONTENT_TYPE, "application/json")], Body::from(bytes)).into_response()
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// The HTTP methods that get their own label value; anything else is `OTHER`,
/// so a client can't grow the `method` label by inventing methods.
const KNOWN_METHODS: [&str; 7] = ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"];

fn method_label(method: &axum::http::Method) -> String {
    KNOWN_METHODS.iter().find(|m| **m == method.as_str()).copied().unwrap_or("OTHER").to_owned()
}

#[cfg(test)]
mod tests {
    use axum::http::Method;
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case("GET", "GET")]
    #[case("POST", "POST")]
    #[case("OPTIONS", "OPTIONS")]
    #[case("PROPFIND", "OTHER")]
    #[case("TRACE", "OTHER")]
    #[case("CONNECT", "OTHER")]
    #[case("MYMETHOD", "OTHER")]
    fn methods_map_to_a_fixed_set(#[case] method: &str, #[case] label: &str) {
        assert_eq!(method_label(&Method::from_bytes(method.as_bytes()).unwrap()), label);
    }
}
