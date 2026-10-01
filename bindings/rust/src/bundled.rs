//! The module bundled by the `bundled` feature, see build.rs.

pub(crate) static WASM: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/sigil.wasm"));
