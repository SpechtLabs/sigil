//! The wasmtime configuration of every engine this crate builds, in one place:
//! `build.rs` precompiles the bundled module with it (the `precompiled`
//! feature), and wasmtime refuses an artifact compiled under settings that
//! differ from the engine that loads it, so the two must not drift.

/// The configuration for a module with or without fuel metering.
pub(crate) fn config(fuel: bool) -> wasmtime::Config {
    let mut config = wasmtime::Config::new();
    // Epoch interruption is what kills a call from outside; it costs a load
    // and a compare at loop headers and function entries.
    config.epoch_interruption(true);
    config.consume_fuel(fuel);
    config
}
