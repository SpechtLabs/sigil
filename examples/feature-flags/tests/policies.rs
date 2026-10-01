//! The policies under `policies/`: every `*_test.yaml` case runs against the
//! flag policies compiled the way the service compiles them (kind, required
//! platform guardrail, trusted documents), so a case proves what production
//! decides. `sigil test` runs the same files from the CLI (`mise run
//! policies`); this suite keeps them honest without the Go toolchain.

mod common;

use std::collections::BTreeSet;
use std::path::{Path, PathBuf};
use std::time::Duration;

use featuregate::flags;
use featuregate::kind;
use featuregate::store::{Source, Store};
use serde_json::Value;
use sigil::EvalOptions;

fn flags_dir() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("policies/flags")
}

fn test_files() -> Vec<PathBuf> {
    let mut files: Vec<_> = std::fs::read_dir(flags_dir())
        .unwrap()
        .map(|e| e.unwrap().path())
        .filter(|p| p.file_name().unwrap().to_string_lossy().ends_with("_test.yaml"))
        .collect();
    files.sort();
    files
}

fn yaml(path: &Path) -> Value {
    serde_json::to_value(serde_yaml_ng::from_str::<serde_yaml_ng::Value>(&std::fs::read_to_string(path).unwrap()).unwrap()).unwrap()
}

/// Whether every key of `want` is in `got` with the same value.
fn is_subset(want: &Value, got: &Value) -> bool {
    match (want, got) {
        (Value::Object(w), Value::Object(g)) => w.iter().all(|(k, v)| g.get(k).is_some_and(|gv| is_subset(v, gv))),
        _ => want == got,
    }
}

#[test]
fn every_test_case_decides_as_expected() {
    let store = Store::open(common::module(), Source::Directory(flags_dir()), 1, Duration::from_secs(5)).expect("the sample policies load");
    let bundle = store.current();
    let mut cases_run = 0;
    let mut failures = Vec::new();
    for path in test_files() {
        let file = yaml(&path);
        let policy = file["policy"].as_str().expect("a test file names its policy");
        assert!(bundle.flags.values().any(|f| f.policy == policy), "{} tests {policy}, which no flag serves", path.display());
        for case in file["cases"].as_array().expect("a test file has cases") {
            let name = case["name"].as_str().unwrap();
            let result = bundle.pool.evaluate(policy, &case["input"], &EvalOptions::timeout(Duration::from_secs(5))).unwrap();
            cases_run += 1;
            let expect = &case["expect"];
            let decided = serde_json::json!({
                "decision": result.decision, "reason": result.reason,
                "payload": result.payload.map(Value::Object).unwrap_or(Value::Null),
            });
            let want_payload = expect.get("payload").cloned().unwrap_or(Value::Null);
            let ok = result.error.is_none()
                && expect["decision"] == decided["decision"]
                && expect["reason"] == decided["reason"]
                && (want_payload.is_null() || is_subset(&want_payload, &decided["payload"]));
            if !ok {
                failures.push(format!(
                    "{} / {name}: decided {decided}, error {:?}, want {expect}",
                    path.file_name().unwrap().to_string_lossy(),
                    result.error.map(|e| e.message)
                ));
            }
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
    assert!(cases_run >= 20, "only {cases_run} cases ran");
}

#[test]
fn every_flag_has_a_test_file_that_reaches_both_decisions() {
    let tested: BTreeSet<String> = test_files().iter().map(|p| yaml(p)["policy"].as_str().unwrap().to_owned()).collect();
    let mut served = BTreeSet::new();
    for entry in std::fs::read_dir(flags_dir()).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_some_and(|e| e == "sigil") {
            let stem = path.file_stem().unwrap().to_string_lossy().into_owned();
            served.insert(format!("flags.{stem}"));
        }
    }
    assert_eq!(tested, served, "every flag policy has exactly one test file");

    for path in test_files() {
        let decisions: BTreeSet<String> =
            yaml(&path)["cases"].as_array().unwrap().iter().map(|c| c["expect"]["decision"].as_str().unwrap().to_owned()).collect();
        assert_eq!(decisions.into_iter().collect::<Vec<_>>(), ["disable", "enable"], "{}", path.display());
    }
}

#[test]
fn file_names_follow_policy_names() {
    // The path-matches-name lint of policies/sigil.yaml wants flags/new_checkout.sigil
    // for flags.new_checkout, and the flag key is the same name with hyphens.
    for entry in std::fs::read_dir(flags_dir()).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_some_and(|e| e == "sigil") {
            let policy = format!("flags.{}", path.file_stem().unwrap().to_string_lossy());
            assert!(flags::key_of_policy(&policy).is_some(), "{policy} names no flag key");
        }
    }
}

#[test]
fn the_kind_file_matches_the_kind_the_service_compiles_against() {
    let on_disk = std::fs::read_to_string(Path::new(env!("CARGO_MANIFEST_DIR")).join("policies").join(kind::KIND_PATH)).unwrap();
    assert_eq!(on_disk, kind::schema(), "policies/feature_rollout.sigil is stale; run `featuregate export-kind`");
}
