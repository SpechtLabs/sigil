//! What the FeatureRollout kind decides, as Rust types: the input a policy
//! reads and the outcome it returns. Both are plain data; `engine` converts
//! between them and Sigil's JSON.

use std::collections::BTreeMap;

use serde::Serialize;

/// The customer tier a context names.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Plan {
    Free,
    Pro,
    Enterprise,
}

impl Plan {
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "free" => Some(Self::Free),
            "pro" => Some(Self::Pro),
            "enterprise" => Some(Self::Enterprise),
            _ => None,
        }
    }
}

/// Who the flag is evaluated for, read from an OFREP context.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct User {
    pub id: String,
    pub plan: Plan,
    pub region: String,
    pub beta: bool,
    pub attributes: BTreeMap<String, String>,
}

/// The kind's input: what `policies/feature_rollout.sigil` calls `flag`,
/// `user`, `bucket` and `killed`.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct RolloutInput {
    pub flag: String,
    pub user: User,
    /// The user's place in the flag's rollout, 0 to 99.
    pub bucket: i64,
    /// The platform kill switch for this flag.
    pub killed: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EnableReason {
    Enterprise,
    BetaTester,
    Rollout,
    Targeted,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DisableReason {
    KillSwitch,
    RegionNotReady,
    NotRolledOut,
}

/// The kind's two decisions with their reasons and payload.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Verdict {
    Enable { reason: EnableReason, variant: String },
    Disable { reason: DisableReason },
}

impl Verdict {
    /// Reads a decision the way `sigil eval -o json` names it. `None` for one
    /// the kind doesn't declare, which only a kind/service mismatch produces.
    pub fn from_parts(decision: &str, reason: &str, variant: Option<&str>) -> Option<Self> {
        match decision {
            "enable" => Some(Self::Enable {
                reason: match reason {
                    "enterprise" => EnableReason::Enterprise,
                    "beta_tester" => EnableReason::BetaTester,
                    "rollout" => EnableReason::Rollout,
                    "targeted" => EnableReason::Targeted,
                    _ => return None,
                },
                variant: variant.unwrap_or("on").to_owned(),
            }),
            "disable" => Some(Self::Disable {
                reason: match reason {
                    "kill_switch" => DisableReason::KillSwitch,
                    "region_not_ready" => DisableReason::RegionNotReady,
                    "not_rolled_out" => DisableReason::NotRolledOut,
                    _ => return None,
                },
            }),
            _ => None,
        }
    }

    /// The decision's name, as it appears in metrics and metadata.
    pub fn decision(&self) -> &'static str {
        match self {
            Self::Enable { .. } => "enable",
            Self::Disable { .. } => "disable",
        }
    }

    /// The reason's name.
    pub fn reason(&self) -> &'static str {
        match self {
            Self::Enable { reason, .. } => match reason {
                EnableReason::Enterprise => "enterprise",
                EnableReason::BetaTester => "beta_tester",
                EnableReason::Rollout => "rollout",
                EnableReason::Targeted => "targeted",
            },
            Self::Disable { reason } => match reason {
                DisableReason::KillSwitch => "kill_switch",
                DisableReason::RegionNotReady => "region_not_ready",
                DisableReason::NotRolledOut => "not_rolled_out",
            },
        }
    }

    pub fn is_enabled(&self) -> bool {
        matches!(self, Self::Enable { .. })
    }
}
