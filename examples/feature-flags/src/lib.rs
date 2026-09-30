//! featuregate: an OFREP feature-flag service whose rollouts are Sigil policies.
//! See ARCHITECTURE.md for how the pieces fit.

pub mod api;
pub mod cases;
pub mod config;
pub mod embedded;
pub mod engine;
pub mod error;
pub mod flags;
pub mod kind;
pub mod manifest;
pub mod metrics;
pub mod ofrep;
pub mod reload;
pub mod service;
pub mod store;
pub mod telemetry;
pub mod verdict;
