//! The flag manifest, `flags.yaml` beside the flag policies: what value type
//! each flag answers in, and for a string flag its off value.
//!
//! OFREP clients ask for a typed value (`getStringValue`), and a flag that
//! answers `false` where a string was asked for is a type mismatch. A policy
//! decides *whether* a flag is on and with which variant; the manifest says
//! what the flag's values look like, so that on, off and ERROR all answer in
//! the flag's own type. A flag with no entry is boolean.
//!
//! ```yaml
//! flags:
//!   search-v2:
//!     type: string
//!     off: control   # the value and variant a user the flag is off for gets
//! ```

use std::collections::BTreeMap;

use serde::Deserialize;

use crate::flags;

/// The file's name, in the policies directory.
pub const MANIFEST_FILE: &str = "flags.yaml";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ValueType {
    Boolean,
    String,
}

/// What one flag answers in.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FlagSpec {
    pub value_type: ValueType,
    /// The off variant's name, which is also a string flag's off value.
    pub off: String,
}

impl Default for FlagSpec {
    fn default() -> Self {
        Self { value_type: ValueType::Boolean, off: crate::ofrep::VARIANT_OFF.to_owned() }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct File {
    #[serde(default)]
    flags: BTreeMap<String, Entry>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Entry {
    #[serde(rename = "type")]
    value_type: ValueType,
    off: Option<String>,
}

/// Parses and validates a manifest. Whether every key is a served flag is the
/// store's check, which knows the flags.
pub fn parse(text: &str) -> Result<BTreeMap<String, FlagSpec>, String> {
    let file: File = serde_yaml_ng::from_str(text).map_err(|e| format!("{MANIFEST_FILE} doesn't parse: {e}\n  help: it holds `flags:` and, under it, each flag's `type: boolean|string` and, for a string flag, `off:`"))?;
    let mut out = BTreeMap::new();
    for (key, entry) in file.flags {
        flags::validate_key(&key).map_err(|e| format!("{MANIFEST_FILE}: {e}"))?;
        let spec = match (entry.value_type, entry.off) {
            (ValueType::Boolean, None) => FlagSpec::default(),
            (ValueType::Boolean, Some(_)) => {
                return Err(format!(
                    "{MANIFEST_FILE}: the boolean flag {key} has an `off:`; a boolean flag is off as false\n  help: remove it, or make the flag `type: string`"
                ));
            }
            (ValueType::String, Some(off)) if !off.is_empty() => FlagSpec { value_type: ValueType::String, off },
            (ValueType::String, _) => {
                return Err(format!(
                    "{MANIFEST_FILE}: the string flag {key} needs an `off:` value, the one users it is off for get\n  help: add `off: control`, or whatever your clients treat as the default"
                ));
            }
        };
        out.insert(key, spec);
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[test]
    fn a_manifest_declares_types_and_off_values() {
        let m = parse("flags:\n  search-v2:\n    type: string\n    off: control\n  dark-mode:\n    type: boolean\n").unwrap();
        assert_eq!(m["search-v2"], FlagSpec { value_type: ValueType::String, off: "control".into() });
        assert_eq!(m["dark-mode"], FlagSpec::default());
    }

    #[test]
    fn an_empty_manifest_declares_nothing() {
        assert!(parse("").unwrap().is_empty(), "a file of comments declares nothing");
        assert!(parse("flags: {}\n").unwrap().is_empty());
    }

    #[rstest]
    #[case::string_without_off("flags:\n  a:\n    type: string\n", "needs an `off:`")]
    #[case::string_with_empty_off("flags:\n  a:\n    type: string\n    off: \"\"\n", "needs an `off:`")]
    #[case::boolean_with_off("flags:\n  a:\n    type: boolean\n    off: x\n", "has an `off:`")]
    #[case::bad_type("flags:\n  a:\n    type: number\n", "doesn't parse")]
    #[case::unknown_field("flags:\n  a:\n    type: boolean\n    color: red\n", "doesn't parse")]
    #[case::unknown_top_level("flagz: {}\n", "doesn't parse")]
    #[case::bad_key("flags:\n  Bad_Key:\n    type: boolean\n", "not a flag key")]
    fn invalid_manifests_say_what_to_fix(#[case] text: &str, #[case] hint: &str) {
        let err = parse(text).unwrap_err();
        assert!(err.contains(hint), "{err}");
        assert!(err.contains(MANIFEST_FILE));
    }
}
