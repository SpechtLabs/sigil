//! The module bundled by the `bundled` feature, see build.rs.

pub(crate) static WASM: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/sigil.wasm"));

/// The module precompiled for the target by build.rs, with the `precompiled`
/// feature: empty when build.rs couldn't (it says why in a build warning).
#[cfg(feature = "precompiled")]
pub(crate) static PRECOMPILED: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/sigil.cwasm"));
