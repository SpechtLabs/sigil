//! The crate blocks, and a host may call it from async code anyway: by mistake,
//! or from a task it hasn't moved to a blocking thread yet. wasmtime-wasi's
//! synchronous WASI panics on a Tokio worker thread; the crate moves such a
//! call off it, so these calls work, blocking the caller as documented.

mod common;

use std::sync::Arc;

use common::{deploy_gates, json, sigil_files, split};
use sigil::{CompileOptions, EvalOptions, Pool, Sigil};

fn options() -> CompileOptions {
    CompileOptions { policy: Some("payments.production".into()), functions: [("split".to_string(), split())].into(), ..Default::default() }
}

async fn use_a_sigil_on_this_thread() {
    let sigil = Sigil::new(common::module()).unwrap();
    let policy = sigil.compile(&sigil_files(&deploy_gates()), options()).unwrap();
    let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
    assert!(policy.eval(&input).unwrap().error.is_none());
    // A host function re-entering its own instance is still told so, not deadlocked.
    assert!(sigil.version().is_ok());
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn works_on_a_multi_thread_runtime_worker() {
    use_a_sigil_on_this_thread().await;
}

#[tokio::test(flavor = "current_thread")]
async fn works_on_a_current_thread_runtime() {
    use_a_sigil_on_this_thread().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_pool_evaluates_from_blocking_threads() {
    let pool = Arc::new(Pool::new(common::module(), 2).unwrap());
    pool.compile("payments", &sigil_files(&deploy_gates()), options()).unwrap();
    let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
    let tasks: Vec<_> = (0..16)
        .map(|_| {
            let (pool, input) = (Arc::clone(&pool), input.clone());
            tokio::task::spawn_blocking(move || pool.evaluate("payments", &input, &EvalOptions::default()))
        })
        .collect();
    for task in tasks {
        assert!(task.await.unwrap().unwrap().error.is_none());
    }
}
