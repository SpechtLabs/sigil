//! Reloading the policies: the one place that runs a store reload, counts it
//! and logs it, whoever asked (the poll timer, SIGHUP, the endpoint).

use std::collections::BTreeSet;
use std::sync::Arc;

use crate::metrics::Metrics;
use crate::store::{ReloadOutcome, Store};

/// What set a reload off, for the log line.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Trigger {
    Startup,
    Poll,
    Signal,
    Endpoint,
}

impl Trigger {
    fn as_str(self) -> &'static str {
        match self {
            Self::Startup => "startup",
            Self::Poll => "poll",
            Self::Signal => "signal",
            Self::Endpoint => "endpoint",
        }
    }
}

#[derive(Clone)]
pub struct Reloader {
    store: Arc<Store>,
    metrics: Metrics,
    /// The kill switch's flag names, to report the ones a reload left unmatched.
    killed: Arc<BTreeSet<String>>,
}

impl Reloader {
    pub fn new(store: Arc<Store>, metrics: Metrics, killed: BTreeSet<String>) -> Self {
        Self { store, metrics, killed: Arc::new(killed) }
    }

    /// Records the bundle the store opened with.
    pub fn record_startup(&self) {
        let bundle = self.store.current();
        self.metrics.set_loaded(bundle.source.label(), &bundle.digest, bundle.flags.len());
        self.report_unmatched(&bundle);
        self.metrics.reloads.with_label_values(&["loaded"]).inc();
        tracing::info!(trigger = Trigger::Startup.as_str(), source = bundle.source.label(), digest = %bundle.digest, flags = bundle.flags.len(), "policies loaded");
    }

    /// Reloads on a blocking thread and reports. `force` compiles again even
    /// when the documents haven't changed.
    pub async fn reload(&self, trigger: Trigger, force: bool) -> ReloadOutcome {
        let store = Arc::clone(&self.store);
        let outcome = outcome_of(tokio::task::spawn_blocking(move || store.reload(force)).await);
        match &outcome {
            ReloadOutcome::Loaded { digest, flags } => {
                let bundle = self.store.current();
                self.metrics.reloads.with_label_values(&["loaded"]).inc();
                self.metrics.set_loaded(bundle.source.label(), digest, flags.len());
                self.report_unmatched(&bundle);
                tracing::info!(trigger = trigger.as_str(), digest = %digest, flags = flags.len(), "policies reloaded");
            }
            ReloadOutcome::Unchanged { digest } => {
                self.metrics.reloads.with_label_values(&["unchanged"]).inc();
                tracing::debug!(trigger = trigger.as_str(), digest = %digest, "policies unchanged");
            }
            ReloadOutcome::Failed { error } => {
                self.metrics.reloads.with_label_values(&["failed"]).inc();
                tracing::warn!(trigger = trigger.as_str(), error = %error, "policy reload failed, the last known good bundle keeps serving");
            }
        }
        outcome
    }
}

impl Reloader {
    /// Sets the unmatched-kill-switch gauge for `bundle`, and warns when a
    /// reload (a flag removed, a key renamed) left a killed name with no flag.
    fn report_unmatched(&self, bundle: &crate::store::Bundle) {
        let unmatched = bundle.unserved(&self.killed);
        self.metrics.killed_flags_unmatched.set(unmatched.len() as i64);
        if !unmatched.is_empty() {
            tracing::warn!(flags = %unmatched.join(","), "FEATUREGATE_KILLED_FLAGS names flags the loaded policies don't serve; the kill switch switches nothing off for them");
        }
    }
}

/// A reload thread that panicked is a failed reload, not a dead poll task: the
/// last known good bundle keeps serving and the next tick tries again.
fn outcome_of(joined: Result<ReloadOutcome, tokio::task::JoinError>) -> ReloadOutcome {
    joined.unwrap_or_else(|e| ReloadOutcome::Failed { error: format!("the reload thread failed: {e}") })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn a_panicking_reload_is_a_failed_reload() {
        let joined = tokio::task::spawn_blocking(|| -> ReloadOutcome { panic!("boom") }).await;
        match outcome_of(joined) {
            ReloadOutcome::Failed { error } => assert!(error.contains("reload thread failed"), "{error}"),
            other => panic!("{other:?}"),
        }
    }

    #[tokio::test]
    async fn a_finished_reload_passes_through() {
        let joined = tokio::task::spawn_blocking(|| ReloadOutcome::Unchanged { digest: "d".into() }).await;
        assert_eq!(outcome_of(joined), ReloadOutcome::Unchanged { digest: "d".into() });
    }
}
