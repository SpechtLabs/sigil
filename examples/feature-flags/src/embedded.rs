//! The documents compiled into the binary: the platform's trusted policies,
//! which no mounted directory can replace, and a sample flag bundle served
//! when `FEATUREGATE_POLICIES` is empty. They are the files under `policies/`,
//! so `sigil test` and the service read the very same text.

use sigil::SourceFile;

/// `platform.guardrails` and what it imports. Required of every flag policy.
pub fn platform() -> Vec<SourceFile> {
    vec![
        SourceFile::new("platform/residency.sigil", include_str!("../policies/platform/residency.sigil")),
        SourceFile::new("platform/guardrails.sigil", include_str!("../policies/platform/guardrails.sigil")),
    ]
}

/// The sample flags, one file each.
pub fn sample_flags() -> Vec<SourceFile> {
    vec![
        SourceFile::new("flags.yaml", include_str!("../policies/flags/flags.yaml")),
        SourceFile::new("flags/beta_api.sigil", include_str!("../policies/flags/beta_api.sigil")),
        SourceFile::new("flags/dark_mode.sigil", include_str!("../policies/flags/dark_mode.sigil")),
        SourceFile::new("flags/new_checkout.sigil", include_str!("../policies/flags/new_checkout.sigil")),
        SourceFile::new("flags/search_v2.sigil", include_str!("../policies/flags/search_v2.sigil")),
    ]
}
