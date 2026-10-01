//! The OpenFeature Remote Evaluation Protocol (OFREP), version 0.1: request
//! and response shapes, the error codes, and the mapping from a Sigil
//! decision to an OFREP answer.
//!
//! | Sigil decision                      | OFREP reason      | value           |
//! | ----------------------------------- | ----------------- | --------------- |
//! | `enable(rollout)`                   | `SPLIT`           | `true` or the variant |
//! | `enable(enterprise/beta_tester/targeted)` | `TARGETING_MATCH` | `true` or the variant |
//! | `disable(kill_switch)`              | `DISABLED`        | the off value   |
//! | `disable(region_not_ready)`         | `TARGETING_MATCH` | the off value   |
//! | `disable(not_rolled_out)`           | `DEFAULT`         | the off value   |
//! | evaluation failed                   | `ERROR`           | the off value   |
//!
//! A flag's value type comes from `flags.yaml` (see `manifest`): a boolean flag
//! answers `true` or `false` with its variant (`on` and `off` unless the policy
//! names another); a string flag answers its variant, or its declared off value.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::manifest::{FlagSpec, ValueType};
use crate::verdict::{EnableReason, Plan, User, Verdict};

pub const VARIANT_OFF: &str = "off";

/// `POST .../evaluate/flags[/{key}]`'s body.
#[derive(Debug, Default, Deserialize)]
pub struct EvaluationRequest {
    #[serde(default)]
    pub context: Map<String, Value>,
}

/// OFREP's evaluation reasons, the ones this service produces.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Reason {
    TargetingMatch,
    Split,
    Disabled,
    Default,
    Error,
}

impl Reason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::TargetingMatch => "TARGETING_MATCH",
            Self::Split => "SPLIT",
            Self::Disabled => "DISABLED",
            Self::Default => "DEFAULT",
            Self::Error => "ERROR",
        }
    }
}

/// One flag's answer; also an entry of the bulk answer.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct Evaluation {
    pub key: String,
    pub value: Value,
    pub reason: Reason,
    pub variant: String,
    pub metadata: BTreeMap<String, Value>,
}

/// OFREP's error codes.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ErrorCode {
    FlagNotFound,
    ParseError,
    TargetingKeyMissing,
    InvalidContext,
    General,
}

impl ErrorCode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::FlagNotFound => "FLAG_NOT_FOUND",
            Self::ParseError => "PARSE_ERROR",
            Self::TargetingKeyMissing => "TARGETING_KEY_MISSING",
            Self::InvalidContext => "INVALID_CONTEXT",
            Self::General => "GENERAL",
        }
    }
}

/// An error answer: the body of a 4xx or 5xx.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ErrorBody {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
    pub error_code: ErrorCode,
    pub error_details: String,
}

/// The bulk answer.
#[derive(Debug, Serialize)]
pub struct BulkResponse {
    pub flags: Vec<Evaluation>,
}

/// Why a context can't be evaluated, with the OFREP code it answers with.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ContextError {
    pub code: ErrorCode,
    pub details: String,
}

/// Reads the user a policy decides for from an OFREP context:
///
/// - `targetingKey` (required, a string) is the user's id.
/// - `plan` is `free` (the default), `pro` or `enterprise`.
/// - `region` is a string; a context without one gets `unknown`, which no
///   region guardrail accepts.
/// - `beta` is a boolean, false by default.
/// - every other attribute that is a string, number or boolean becomes a
///   string in `attributes`; objects, arrays and null are rejected, because
///   a policy can't read them.
pub fn user_from_context(context: &Map<String, Value>) -> Result<User, ContextError> {
    let invalid = |details: String| ContextError { code: ErrorCode::InvalidContext, details };
    let id = match context.get("targetingKey") {
        Some(Value::String(s)) if !s.is_empty() => s.clone(),
        Some(Value::String(_)) | None => {
            return Err(ContextError {
                code: ErrorCode::TargetingKeyMissing,
                details: "the context has no targetingKey; send the user's stable id as context.targetingKey".into(),
            });
        }
        Some(_) => return Err(invalid("targetingKey must be a string".into())),
    };
    let plan = match context.get("plan") {
        None => Plan::Free,
        Some(Value::String(s)) => Plan::parse(s).ok_or_else(|| invalid(format!("plan {s:?} is not free, pro or enterprise")))?,
        Some(_) => return Err(invalid("plan must be a string: free, pro or enterprise".into())),
    };
    let region = match context.get("region") {
        None => "unknown".to_owned(),
        Some(Value::String(s)) => s.clone(),
        Some(_) => return Err(invalid("region must be a string, such as eu-1".into())),
    };
    let beta = match context.get("beta") {
        None => false,
        Some(Value::Bool(b)) => *b,
        Some(_) => return Err(invalid("beta must be a boolean".into())),
    };
    let mut attributes = BTreeMap::new();
    for (name, value) in context {
        if matches!(name.as_str(), "targetingKey" | "plan" | "region" | "beta") {
            continue;
        }
        let text = match value {
            Value::String(s) => s.clone(),
            Value::Number(n) => n.to_string(),
            Value::Bool(b) => b.to_string(),
            _ => return Err(invalid(format!("attribute {name:?} must be a string, number or boolean"))),
        };
        attributes.insert(name.clone(), text);
    }
    Ok(User { id, plan, region, beta, attributes })
}

/// The OFREP answer for a verdict, in the value type the flag's manifest entry
/// declares: a boolean flag is `true` or `false`, a string flag its variant or
/// its off value.
pub fn evaluation(key: &str, policy: &str, spec: &FlagSpec, verdict: &Verdict) -> Evaluation {
    let (value, variant, reason) = match verdict {
        Verdict::Enable { reason, variant } => {
            let value = match spec.value_type {
                ValueType::Boolean => Value::Bool(true),
                ValueType::String => Value::String(variant.clone()),
            };
            let reason = if *reason == EnableReason::Rollout { Reason::Split } else { Reason::TargetingMatch };
            (value, variant.clone(), reason)
        }
        Verdict::Disable { reason } => {
            use crate::verdict::DisableReason as D;
            let reason = match reason {
                D::KillSwitch => Reason::Disabled,
                D::RegionNotReady => Reason::TargetingMatch,
                D::NotRolledOut => Reason::Default,
            };
            (off_value(spec), spec.off.clone(), reason)
        }
    };
    let mut metadata = BTreeMap::new();
    metadata.insert("sigil.policy".to_owned(), policy.into());
    metadata.insert("sigil.decision".to_owned(), verdict.decision().into());
    metadata.insert("sigil.reason".to_owned(), verdict.reason().into());
    Evaluation { key: key.to_owned(), value, reason, variant, metadata }
}

/// The fail-closed answer for an evaluation that failed: off, reason `ERROR`,
/// and the kind of failure in the metadata. The details stay in the logs.
pub fn failed_evaluation(key: &str, policy: &str, spec: &FlagSpec, kind: &str) -> Evaluation {
    let mut metadata = BTreeMap::new();
    metadata.insert("sigil.policy".to_owned(), policy.into());
    metadata.insert("sigil.error".to_owned(), kind.into());
    Evaluation { key: key.to_owned(), value: off_value(spec), reason: Reason::Error, variant: spec.off.clone(), metadata }
}

fn off_value(spec: &FlagSpec) -> Value {
    match spec.value_type {
        ValueType::Boolean => Value::Bool(false),
        ValueType::String => Value::String(spec.off.clone()),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde_json::json;

    use super::*;
    use crate::verdict::DisableReason;

    fn ctx(v: Value) -> Map<String, Value> {
        v.as_object().unwrap().clone()
    }

    #[test]
    fn a_full_context_becomes_a_user() {
        let user = user_from_context(&ctx(json!({
            "targetingKey": "u1", "plan": "pro", "region": "eu-1", "beta": true,
            "cohort": "checkout-pilot", "seats": 12, "trial": false
        })))
        .unwrap();
        assert_eq!(user.id, "u1");
        assert_eq!(user.plan, Plan::Pro);
        assert_eq!(user.region, "eu-1");
        assert!(user.beta);
        assert_eq!(user.attributes["cohort"], "checkout-pilot");
        assert_eq!(user.attributes["seats"], "12");
        assert_eq!(user.attributes["trial"], "false");
        assert_eq!(user.attributes.len(), 3);
    }

    #[test]
    fn defaults_are_the_safe_ones() {
        let user = user_from_context(&ctx(json!({"targetingKey": "u1"}))).unwrap();
        assert_eq!((user.plan, user.region.as_str(), user.beta), (Plan::Free, "unknown", false));
    }

    #[rstest]
    #[case(json!({}), ErrorCode::TargetingKeyMissing)]
    #[case(json!({"targetingKey": ""}), ErrorCode::TargetingKeyMissing)]
    #[case(json!({"targetingKey": 7}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "plan": "gold"}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "plan": 1}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "region": ["eu"]}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "beta": "yes"}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "nested": {"a": 1}}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "list": [1]}), ErrorCode::InvalidContext)]
    #[case(json!({"targetingKey": "u", "nothing": null}), ErrorCode::InvalidContext)]
    fn bad_contexts(#[case] context: Value, #[case] code: ErrorCode) {
        let err = user_from_context(&ctx(context)).unwrap_err();
        assert_eq!(err.code, code, "{}", err.details);
        assert!(!err.details.is_empty());
    }

    fn string_flag() -> FlagSpec {
        FlagSpec { value_type: ValueType::String, off: "control".into() }
    }

    #[rstest]
    #[case::rollout(Verdict::Enable { reason: EnableReason::Rollout, variant: "on".into() }, FlagSpec::default(), Reason::Split, json!(true), "on")]
    #[case::enterprise(Verdict::Enable { reason: EnableReason::Enterprise, variant: "on".into() }, FlagSpec::default(), Reason::TargetingMatch, json!(true), "on")]
    #[case::boolean_keeps_its_variant(Verdict::Enable { reason: EnableReason::BetaTester, variant: "semantic".into() }, FlagSpec::default(), Reason::TargetingMatch, json!(true), "semantic")]
    #[case::string_targeted(Verdict::Enable { reason: EnableReason::Targeted, variant: "hybrid".into() }, string_flag(), Reason::TargetingMatch, json!("hybrid"), "hybrid")]
    #[case::string_rollout(Verdict::Enable { reason: EnableReason::Rollout, variant: "hybrid".into() }, string_flag(), Reason::Split, json!("hybrid"), "hybrid")]
    #[case::string_on(Verdict::Enable { reason: EnableReason::Rollout, variant: "on".into() }, string_flag(), Reason::Split, json!("on"), "on")]
    #[case::kill_switch(Verdict::Disable { reason: DisableReason::KillSwitch }, FlagSpec::default(), Reason::Disabled, json!(false), "off")]
    #[case::region(Verdict::Disable { reason: DisableReason::RegionNotReady }, FlagSpec::default(), Reason::TargetingMatch, json!(false), "off")]
    #[case::not_rolled_out(Verdict::Disable { reason: DisableReason::NotRolledOut }, FlagSpec::default(), Reason::Default, json!(false), "off")]
    #[case::string_kill_switch(Verdict::Disable { reason: DisableReason::KillSwitch }, string_flag(), Reason::Disabled, json!("control"), "control")]
    #[case::string_not_rolled_out(Verdict::Disable { reason: DisableReason::NotRolledOut }, string_flag(), Reason::Default, json!("control"), "control")]
    fn verdicts_map_to_ofrep(
        #[case] verdict: Verdict,
        #[case] spec: FlagSpec,
        #[case] reason: Reason,
        #[case] value: Value,
        #[case] variant: &str,
    ) {
        let e = evaluation("k", "flags.k", &spec, &verdict);
        assert_eq!((e.reason, e.value, e.variant.as_str()), (reason, value, variant));
        assert_eq!(e.metadata["sigil.policy"], "flags.k");
        assert_eq!(e.metadata["sigil.decision"], verdict.decision());
        assert_eq!(e.metadata["sigil.reason"], verdict.reason());
    }

    #[rstest]
    #[case::boolean(FlagSpec::default(), json!(false), "off")]
    #[case::string(string_flag(), json!("control"), "control")]
    fn a_failed_evaluation_is_off_in_the_flags_type_with_reason_error(#[case] spec: FlagSpec, #[case] value: Value, #[case] variant: &str) {
        let e = failed_evaluation("k", "flags.k", &spec, "timeout");
        assert_eq!((e.reason, &e.value, e.variant.as_str()), (Reason::Error, &value, variant));
        assert_eq!(e.metadata["sigil.error"], "timeout");
        let body = serde_json::to_value(&e).unwrap();
        assert_eq!(body["reason"], "ERROR");
    }

    #[test]
    fn error_bodies_use_ofrep_names() {
        let body =
            serde_json::to_value(ErrorBody { key: Some("k".into()), error_code: ErrorCode::FlagNotFound, error_details: "x".into() })
                .unwrap();
        assert_eq!(body, json!({"key": "k", "errorCode": "FLAG_NOT_FOUND", "errorDetails": "x"}));
        let body = serde_json::to_value(ErrorBody { key: None, error_code: ErrorCode::ParseError, error_details: "x".into() }).unwrap();
        assert_eq!(body, json!({"errorCode": "PARSE_ERROR", "errorDetails": "x"}));
    }

    #[rstest]
    #[case(ErrorCode::FlagNotFound, "FLAG_NOT_FOUND")]
    #[case(ErrorCode::ParseError, "PARSE_ERROR")]
    #[case(ErrorCode::TargetingKeyMissing, "TARGETING_KEY_MISSING")]
    #[case(ErrorCode::InvalidContext, "INVALID_CONTEXT")]
    #[case(ErrorCode::General, "GENERAL")]
    fn error_code_names_match_serde(#[case] code: ErrorCode, #[case] name: &str) {
        assert_eq!(code.as_str(), name);
        assert_eq!(serde_json::to_value(code).unwrap(), name);
    }

    #[rstest]
    #[case(Reason::TargetingMatch, "TARGETING_MATCH")]
    #[case(Reason::Split, "SPLIT")]
    #[case(Reason::Disabled, "DISABLED")]
    #[case(Reason::Default, "DEFAULT")]
    #[case(Reason::Error, "ERROR")]
    fn reason_names_match_serde(#[case] reason: Reason, #[case] name: &str) {
        assert_eq!(reason.as_str(), name);
        assert_eq!(serde_json::to_value(reason).unwrap(), name);
    }
}
