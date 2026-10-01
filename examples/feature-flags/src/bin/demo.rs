//! A demo client: sends every request of `requests/cases.json` to a running
//! featuregate, prints each answer and checks it against what the fixture
//! expects, then shows the bulk endpoint's ETag saving a second round trip.
//!
//! `cargo run --bin demo [-- http://host:port]`, or `mise run demo`.

use std::process::ExitCode;

use featuregate::cases::Case;
use reqwest::header::{ETAG, IF_NONE_MATCH};
use serde_json::Value;

#[tokio::main]
async fn main() -> ExitCode {
    let base =
        std::env::args().nth(1).or_else(|| std::env::var("FEATUREGATE_URL").ok()).unwrap_or_else(|| "http://127.0.0.1:8080".to_owned());
    let client = reqwest::Client::builder().timeout(std::time::Duration::from_secs(5)).build().expect("a client builds");
    println!("featuregate at {base}\n");

    let mut failed = 0;
    for case in Case::all() {
        let response =
            client.post(format!("{base}{}", case.path())).header("content-type", "application/json").body(case.body()).send().await;
        let response = match response {
            Ok(r) => r,
            Err(e) => {
                eprintln!("can't reach {base}: {e}\n  help: start the service with `mise run dev` or `mise run up`, or pass its URL");
                return ExitCode::FAILURE;
            }
        };
        let status = response.status().as_u16();
        let body: Value = response.json().await.unwrap_or(Value::Null);
        let diff = case.mismatches(status, &body);
        println!("{} {:<32} {}", if diff.is_empty() { "ok  " } else { "FAIL" }, case.name, summary(&case, status, &body));
        println!("     {}", case.description);
        for d in &diff {
            println!("     ! {d}");
        }
        failed += usize::from(!diff.is_empty());
    }

    // The bulk answer's ETag: a client that holds one asks again with
    // If-None-Match and gets an empty 304 while nothing changed.
    let bulk = Case::all().into_iter().find(|c| c.flag.is_none() && c.expect.status == 200).expect("a bulk case exists");
    let first = client.post(format!("{base}{}", bulk.path())).body(bulk.body()).send().await;
    if let Ok(first) = first
        && let Some(etag) = first.headers().get(ETAG).cloned()
    {
        let second = client.post(format!("{base}{}", bulk.path())).header(IF_NONE_MATCH, etag.clone()).body(bulk.body()).send().await;
        let status = second.map_or(0, |r| r.status().as_u16());
        let ok = status == 304;
        println!(
            "\n{} bulk with If-None-Match {} answers {status} (want 304)",
            if ok { "ok  " } else { "FAIL" },
            etag.to_str().unwrap_or("?")
        );
        failed += usize::from(!ok);
    }

    if failed > 0 {
        eprintln!("\n{failed} request(s) answered something else than expected");
        return ExitCode::FAILURE;
    }
    println!("\nevery answer is what the fixtures expect");
    ExitCode::SUCCESS
}

/// One line for an answer: the value and why, or the error.
fn summary(case: &Case, status: u16, body: &Value) -> String {
    if let Some(code) = body.get("errorCode").and_then(Value::as_str) {
        return format!("{status} {code}");
    }
    if case.flag.is_none() {
        let flags = body.get("flags").and_then(Value::as_array).cloned().unwrap_or_default();
        let parts: Vec<String> = flags.iter().map(|f| format!("{}={}", f["key"].as_str().unwrap_or("?"), f["value"])).collect();
        return format!("{status} {}", parts.join(" "));
    }
    let sigil = body.pointer("/metadata/sigil.reason").and_then(Value::as_str).unwrap_or("-");
    format!("{status} value={} {} ({sigil})", body["value"], body["reason"].as_str().unwrap_or("?"))
}
