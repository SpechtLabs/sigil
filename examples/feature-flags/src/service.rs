//! The composition root: builds every long-lived object from a [`Config`] and
//! a compiled Sigil module, and runs them until shutdown. Nothing is created at
//! import time, and nothing here touches process globals, so tests call
//! [`Service::new`] directly.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use sigil::Module;
use tokio::net::TcpListener;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::watch;

use crate::api::{self, AppState};
use crate::config::Config;
use crate::engine::Engine;
use crate::metrics::Metrics;
use crate::reload::{Reloader, Trigger};
use crate::store::{Source, Store};

/// A built service: the router's state and the pieces `serve` runs around it.
pub struct Service {
    pub config: Config,
    pub state: AppState,
}

impl Service {
    /// Loads the first policy bundle and wires the engine. Blocking: it
    /// compiles every flag in every pool instance. An error says what to fix.
    pub fn new(config: Config, module: Module, metrics: Metrics) -> Result<Self, String> {
        let precompiled = module.is_precompiled();
        metrics.module_precompiled.set(i64::from(precompiled));
        if precompiled {
            tracing::info!("the Sigil module loaded precompiled");
        } else {
            tracing::warn!(
                fuel = config.evaluation_fuel.is_some(),
                "the Sigil module was compiled at startup, about 4 s of CPU time at every start; build the crate with the precompiled feature (and leave FEATUREGATE_EVALUATION_FUEL unset) to load it in milliseconds"
            );
        }
        let source = config.policies.clone().map_or(Source::Embedded, Source::Directory);
        let store = Store::open(module, source, config.workers, config.evaluation_timeout)?;
        let store = Arc::new(store);
        let killed = config.killed_flags.clone();
        let unserved = store.current().unserved(&killed);
        if !unserved.is_empty() {
            return Err(format!(
                "FEATUREGATE_KILLED_FLAGS names flags no policy serves: {}\n  help: the kill switch would switch nothing off; check the spelling against GET /api/v1/flags",
                unserved.join(", ")
            ));
        }
        let reloader = Reloader::new(Arc::clone(&store), metrics.clone(), killed);
        reloader.record_startup();
        let engine =
            Arc::new(Engine::new(store, config.killed_flags.clone(), config.evaluation_timeout, metrics.clone(), config.evaluation_fuel));
        let state = AppState { engine, metrics, reloader, draining: Arc::new(AtomicBool::new(false)), version: config.version.clone() };
        Ok(Self { config, state })
    }

    /// Serves until SIGTERM or SIGINT, reloading on the poll interval and on
    /// SIGHUP, then drains: `/readyz` turns 503, in-flight requests finish
    /// (up to the shutdown timeout) and the listener closes.
    pub async fn serve(self, listener: TcpListener) -> std::io::Result<()> {
        let mut term = signal(SignalKind::terminate())?;
        let stop = async move {
            tokio::select! {
                _ = term.recv() => tracing::info!(signal = "SIGTERM", "shutting down"),
                _ = tokio::signal::ctrl_c() => tracing::info!(signal = "SIGINT", "shutting down"),
            }
        };
        self.serve_until(listener, stop).await
    }

    /// [`Service::serve`] with the shutdown trigger given: serving ends after
    /// `stop` completes and the drain finishes. Tests pass their own.
    pub async fn serve_until(self, listener: TcpListener, stop: impl Future<Output = ()> + Send + 'static) -> std::io::Result<()> {
        let (stop_tx, mut stop_rx) = watch::channel(false);
        let app = api::router(self.state.clone());
        tracing::info!(addr = %listener.local_addr()?, version = %self.config.version, "listening");
        self.state.metrics.ready.set(1);

        let mut background = tokio::task::JoinSet::new();
        if !self.config.reload_interval.is_zero() {
            let (reloader, interval, mut stop) = (self.state.reloader.clone(), self.config.reload_interval, stop_rx.clone());
            background.spawn(async move {
                let mut tick = tokio::time::interval(interval);
                tick.tick().await; // the first tick is immediate, and the bundle just loaded
                loop {
                    tokio::select! {
                        _ = tick.tick() => { reloader.reload(Trigger::Poll, false).await; }
                        _ = stop.changed() => return,
                    }
                }
            });
        }
        {
            let (reloader, mut stop) = (self.state.reloader.clone(), stop_rx.clone());
            let mut hup = signal(SignalKind::hangup())?;
            background.spawn(async move {
                loop {
                    tokio::select! {
                        _ = hup.recv() => { reloader.reload(Trigger::Signal, true).await; }
                        _ = stop.changed() => return,
                    }
                }
            });
        }

        let (draining, metrics, timeout) = (Arc::clone(&self.state.draining), self.state.metrics.clone(), self.config.shutdown_timeout);
        let shutdown = async move {
            stop.await;
            draining.store(true, Ordering::Release);
            metrics.ready.set(0);
            let _ = stop_tx.send(true);
        };
        let server = axum::serve(listener, app).with_graceful_shutdown(shutdown);
        let result = tokio::select! {
            r = server => r,
            // The drain has a deadline: a client holding a connection open can't keep the process up.
            _ = async { stop_rx.changed().await.ok(); tokio::time::sleep(timeout).await } => {
                tracing::warn!(timeout = ?timeout, "requests were still in flight at the shutdown timeout, closing");
                Ok(())
            }
        };
        background.shutdown().await;
        result
    }
}
