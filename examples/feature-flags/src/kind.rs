//! The FeatureRollout kind, defined with the `sigil` crate's kind builder.
//! It is the source of truth: `featuregate export-kind` writes it out as
//! `policies/feature_rollout.sigil` for the tooling that runs without this
//! code (`sigil check`, `sigil test`), and a test fails when that copy is
//! stale.
//!
//! Decisions are listed in precedence order: a disable beats an enable, so the
//! platform's guardrails (which only ever disable) can't be outvoted by a
//! flag policy, however it ranks its own enables. Each decision ranks its
//! reasons in the order declared: `kill_switch` leads the disables, so a
//! killed flag reports the kill switch even where its region isn't ready
//! either.

use std::sync::OnceLock;

use serde_json::json;
use sigil::{Decision, Kind, Type};

/// Where the kind file is placed among the documents the engine reads.
pub const KIND_PATH: &str = "feature_rollout.sigil";

/// The kind, built once on first use.
pub fn feature_rollout() -> &'static Kind {
    static KIND: OnceLock<Kind> = OnceLock::new();
    KIND.get_or_init(|| {
        let plan = Type::enumeration("Plan", ["free", "pro", "enterprise"]);
        let user = Type::structure(
            "User",
            [
                ("id", Type::string()),
                ("plan", plan),
                ("region", Type::string()),
                ("beta", Type::bool()),
                ("attributes", Type::map(Type::string(), Type::string())),
            ],
        );
        let enable = Decision::new("enable", ["enterprise", "beta_tester", "targeted", "rollout"]).field_default(
            "variant",
            Type::string(),
            json!("on"),
        );
        let disable = Decision::new("disable", ["kill_switch", "region_not_ready", "not_rolled_out"]);
        Kind::builder("FeatureRollout")
            .version(1)
            .input("flag", Type::string())
            .input("user", user)
            .input("bucket", Type::int())
            .input("killed", Type::bool())
            .decisions([&disable, &enable])
            .rank_reasons(&disable)
            .rank_reasons(&enable)
            .default_outcome(disable.reason("not_rolled_out"))
            .build()
            .expect("the FeatureRollout kind is valid")
    })
}

/// The kind file: `kind FeatureRollout version 1` and everything it declares.
pub fn schema() -> String {
    feature_rollout().schema().to_owned()
}
