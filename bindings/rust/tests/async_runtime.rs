//! Async hosts. The crate blocks; without the `tokio` feature a host calls it
//! on a blocking thread, and calling it straight from a task works too (it
//! blocks that worker, nothing more, now that WASI no longer needs a runtime of
//! its own). With the feature, `evaluate_async` and friends do the blocking
//! hop themselves.

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

#[cfg(feature = "tokio")]
mod with_the_feature {
    use super::*;
    use sigil::Error;

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_pool_evaluates_async_without_the_caller_blocking_a_worker() {
        let pool = Pool::new(common::module(), 2).unwrap();
        pool.compile_async("payments", sigil_files(&deploy_gates()), options()).await.unwrap();
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        let tasks: Vec<_> = (0..16)
            .map(|_| {
                let (pool, input) = (pool.clone(), input.clone());
                tokio::spawn(async move { pool.evaluate_async("payments", input, EvalOptions::default()).await })
            })
            .collect();
        for task in tasks {
            assert!(task.await.unwrap().unwrap().error.is_none());
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn a_pool_evaluates_async_on_a_current_thread_runtime_and_reports_errors() {
        let pool = Pool::new(common::module(), 1).unwrap();
        let err = pool.evaluate_async("nope", serde_json::json!({}), EvalOptions::default()).await.unwrap_err();
        assert!(matches!(err, Error::NoPolicy(n) if n == "nope"));
        pool.compile_async("payments", sigil_files(&deploy_gates()), options()).await.unwrap();
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        assert!(pool.evaluate_async("payments", input, EvalOptions::default()).await.unwrap().error.is_none());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_policy_evaluates_async_from_an_arc() {
        let sigil = Sigil::new(common::module()).unwrap();
        let policy = Arc::new(sigil.compile(&sigil_files(&deploy_gates()), options()).unwrap());
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        let (a, b) =
            tokio::join!(policy.eval_async(input.clone(), EvalOptions::default()), policy.eval_async(input, EvalOptions::default()));
        assert_eq!(a.unwrap(), b.unwrap());
    }
}

#[cfg(feature = "tokio")]
mod panics {
    use super::*;

    /// An input whose serialization panics, which happens on the blocking thread.
    struct Panicky;

    impl serde::Serialize for Panicky {
        fn serialize<S: serde::Serializer>(&self, _: S) -> Result<S::Ok, S::Error> {
            panic!("the input's serializer panics")
        }
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_panic_in_the_blocking_task_is_an_error_and_the_pool_recovers() {
        let pool = Pool::new(common::module(), 1).unwrap();
        pool.compile_async("payments", sigil_files(&deploy_gates()), options()).await.unwrap();
        let err = pool.evaluate_async("payments", Panicky, EvalOptions::default()).await.unwrap_err();
        assert!(err.to_string().contains("the evaluation panicked: the input's serializer panics"), "{err}");
        // The instance that was leased while unwinding is rebuilt, and the pool serves again.
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        assert!(pool.evaluate_async("payments", input, EvalOptions::default()).await.unwrap().error.is_none());
        assert_eq!(pool.stats().replaced, 1);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_policy_eval_async_reports_a_panic_too() {
        let sigil = Sigil::new(common::module()).unwrap();
        let policy = Arc::new(sigil.compile(&sigil_files(&deploy_gates()), options()).unwrap());
        let err = policy.eval_async(Panicky, EvalOptions::default()).await.unwrap_err();
        assert!(err.to_string().contains("the evaluation panicked"), "{err}");
    }
}
