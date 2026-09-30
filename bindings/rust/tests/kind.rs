//! The kind builder against Go: `schema()` is byte for byte what Go's
//! `Kind.Schema` writes for the same kind (the generator is the one the
//! TypeScript tests use), and what the example services' Go hosts exported.

mod common;
mod kinds;

use std::collections::BTreeMap;
use std::fs;
use std::process::Command;

use rstest::rstest;

#[rstest]
#[case::deploy_approval(kinds::deploy_approval(), "deploy_approval.sigil")]
#[case::access_grant(kinds::access_grant(), "access_grant.sigil")]
fn schema_equals_the_kind_file_the_go_host_exported(#[case] kind: sigil::Kind, #[case] file: &str) {
    let exported = fs::read_to_string(common::deploy_gates().join(file)).unwrap();
    assert_eq!(kind.schema(), exported);
}

#[test]
fn schema_equals_gos_kind_schema_for_the_same_kind() {
    let run = Command::new("go")
        .args(["run", "./bindings/typescript/test/testdata/gokinds"])
        .current_dir(common::root())
        .output()
        .expect("running go");
    assert!(run.status.success(), "go run gokinds failed:\n{}", String::from_utf8_lossy(&run.stderr));
    let golden: BTreeMap<String, String> = serde_json::from_slice(&run.stdout).unwrap();
    for (name, kind) in [
        ("AlertRouting", kinds::alert_routing()),
        ("Everything", kinds::everything()),
        ("Collecting", kinds::collecting()),
        ("Minimal", kinds::minimal()),
    ] {
        let want = golden.get(name).unwrap_or_else(|| panic!("gokinds printed no {name}"));
        assert_eq!(kind.schema(), want, "kind {name}");
    }
}

mod with_the_engine {
    use super::*;
    use common::{deploy_gates, err_of, inputs, json, sigil, sigil_files};
    use serde::Deserialize;
    use sigil::{CompileOptions, KindCompileOptions, SourceFile};

    #[rstest]
    #[case::alert_routing(kinds::alert_routing())]
    #[case::deploy_approval(kinds::deploy_approval())]
    #[case::access_grant(kinds::access_grant())]
    #[case::everything(kinds::everything())]
    #[case::collecting(kinds::collecting())]
    #[case::minimal(kinds::minimal())]
    fn the_engine_accepts_the_kind_file(#[case] kind: sigil::Kind) {
        assert_eq!(kind.check(&sigil()).unwrap(), []);
    }

    #[derive(Deserialize, PartialEq, Debug)]
    struct Bake {
        bake: String,
    }

    fn compile_options(policy: &str) -> KindCompileOptions {
        KindCompileOptions { compile: CompileOptions { policy: Some(policy.into()), ..Default::default() }, kind_file: None }
    }

    #[test]
    fn compile_uses_the_kinds_host_functions_and_reads_typed_results() {
        let s = sigil();
        let kind = kinds::deploy_approval();
        let (deny, _, approve) = kinds::deploy_decisions();
        let policy = kind.compile(&s, &sigil_files(&deploy_gates()), compile_options("payments.production")).unwrap();
        let result = policy.eval(&json(&deploy_gates().join("teams/payments/testdata/sre.json"))).unwrap();
        assert!(result.error.is_none(), "{:?}", result.error);
        assert_eq!(approve.matches::<Bake>(&result).unwrap(), Some(Bake { bake: "15m".into() }));
        assert!(approve.reason("payments_sre").is(&result).unwrap());
        assert!(!approve.reason("release_manager").is(&result).unwrap());
        assert_eq!(deny.matches::<serde_json::Value>(&result).unwrap(), None);
    }

    #[test]
    fn compile_adds_the_kind_file_when_the_files_dont_hold_it() {
        let s = sigil();
        let kind = kinds::deploy_approval();
        let all = sigil_files(&deploy_gates());
        let docs: Vec<SourceFile> =
            all.iter().filter(|f| f.path.starts_with("platform/deploy/") || f.path.starts_with("teams/")).cloned().collect();
        let a = kind.compile(&s, &docs, compile_options("payments.production")).unwrap();
        let b = kind.compile(&s, &all, compile_options("payments.production")).unwrap();
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        assert_eq!(a.eval(&input).unwrap(), b.eval(&input).unwrap());
    }

    #[test]
    fn compile_fails_on_a_stale_kind_file_and_takes_a_path_for_the_one_it_adds() {
        let s = sigil();
        let kind = kinds::deploy_approval();
        let mut all = sigil_files(&deploy_gates());
        let stale = all.iter_mut().find(|f| f.path == "deploy_approval.sigil").unwrap();
        stale.source = stale.source.replace("= 1h", "= 2h");
        let err = err_of(kind.compile(&s, &all, compile_options("payments.production")));
        assert!(err.to_string().contains("declares kind DeployApproval, but not as this program defines it"), "{err}");

        let docs: Vec<SourceFile> = sigil_files(&deploy_gates())
            .into_iter()
            .filter(|f| f.path.starts_with("platform/deploy/") || f.path.starts_with("teams/"))
            .collect();
        let options = KindCompileOptions { kind_file: Some("kinds/deploy.sigil".into()), ..compile_options("payments.production") };
        assert!(kind.compile(&s, &docs, options).is_ok());
    }

    #[test]
    fn a_collecting_kind_without_precedence_reads_with_match_all_and_refuses_matches() {
        let s = sigil();
        let kind = kinds::access_grant();
        let files: Vec<SourceFile> = sigil_files(&deploy_gates())
            .into_iter()
            .filter(|f| f.path.starts_with("platform/access/") || f.path.starts_with("access/"))
            .collect();
        let policy = kind.compile(&s, &files, compile_options("access.main")).unwrap();
        let (path, input) = inputs("access/testdata").into_iter().find(|(p, _)| p == "access/testdata/sre.json").unwrap();
        let result = policy.eval(&input).unwrap();
        assert!(result.error.is_none(), "{path}: {:?}", result.error);
        assert!(result.collect);
        let decisions = kinds::access_decisions();
        let granted: usize = decisions.iter().map(|d| d.match_all::<serde_json::Value>(&result).unwrap().len()).sum();
        assert_eq!(granted, result.outcome.len());
        assert!(granted > 0);
        let any = &decisions[1];
        let err = err_of(any.matches::<serde_json::Value>(&result));
        assert!(err.to_string().contains("match_all"), "{err}");
        let err = err_of(any.reason("oncall").is(&result));
        assert!(err.to_string().contains("collecting kind"), "{err}");
        for m in decisions.iter().flat_map(|d| d.match_all::<serde_json::Value>(&result).unwrap()) {
            assert!(m.policy.is_some());
        }
    }

    #[test]
    fn a_payload_that_doesnt_fit_the_type_asked_for_says_which_decision() {
        let s = sigil();
        let (_, _, approve) = kinds::deploy_decisions();
        let policy = kinds::deploy_approval().compile(&s, &sigil_files(&deploy_gates()), compile_options("payments.production")).unwrap();
        let result = policy.eval(&json(&deploy_gates().join("teams/payments/testdata/sre.json"))).unwrap();
        #[derive(Deserialize, Debug)]
        struct Wrong {
            #[allow(dead_code)]
            bake: u32,
        }
        let err = err_of(approve.matches::<Wrong>(&result));
        assert!(err.to_string().contains("payload of decision approve"), "{err}");
    }
}

mod builder_problems {
    use super::*;
    use serde_json::json;
    use sigil::{Decision, FnDecl, Kind, OutcomeRef, Type};

    fn ok() -> Decision {
        Decision::new("ok", ["yes"])
    }

    fn base(ok: &Decision) -> sigil::KindBuilder {
        Kind::builder("K").version(1).input("who", Type::string()).decisions([ok]).default_outcome(ok.reason("yes"))
    }

    fn problems(result: Result<Kind, sigil::Error>) -> String {
        match result {
            Ok(_) => panic!("the kind built"),
            Err(e) => e.to_string(),
        }
    }

    #[test]
    fn a_sound_kind_builds() {
        let ok = ok();
        assert!(base(&ok).build().is_ok());
    }

    #[rstest]
    #[case::version_zero(|ok: &Decision| Kind::builder("K").input("who", Type::string()).decisions([ok]).default_outcome(ok.reason("yes")), "version 0 isn't a whole number from 1")]
    #[case::accepts_above_version(|ok: &Decision| base(ok).accepts(2), "accepts 2 isn't from 1 to the version, 1")]
    #[case::bad_kind_name(|ok: &Decision| Kind::builder("1 K").version(1).decisions([ok]).default_outcome(ok.reason("yes")), "kind name \"1 K\" isn't an identifier")]
    #[case::no_default(|ok: &Decision| Kind::builder("K").version(1).decisions([ok]), "a `collect one` kind needs a default")]
    #[case::no_decisions(|_: &Decision| Kind::builder("K").version(1), "the kind declares no decisions")]
    #[case::duplicate_input(|ok: &Decision| base(ok).input("who", Type::int()), "input who is declared twice")]
    #[case::undeclared_default(|ok: &Decision| base(ok).default_outcome(sigil::Outcome::new("nope", "x")), "default nope.x: decision nope isn't one of the kind's")]
    #[case::default_without_payload_default(
        |_: &Decision| {
            let d = Decision::new("d", ["r"]).field("n", Type::int());
            Kind::builder("K").version(1).decisions([&d]).default_outcome(d.reason("r"))
        },
        "default d.r: payload field n has no default"
    )]
    #[case::conflict_in_collect_all(|ok: &Decision| Kind::builder("K").version(1).collect_all([ok]).conflict(ok.reason("yes")), "conflict is for a `collect one` kind")]
    #[case::optional_function_result(|ok: &Decision| base(ok).function("f", FnDecl::new([], Type::optional(Type::int()))), "function f: the result can't be optional")]
    #[case::two_types_one_name(
        |ok: &Decision| base(ok).input("a", Type::enumeration("E", ["x"])).input("b", Type::enumeration("E", ["y"])),
        "two different types are both named E"
    )]
    #[case::empty_enum(|ok: &Decision| base(ok).input("a", Type::enumeration("E", Vec::<String>::new())), "enum E declares no values")]
    #[case::bad_default_constant(
        |_: &Decision| {
            let d = Decision::new("d", ["r"]).field_default("n", Type::int(), json!("seven"));
            Kind::builder("K").version(1).decisions([&d]).default_outcome(d.reason("r"))
        },
        "decision d: field n: \"seven\" isn't a constant of type int"
    )]
    #[case::timestamp_default(
        |_: &Decision| {
            let d = Decision::new("d", ["r"]).field_default("t", Type::timestamp(), json!("2026-01-01T00:00:00Z"));
            Kind::builder("K").version(1).decisions([&d]).default_outcome(d.reason("r"))
        },
        "a timestamp field can't have a default"
    )]
    #[case::incomplete_reason_ranking(
        |_: &Decision| {
            let d = Decision::new("d", ["a", "b"]);
            Kind::builder("K").version(1).decisions([&d]).default_outcome(d.reason("a")).rank_outcomes([d.reason("a")])
        },
        "the reason ranking of d must name every reason of d once"
    )]
    #[case::short_exclusive_set(|ok: &Decision| base(ok).exclusive([OutcomeRef::from(ok)]), "an exclusive set names at least two outcomes")]
    #[case::partial_precedence(
        |_: &Decision| {
            let (a, b) = (Decision::new("a", ["r"]), Decision::new("b", ["r"]));
            Kind::builder("K").version(1).collect_all([&a, &b]).precedence([&a])
        },
        "precedence must list every decision of the kind once"
    )]
    fn a_broken_kind_fails_to_build_with_the_reason(#[case] build: fn(&Decision) -> sigil::KindBuilder, #[case] expected: &str) {
        let text = problems(build(&ok()).build());
        assert!(text.contains(expected), "{text}");
        assert!(text.starts_with("Kind::builder("), "{text}");
    }

    #[test]
    fn every_problem_is_listed_at_once() {
        let d = Decision::new("d", ["a", "a"]);
        let text = problems(Kind::builder("K").version(0).decisions([&d]).build());
        for expected in ["version 0", "declares reason a twice", "needs a default"] {
            assert!(text.contains(expected), "{text}");
        }
    }

    #[test]
    #[should_panic(expected = "did you mean \"unrouted\"")]
    fn an_undeclared_reason_panics_with_a_did_you_mean() {
        Decision::new("notify", ["routine", "unrouted"]).reason("unroutd");
    }

    #[test]
    fn try_reason_reports_instead_of_panicking() {
        let d = Decision::new("notify", ["routine", "unrouted"]);
        let err = d.try_reason("nope").unwrap_err();
        assert!(err.to_string().contains("notify declares: routine, unrouted"), "{err}");
        assert_eq!(d.try_reason("routine").unwrap().to_string(), "notify.routine");
    }
}
