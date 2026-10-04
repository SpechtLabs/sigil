//! Sigil in Rust: the Go policy engine, compiled to WebAssembly and run on
//! [wasmtime](https://wasmtime.dev), behind the API of `policy.NewKind`.
//!
//! A host defines a [`Kind`] (the inputs a policy reads, the decisions it may
//! construct, the host functions it may call), compiles its teams' policies
//! against it, and evaluates them. The answers are the stock `sigil` CLI's for
//! the same files: [`Sigil::check`] is `sigil check -o json`,
//! [`Policy::eval`] is `sigil eval -o json`, and so on.
//!
//! ```no_run
//! use serde_json::json;
//! use sigil::{CompileOptions, Decision, EvalOptions, Kind, KindCompileOptions, Sigil, SourceFile, Type};
//!
//! # fn main() -> Result<(), sigil::Error> {
//! let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));
//! let kind = Kind::builder("Routing")
//!     .version(1)
//!     .input("team", Type::string())
//!     .decisions([&notify])
//!     .default_outcome(notify.reason("unrouted"))
//!     .build()?;
//!
//! let sigil = Sigil::bundled()?;
//! let files = [SourceFile::new(
//!     "checkout.sigil",
//!     "policy checkout.routing: Routing@1\n\nwhen team == \"checkout\" {\n  notify(reason: routine, channel: \"#checkout\")\n}\n",
//! )];
//! let options = KindCompileOptions { compile: CompileOptions { policy: Some("checkout.routing".into()), ..Default::default() }, kind_file: None };
//! let policy = kind.compile(&sigil, &files, options)?;
//!
//! let result = policy.eval_with(&json!({ "team": "checkout" }), &EvalOptions::timeout(std::time::Duration::from_millis(50)))?;
//! assert!(notify.reason("routine").is(&result)?);
//! # Ok(())
//! # }
//! ```
//!
//! # Where things are
//!
//! - [`Module`] compiles `sigil.wasm` once; [`Sigil`] is one instance of it and
//!   [`Policy`] a policy compiled in it. [`Pool`] is several instances for
//!   parallel evaluation, which replaces any that stops.
//! - [`Kind`], [`Decision`] and [`Type`] define a kind in Rust, with the kind
//!   file ([`Kind::schema`]) byte for byte what Go's `Kind.Schema` writes.
//! - [`Error`] says what failed and what to do. A failed *evaluation* is not an
//!   error: [`EvalResult::error`] says why, like the CLI.
//! - [`Limits`], [`EvalOptions`] and [`ModuleConfig`] bound a call by time
//!   (the ABI's timeout, and epoch interruption that kills a call from outside)
//!   and, optionally, by fuel.
//!
//! # Features
//!
//! - `bundled` (on by default) embeds `sigil.wasm` in the crate, see
//!   [`Module::bundled`]. Without it, load the module yourself with
//!   [`Module::from_file`] or [`Module::from_bytes`].
//! - `precompiled` (implies `bundled`) compiles the bundled module for the
//!   target at build time, so [`Module::bundled`] loads native code in
//!   milliseconds instead of compiling for about 4 s of CPU at every start. It
//!   makes the build longer and the binary larger, and falls back to compiling
//!   when wasmtime refuses the artifact.
//! - `tokio` adds `Pool::evaluate_async`, `Pool::compile_async` and
//!   `Policy::eval_async`, which run the blocking calls on tokio's blocking pool.
//!   Off by default: without it the crate has no tokio in its tree.

mod duration;
mod engine;
mod error;
mod module;
mod pool;
mod runtime;
mod sigil;
mod types;
mod wasi;

pub mod kind;

#[cfg(feature = "bundled")]
mod bundled;

pub use duration::{format_duration, parse_duration};
pub use error::{Error, SigilError, StoppedError};
pub use kind::{Decision, FnDecl, Kind, KindBuilder, KindCompileOptions, Matched, Outcome, OutcomeRef, Type};
pub use module::{Limits, Module, ModuleConfig};
pub use pool::{Pool, PoolOptions, PoolStats};
pub use runtime::ABI_VERSION;
pub use sigil::{Policy, Sigil};
pub use types::*;
