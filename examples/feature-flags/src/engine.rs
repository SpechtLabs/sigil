//! Evaluating a flag: the host-side work around one Sigil evaluation. It
//! computes the rollout bucket and the kill switch, runs the flag's policy on
//! a pool instance off the async threads, and turns whatever comes back into
//! an OFREP answer. An evaluation that fails never fails the request: the
//! flag answers off with reason `ERROR`, is logged and counted.

use std::collections::BTreeSet;
use std::sync::Arc;
use std::time::{Duration, Instant};

use sigil::{Error, EvalOptions, EvalResult, FailureKind};
use tokio::task::JoinSet;
use tracing::field::Empty;
use tracing::{Instrument, Span};

use crate::flags;
use crate::metrics::Metrics;
use crate::ofrep::{self, Evaluation};
use crate::store::{Bundle, FlagInfo, Store};
use crate::verdict::{RolloutInput, User, Verdict};

/// How long past its timeout an evaluation may run before the engine kills it
/// from outside (which costs the instance: the pool replaces it).
const GRACE: Duration = Duration::from_millis(100);

/// Runs flag evaluations for the service.
pub struct Engine {
    store: Arc<Store>,
    killed: BTreeSet<String>,
    timeout: Duration,
    fuel: Option<u64>,
    metrics: Metrics,
}

impl Engine {
    pub fn new(store: Arc<Store>, killed: BTreeSet<String>, timeout: Duration, metrics: Metrics, fuel: Option<u64>) -> Self {
        Self { store, killed, timeout, fuel, metrics }
    }

    pub fn store(&self) -> &Arc<Store> {
        &self.store
    }

    /// Evaluates `key` for `user` on the serving bundle; `None` when no policy
    /// serves that key.
    pub async fn evaluate(&self, key: &str, user: &User) -> Option<Evaluation> {
        let bundle = self.store.current();
        let info = bundle.flags.get(key)?.clone();
        Some(self.evaluate_in(bundle, info, user.clone()).await)
    }

    /// Evaluates every flag of one bundle for `user`, concurrently, sorted by
    /// key. All of them come from the same bundle, so a reload during the call
    /// can't mix two versions in one answer.
    pub async fn evaluate_all(self: &Arc<Self>, user: &User) -> Vec<Evaluation> {
        let bundle = self.store.current();
        let mut set = JoinSet::new();
        for info in bundle.flags.values() {
            let engine = Arc::clone(self);
            let (bundle, info, user) = (Arc::clone(&bundle), info.clone(), user.clone());
            // A task doesn't inherit the current span, so carry it over.
            set.spawn(async move { engine.evaluate_in(bundle, info, user).await }.instrument(Span::current()));
        }
        let mut out = Vec::with_capacity(set.len());
        while let Some(done) = set.join_next().await {
            out.push(done.expect("evaluation tasks don't panic"));
        }
        out.sort_by(|a, b| a.key.cmp(&b.key));
        out
    }

    async fn evaluate_in(&self, bundle: Arc<Bundle>, info: FlagInfo, user: User) -> Evaluation {
        let span = tracing::info_span!(
            "evaluate flag",
            flag = %info.key,
            "sigil.policy" = %info.policy,
            "sigil.decision" = Empty,
            "sigil.reason" = Empty,
            "ofrep.reason" = Empty,
            "sigil.error" = Empty,
        );
        let input = RolloutInput {
            bucket: flags::bucket(&info.key, &user.id),
            killed: self.killed.contains(&info.key),
            flag: info.key.clone(),
            user,
        };
        let options = EvalOptions { timeout: Some(self.timeout), grace: Some(GRACE), fuel: self.fuel };
        let started = Instant::now();
        let run_span = span.clone();
        let policy = info.policy.clone();
        // The pool blocks for a free instance and then for the evaluation, so
        // it runs on a blocking thread; the span goes along into it.
        let joined = tokio::task::spawn_blocking(move || run_span.in_scope(|| bundle.pool.evaluate(&policy, &input, &options))).await;
        self.metrics.evaluation_duration.with_label_values(&[&info.key]).observe(started.elapsed().as_secs_f64());

        let evaluation = match joined.map_err(from_join).and_then(read) {
            Ok(verdict) => {
                self.metrics.evaluations.with_label_values(&[&info.key, verdict.decision(), verdict.reason()]).inc();
                let evaluation = ofrep::evaluation(&info.key, &info.policy, &info.spec, &verdict);
                span.record("sigil.decision", verdict.decision());
                span.record("sigil.reason", verdict.reason());
                span.in_scope(|| {
                    tracing::info!(
                        flag = %info.key,
                        policy = %info.policy,
                        decision = verdict.decision(),
                        reason = verdict.reason(),
                        ofrep_reason = evaluation.reason.as_str(),
                        duration_ms = started.elapsed().as_secs_f64() * 1000.0,
                        "flag evaluated"
                    );
                });
                evaluation
            }
            Err(failure) => {
                self.metrics.evaluation_errors.with_label_values(&[failure.kind]).inc();
                // The answer is off, so count it as the disable it is.
                self.metrics.evaluations.with_label_values(&[&info.key, "disable", "error"]).inc();
                if failure.replaced {
                    self.metrics.pool_replacements.inc();
                }
                span.record("sigil.error", failure.kind);
                span.in_scope(|| {
                    tracing::error!(flag = %info.key, policy = %info.policy, decision = "disable", kind = failure.kind, error = %failure.detail, "evaluation failed, the flag answers off");
                });
                ofrep::failed_evaluation(&info.key, &info.policy, &info.spec, failure.kind)
            }
        };
        span.record("ofrep.reason", evaluation.reason.as_str());
        evaluation
    }
}

/// Why an evaluation didn't decide.
#[derive(Debug, PartialEq, Eq)]
pub struct Failure {
    /// Bounded: `timeout`, `conflict`, `assertion`, `runtime`, `stopped`,
    /// `busy` or `internal`.
    pub kind: &'static str,
    pub detail: String,
    /// The pool replaced the instance the call stopped.
    pub replaced: bool,
}

/// A panic on the evaluation thread is a failed evaluation like any other: the
/// flag answers off, and the panic is logged and counted instead of taking the
/// request down.
fn from_join(e: tokio::task::JoinError) -> Failure {
    Failure { kind: "internal", detail: format!("the evaluation thread failed: {e}"), replaced: false }
}

/// Reads an evaluation's outcome, which is either a decision or a failure.
pub fn read(outcome: Result<EvalResult, Error>) -> Result<Verdict, Failure> {
    let result = match outcome {
        Ok(r) => r,
        Err(e) => {
            let kind = match &e {
                Error::Timeout(_) => "timeout",
                Error::Stopped(_) | Error::OutOfFuel => "stopped",
                Error::Busy(_) => "busy",
                _ => "internal",
            };
            return Err(Failure { kind, detail: e.to_string(), replaced: e.is_stopped() });
        }
    };
    if let Some(err) = &result.error {
        let kind = match err.kind {
            FailureKind::Canceled => "timeout",
            FailureKind::Conflict => "conflict",
            FailureKind::Assertion => "assertion",
            FailureKind::Runtime => "runtime",
        };
        return Err(Failure { kind, detail: err.message.clone(), replaced: false });
    }
    let decision = result.decision.as_deref().unwrap_or_default();
    let reason = result.reason.as_deref().unwrap_or_default();
    let variant = result.payload.as_ref().and_then(|p| p.get("variant")).and_then(|v| v.as_str());
    Verdict::from_parts(decision, reason, variant).ok_or_else(|| Failure {
        kind: "internal",
        detail: format!(
            "the policy decided {decision}({reason}), which the FeatureRollout kind this service was built with doesn't declare"
        ),
        replaced: false,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde_json::json;
    use sigil::StoppedError;

    use super::*;
    use crate::verdict::{DisableReason, EnableReason};

    fn result(extra: serde_json::Value) -> EvalResult {
        let mut base = json!({"policy": "flags.x", "outcome": [], "trace": []});
        base.as_object_mut().unwrap().extend(extra.as_object().unwrap().clone());
        serde_json::from_value(base).unwrap()
    }

    fn failure(kind: &str) -> serde_json::Value {
        json!({"error": {"kind": kind, "message": "boom", "help": "fix it"}})
    }

    #[rstest]
    #[case(json!({"decision": "enable", "reason": "rollout"}), Verdict::Enable { reason: EnableReason::Rollout, variant: "on".into() })]
    #[case(json!({"decision": "enable", "reason": "targeted", "payload": {"variant": "semantic"}}), Verdict::Enable { reason: EnableReason::Targeted, variant: "semantic".into() })]
    #[case(json!({"decision": "disable", "reason": "kill_switch"}), Verdict::Disable { reason: DisableReason::KillSwitch })]
    #[case(json!({"decision": "disable", "reason": "not_rolled_out"}), Verdict::Disable { reason: DisableReason::NotRolledOut })]
    fn decisions_are_read(#[case] extra: serde_json::Value, #[case] want: Verdict) {
        assert_eq!(read(Ok(result(extra))), Ok(want));
    }

    #[rstest]
    #[case::canceled("canceled", "timeout")]
    #[case::conflict("conflict", "conflict")]
    #[case::assertion("assertion", "assertion")]
    #[case::runtime("runtime", "runtime")]
    fn failed_evaluations_are_classified(#[case] kind: &str, #[case] want: &str) {
        let failure = read(Ok(result(failure(kind)))).unwrap_err();
        assert_eq!((failure.kind, failure.replaced), (want, false));
        assert_eq!(failure.detail, "boom");
    }

    #[rstest]
    #[case::hard_deadline(Error::Timeout(Duration::from_millis(150)), "timeout", true)]
    #[case::stopped(Error::Stopped(StoppedError { message: "trapped".into(), help: "rebuild".into() }), "stopped", true)]
    #[case::out_of_fuel(Error::OutOfFuel, "stopped", true)]
    #[case::no_free_instance(Error::Busy(Duration::from_millis(50)), "busy", false)]
    #[case::no_such_policy(Error::NoPolicy("flags.x".into()), "internal", false)]
    fn engine_errors_are_classified(#[case] err: Error, #[case] kind: &str, #[case] replaced: bool) {
        let failure = read(Err(err)).unwrap_err();
        assert_eq!((failure.kind, failure.replaced), (kind, replaced));
    }

    #[rstest]
    #[case(json!({"decision": "grant", "reason": "rollout"}))]
    #[case(json!({"decision": "enable", "reason": "mystery"}))]
    #[case(json!({}))]
    fn a_decision_the_kind_does_not_declare_is_internal(#[case] extra: serde_json::Value) {
        let failure = read(Ok(result(extra))).unwrap_err();
        assert_eq!(failure.kind, "internal");
    }
}
