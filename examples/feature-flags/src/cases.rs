//! The request fixtures of `requests/cases.json`, shared by the integration
//! tests, the e2e suite, the demo client and (as data) the k6 load test: one
//! OFREP request and the answer a correct service gives it.

use serde::Deserialize;
use serde_json::{Map, Value};

/// The fixture file, compiled in so the demo runs from anywhere.
const CASES: &str = include_str!("../requests/cases.json");

/// One request and what it must answer.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Case {
    pub name: String,
    pub description: String,
    /// The flag key, or `None` for the bulk endpoint.
    pub flag: Option<String>,
    #[serde(default)]
    pub context: Map<String, Value>,
    /// A body sent as it is, for a request that isn't valid JSON.
    pub raw_body: Option<String>,
    pub expect: Expect,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Expect {
    pub status: u16,
    pub value: Option<Value>,
    pub reason: Option<String>,
    pub variant: Option<String>,
    /// The `sigil.reason` metadata.
    pub sigil_reason: Option<String>,
    pub error_code: Option<String>,
    /// For a bulk case: each flag's expected value, reason and variant.
    pub flags: Option<Map<String, Value>>,
}

fn compare(out: &mut Vec<String>, what: &str, got: Option<&Value>, want: Option<Value>) {
    if let Some(want) = want
        && got != Some(&want)
    {
        out.push(format!("{what} is {}, want {want}", got.map_or("missing".to_owned(), Value::to_string)));
    }
}

impl Case {
    /// Every case of the fixture file.
    pub fn all() -> Vec<Case> {
        serde_json::from_str(CASES).expect("requests/cases.json is valid")
    }

    /// The URL path this case posts to.
    pub fn path(&self) -> String {
        match &self.flag {
            Some(key) => format!("/ofrep/v1/evaluate/flags/{key}"),
            None => "/ofrep/v1/evaluate/flags".to_owned(),
        }
    }

    /// The request body.
    pub fn body(&self) -> String {
        self.raw_body.clone().unwrap_or_else(|| serde_json::json!({ "context": self.context }).to_string())
    }

    /// What differs between `status` and `body` and the expectation; empty
    /// when the service answered correctly.
    pub fn mismatches(&self, status: u16, body: &Value) -> Vec<String> {
        let mut out = Vec::new();
        let e = &self.expect;
        if status != e.status {
            out.push(format!("status {status}, want {}", e.status));
        }
        compare(&mut out, "errorCode", body.get("errorCode"), e.error_code.clone().map(Value::from));
        if let Some(flags) = &e.flags {
            let listed = body.get("flags").and_then(Value::as_array).cloned().unwrap_or_default();
            if listed.len() != flags.len() {
                out.push(format!("{} flags answered, want {}", listed.len(), flags.len()));
            }
            for (key, want) in flags {
                let got = listed.iter().find(|f| f.get("key").and_then(Value::as_str) == Some(key));
                for field in ["value", "reason", "variant"] {
                    compare(&mut out, &format!("{key}.{field}"), got.and_then(|g| g.get(field)), want.get(field).cloned());
                }
            }
        } else {
            compare(&mut out, "value", body.get("value"), e.value.clone());
            compare(&mut out, "reason", body.get("reason"), e.reason.clone().map(Value::from));
            compare(&mut out, "variant", body.get("variant"), e.variant.clone().map(Value::from));
            compare(&mut out, "metadata[sigil.reason]", body.pointer("/metadata/sigil.reason"), e.sigil_reason.clone().map(Value::from));
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    #[test]
    fn the_fixtures_parse_and_are_well_formed() {
        let cases = Case::all();
        assert!(cases.len() >= 20);
        let mut names: Vec<_> = cases.iter().map(|c| c.name.as_str()).collect();
        names.sort();
        names.dedup();
        assert_eq!(names.len(), cases.len(), "case names are unique");
        for c in &cases {
            assert!(!c.description.is_empty(), "{}", c.name);
            assert!(c.body().starts_with('{'), "{}", c.name);
        }
    }

    #[test]
    fn paths_follow_the_flag() {
        let cases = Case::all();
        let bulk = cases.iter().find(|c| c.flag.is_none()).unwrap();
        assert_eq!(bulk.path(), "/ofrep/v1/evaluate/flags");
        let single = cases.iter().find(|c| c.flag.as_deref() == Some("dark-mode")).unwrap();
        assert_eq!(single.path(), "/ofrep/v1/evaluate/flags/dark-mode");
    }

    #[test]
    fn mismatches_name_what_differs() {
        let case = Case::all().into_iter().find(|c| c.name == "dark-mode-everyone").unwrap();
        let good = json!({"key": "dark-mode", "value": true, "reason": "SPLIT", "variant": "on", "metadata": {"sigil.reason": "rollout"}});
        assert!(case.mismatches(200, &good).is_empty());
        let bad = json!({"key": "dark-mode", "value": false, "reason": "DEFAULT", "variant": "off", "metadata": {"sigil.reason": "not_rolled_out"}});
        let diff = case.mismatches(500, &bad);
        assert_eq!(diff.len(), 5, "{diff:?}");
        assert!(diff[0].contains("status 500"));
    }

    #[test]
    fn bulk_mismatches_name_the_flag() {
        let case = Case::all().into_iter().find(|c| c.name == "bulk-pro-eu").unwrap();
        let body = json!({"flags": [{"key": "dark-mode", "value": false, "reason": "SPLIT", "variant": "on"}]});
        let diff = case.mismatches(200, &body);
        assert!(diff.iter().any(|d| d.contains("1 flags answered")), "{diff:?}");
        assert!(diff.iter().any(|d| d.contains("dark-mode.value")), "{diff:?}");
    }
}
