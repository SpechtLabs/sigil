//! The Sigil API against the real module (mise run wasm-build), with parity
//! checks against the stock CLI's `-o json` records for the same files.

mod common;

use std::collections::BTreeMap;
use std::fs;
use std::sync::{Arc, Mutex};

use common::{check_testdata, cli, deploy_gates, deploy_kind_file, err_of, inputs, json, payments, sigil, sigil_files, split, split_stub};
use serde_json::{Value, json};
use sigil::{
    CheckOptions, CompileOptions, CompileRequirement, EvalResult, ExplainOptions, FailureKind, FormatOptions, LintLevel, Requirement,
    Severity, SourceFile, Stub, host_fn,
};

/// The deploy-gates example's `sigil.yaml`, minus the kinds (they're among the
/// files): its requirements and lint levels, for the module and the CLI.
const CONFIG: &str = "require:
  - policy: deploy.guardrails
    trusted: [platform/deploy]
    roots: [\"payments.*\", \"checkout.*\"]
  - policy: access.guardrails
    trusted: [platform/access]
    roots: [access.main]
lints:
  gated-deny: error
  gated-assert: error
  path-matches-name: error
";

fn deploy_options() -> CheckOptions {
    CheckOptions {
        require: vec![
            Requirement {
                policy: "deploy.guardrails".into(),
                trusted: vec!["platform/deploy".into()],
                roots: vec!["payments.*".into(), "checkout.*".into()],
            },
            Requirement { policy: "access.guardrails".into(), trusted: vec!["platform/access".into()], roots: vec!["access.main".into()] },
        ],
        lints: [("gated-deny", LintLevel::Error), ("gated-assert", LintLevel::Error), ("path-matches-name", LintLevel::Error)]
            .into_iter()
            .map(|(k, v)| (k.to_string(), v))
            .collect(),
        ..Default::default()
    }
}

fn compile_opts(policy: &str) -> CompileOptions {
    CompileOptions { policy: Some(policy.into()), ..Default::default() }
}

fn as_json<T: serde::Serialize>(v: &T) -> Value {
    serde_json::to_value(v).unwrap()
}

fn bad_file() -> SourceFile {
    SourceFile::new("teams/bad.sigil", fs::read_to_string(check_testdata().join("errors/bad.sigil")).unwrap())
}

#[test]
fn version_reports_a_wasip1_build() {
    let v = common::sigil().version().unwrap();
    assert_eq!(v.platform, "wasip1/wasm");
    assert!(v.go_version.starts_with("go1."), "{v:?}");
    assert!(!v.version.is_empty());
}

mod check {
    use super::*;

    #[test]
    fn the_deploy_gates_example_passes_like_the_cli_with_its_config() {
        let files = sigil_files(&deploy_gates());
        let diagnostics = sigil().check(&files, &deploy_options()).unwrap();
        assert!(diagnostics.is_empty(), "{diagnostics:?}");
        assert_eq!(as_json(&diagnostics), cli(&files, &["check"], &[("sigil.yaml", CONFIG)]));
    }

    #[test]
    fn lint_findings_equal_the_clis() {
        let mut files = vec![deploy_kind_file()];
        files.extend(sigil_files(&check_testdata().join("lints")));
        let diagnostics = sigil().check(&files, &CheckOptions::default()).unwrap();
        assert!(!diagnostics.is_empty());
        assert!(diagnostics.iter().all(|d| d.severity == Severity::Warning));
        assert_eq!(as_json(&diagnostics), cli(&files, &["check"], &[]));
    }

    #[test]
    fn a_lint_level_raises_a_warning_to_an_error() {
        let mut files = vec![deploy_kind_file()];
        files.extend(sigil_files(&check_testdata().join("lints")));
        let options = CheckOptions {
            lints: [("unused-let".to_string(), LintLevel::Error), ("unused-import".to_string(), LintLevel::Off)].into(),
            ..Default::default()
        };
        let diagnostics = sigil().check(&files, &options).unwrap();
        let levels: Vec<_> = diagnostics.iter().filter(|d| d.lint.as_deref() == Some("unused-let")).map(|d| d.severity).collect();
        assert_eq!(levels, [Severity::Error]);
        assert!(!diagnostics.iter().any(|d| d.lint.as_deref() == Some("unused-import")));
    }

    #[test]
    fn errors_equal_the_clis_with_positions_in_the_virtual_paths() {
        let files = vec![deploy_kind_file(), bad_file()];
        let diagnostics = sigil().check(&files, &CheckOptions::default()).unwrap();
        assert!(
            diagnostics.iter().any(|d| d.severity == Severity::Error && d.file.as_deref() == Some("teams/bad.sigil") && d.line.is_some())
        );
        assert_eq!(as_json(&diagnostics), cli(&files, &["check"], &[]));
    }

    #[test]
    fn a_requirement_a_policy_breaks_is_an_error_diagnostic_like_the_clis() {
        let files = sigil_files(&deploy_gates());
        let options = CheckOptions {
            require: vec![Requirement {
                policy: "deploy.production".into(),
                trusted: vec!["platform/deploy".into()],
                roots: vec!["payments.*".into()],
            }],
            ..Default::default()
        };
        let diagnostics = sigil().check(&files, &options).unwrap();
        assert!(diagnostics.iter().any(|d| d.severity == Severity::Error && d.file.as_deref() == Some("teams/payments/production.sigil")));
        let config = "require:\n  - policy: deploy.production\n    trusted: [platform/deploy]\n    roots: [\"payments.*\"]\n";
        assert_eq!(as_json(&diagnostics), cli(&files, &["check"], &[("sigil.yaml", config)]));
    }

    #[test]
    fn a_requirement_that_cant_hold_is_an_error_as_a_configuration_error_is_for_the_cli() {
        let files = sigil_files(&deploy_gates());
        let options = CheckOptions {
            require: vec![Requirement {
                policy: "deploy.guardrails".into(),
                trusted: vec!["platform/access".into()],
                roots: vec!["payments.*".into()],
            }],
            ..Default::default()
        };
        let err = err_of(sigil().check(&files, &options));
        assert!(
            err.to_string().contains(
                "require[0]: deploy.guardrails must come from platform/access, but it's defined at platform/deploy/guardrails.sigil"
            ),
            "{err}"
        );
    }

    #[test]
    fn policies_narrows_the_check() {
        let mut files = sigil_files(&deploy_gates());
        files.push(SourceFile::new(
            "teams/bad/production.sigil",
            "policy bad.production: DeployApproval@1\n\nwhen nope {\n  approve(reason: payments_sre)\n}\n",
        ));
        let s = sigil();
        assert!(s.check(&files, &CheckOptions::default()).unwrap().iter().any(|d| d.severity == Severity::Error));
        let narrowed = CheckOptions { policies: vec!["payments.*".into()], ..Default::default() };
        assert!(s.check(&files, &narrowed).unwrap().is_empty());
    }

    #[test]
    fn an_unknown_lint_is_an_error() {
        let options = CheckOptions { lints: [("no-such-lint".to_string(), LintLevel::Error)].into(), ..Default::default() };
        let err = err_of(sigil().check(&[deploy_kind_file()], &options));
        assert!(err.to_string().contains("no-such-lint"), "{err}");
    }
}

mod compile {
    use super::*;

    #[test]
    fn returns_a_policy_with_its_name_and_warnings() {
        let s = sigil();
        let policy = payments(&s);
        assert_eq!(policy.name(), "payments.production");
        assert!(policy.handle() > 0);
        assert!(policy.diagnostics().is_empty());
    }

    #[test]
    fn fails_with_the_diagnostics_of_files_that_dont_compile() {
        let err = err_of(sigil().compile(&[deploy_kind_file(), bad_file()], CompileOptions::default()));
        let sigil::Error::Sigil(err) = err else { panic!("{err:?}") };
        assert!(!err.message.is_empty());
        assert!(!err.diagnostics.is_empty());
        assert_eq!(err.diagnostics[0].file.as_deref(), Some("teams/bad.sigil"));
    }

    #[test]
    fn fails_for_a_policy_the_files_dont_hold() {
        let files = sigil_files(&deploy_gates());
        assert!(matches!(sigil().compile(&files, compile_opts("nope.production")), Err(sigil::Error::Sigil(_))));
    }

    #[test]
    fn fails_for_a_host_function_the_kind_doesnt_declare() {
        let files = sigil_files(&deploy_gates());
        let options = CompileOptions { functions: [("splt".to_string(), split())].into(), ..compile_opts("payments.production") };
        let sigil::Error::Sigil(err) = err_of(sigil().compile(&files, options)) else { panic!("not a SigilError") };
        assert!(err.message.contains("has no host function splt"), "{}", err.message);
        assert!(err.help.unwrap().contains("split"));
    }
}

mod eval {
    use super::*;

    #[test]
    fn payments_production_equals_the_cli_with_stubs() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        for (path, input) in inputs("teams/payments/testdata") {
            let stubs = split_stub(&input);
            let policy = s.compile(&files, CompileOptions { stubs: stubs.clone(), ..compile_opts("payments.production") }).unwrap();
            let result = policy.eval(&input).unwrap();
            let input_text = input.to_string();
            let stubs_text = serde_json::to_string(&stubs).unwrap();
            let expected = cli(
                &files,
                &["eval", "--policy", "payments.production", "--input", &path, "--stubs", "stubs.json"],
                &[(&path, &input_text), ("stubs.json", &stubs_text)],
            );
            assert_eq!(as_json(&result), expected, "{path}");
        }
    }

    #[test]
    fn access_main_equals_the_cli() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let policy = s.compile(&files, compile_opts("access.main")).unwrap();
        for (path, input) in inputs("access/testdata") {
            let input_text = input.to_string();
            let expected = cli(&files, &["eval", "--policy", "access.main", "--input", &path], &[(&path, &input_text)]);
            assert_eq!(as_json(&policy.eval(&input).unwrap()), expected, "{path}");
        }
    }

    #[test]
    fn a_failing_assert_returns_the_fallback_with_the_failure() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let policy = s.compile(&files, compile_opts("access.main")).unwrap();
        let result = policy.eval(&json(&deploy_gates().join("access/testdata/auditor-sre.json"))).unwrap();
        let error = result.error.expect("the assert fails");
        assert_eq!(error.kind, FailureKind::Assertion);
        assert!(!error.asserts.is_empty());
        assert_eq!(error.phase, Some(sigil::AssertPhase::Outcome));
        assert_eq!(error.asserts[0].policy, "access.guardrails");
    }

    #[test]
    fn host_functions_give_the_stubs_answers() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let real = payments(&s);
        for (_, input) in inputs("teams/payments/testdata") {
            let stubbed = s.compile(&files, CompileOptions { stubs: split_stub(&input), ..compile_opts("payments.production") }).unwrap();
            assert_eq!(real.eval(&input).unwrap(), stubbed.eval(&input).unwrap());
        }
    }

    #[test]
    fn a_host_function_receives_the_sigil_arguments() {
        let calls: Arc<Mutex<Vec<Vec<Value>>>> = Arc::default();
        let seen = Arc::clone(&calls);
        let inner = split();
        let recording = host_fn(move |args| {
            seen.lock().unwrap().push(args.clone());
            inner(args)
        });
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let policy = s
            .compile(&files, CompileOptions { functions: [("split".to_string(), recording)].into(), ..compile_opts("payments.production") })
            .unwrap();
        policy.eval(&json(&deploy_gates().join("teams/payments/testdata/sre.json"))).unwrap();
        assert!(calls.lock().unwrap().contains(&vec![json!("eu,us"), json!(",")]));
    }

    fn sre() -> Value {
        json(&deploy_gates().join("teams/payments/testdata/sre.json"))
    }

    fn compile_with(s: &sigil::Sigil, function: sigil::HostFunction) -> sigil::Policy {
        let files = sigil_files(&deploy_gates());
        s.compile(&files, CompileOptions { functions: [("split".to_string(), function)].into(), ..compile_opts("payments.production") })
            .unwrap()
    }

    #[test]
    fn an_erroring_host_function_fails_the_evaluation_at_runtime() {
        let s = sigil();
        let policy = compile_with(&s, host_fn(|_| Err("region directory unavailable".into())));
        let result = policy.eval(&sre()).unwrap();
        let error = result.error.unwrap();
        assert_eq!(error.kind, FailureKind::Runtime);
        assert!(error.message.contains("host function split failed: region directory unavailable"), "{}", error.message);
        // The failure's fallback is the kind's default.
        assert_eq!((result.decision.as_deref(), result.reason.as_deref()), (Some("deny"), Some("no_rule_matched")));
    }

    #[test]
    fn a_panicking_host_function_fails_the_evaluation_and_the_instance_lives() {
        let s = sigil();
        let policy = compile_with(&s, host_fn(|_| panic!("boom")));
        let result = policy.eval(&sre()).unwrap();
        let error = result.error.unwrap();
        assert_eq!(error.kind, FailureKind::Runtime);
        assert!(error.message.contains("host function split panicked: boom"), "{}", error.message);
        assert!(s.stopped().is_none());
        assert_eq!(s.version().unwrap().platform, "wasip1/wasm");
    }

    #[test]
    fn a_host_function_returning_the_wrong_type_fails_the_call_with_the_kinds_help() {
        let s = sigil();
        let policy = compile_with(&s, host_fn(|_| Ok(json!(42))));
        let error = policy.eval(&sre()).unwrap().error.unwrap();
        assert_eq!(error.kind, FailureKind::Runtime);
        assert!(error.message.contains("the host returned result: expected a list<string>, found a number"), "{}", error.message);
    }

    #[test]
    fn a_stub_error_fails_the_call_like_the_clis() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let stubs: BTreeMap<String, Stub> =
            [("split".to_string(), Stub { error: Some("region directory unavailable".into()), ..Default::default() })].into();
        let policy = s.compile(&files, CompileOptions { stubs: stubs.clone(), ..compile_opts("payments.production") }).unwrap();
        let path = "teams/payments/testdata/sre.json";
        let input = sre();
        let (input_text, stubs_text) = (input.to_string(), serde_json::to_string(&stubs).unwrap());
        let expected = cli(
            &files,
            &["eval", "--policy", "payments.production", "--input", path, "--stubs", "stubs.json"],
            &[(path, &input_text), ("stubs.json", &stubs_text)],
        );
        assert_eq!(as_json(&policy.eval(&input).unwrap()), expected);
    }

    #[test]
    fn a_stub_replaces_an_implementation_of_the_same_name() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let options = CompileOptions {
            functions: [("split".to_string(), host_fn(|_| Err("the implementation ran".into())))].into(),
            stubs: [("split".to_string(), Stub { returns: Some(json!(["eu", "us"])), ..Default::default() })].into(),
            ..compile_opts("payments.production")
        };
        let policy = s.compile(&files, options).unwrap();
        assert!(policy.eval(&sre()).unwrap().error.is_none());
    }

    #[test]
    fn a_host_function_with_neither_an_implementation_nor_a_stub_fails_like_the_cli() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let policy = s.compile(&files, compile_opts("payments.production")).unwrap();
        let path = "teams/payments/testdata/sre.json";
        let input = sre();
        let result = policy.eval(&input).unwrap();
        assert_eq!(result.error.as_ref().unwrap().kind, FailureKind::Runtime);
        let input_text = input.to_string();
        let expected = cli(&files, &["eval", "--policy", "payments.production", "--input", path], &[(path, &input_text)]);
        assert_eq!(as_json(&result), expected);
    }

    #[test]
    fn a_host_function_cant_call_back_into_the_instance() {
        let files = sigil_files(&deploy_gates());
        let s = Arc::new(sigil());
        let inner = Arc::new(s.compile(&files, compile_opts("access.main")).unwrap());
        let inner_input = json(&deploy_gates().join("access/testdata/sre.json"));
        let nested = Arc::clone(&inner);
        let real = split();
        let nested_input = inner_input.clone();
        let calling_back = host_fn(move |args| {
            nested.eval(&nested_input).map_err(|e| e.to_string())?;
            real(args)
        });
        let policy = s
            .compile(
                &files,
                CompileOptions { functions: [("split".to_string(), calling_back)].into(), ..compile_opts("payments.production") },
            )
            .unwrap();
        let result = policy.eval(&sre()).unwrap();
        assert!(result.error.unwrap().message.contains("busy"));
        // Both work afterwards.
        assert!(inner.eval(&inner_input).unwrap().error.is_none());
    }

    #[test]
    fn an_input_that_doesnt_fit_the_kind_is_a_sigil_error_and_the_instance_lives() {
        let s = sigil();
        let policy = payments(&s);
        let err = err_of(policy.eval(&json!({"release": {"soak": "a while"}})));
        assert!(matches!(err, sigil::Error::Sigil(_)), "{err:?}");
        assert!(!err.is_stopped());
        assert!(s.stopped().is_none());
    }

    #[test]
    fn a_serializable_struct_is_an_input() {
        #[derive(serde::Serialize)]
        struct Input {
            release: Value,
            service: Value,
            actor: Value,
            environment: String,
        }
        let value = sre();
        let typed = Input {
            release: value["release"].clone(),
            service: value["service"].clone(),
            actor: value["actor"].clone(),
            environment: value["environment"].as_str().unwrap().to_string(),
        };
        let s = sigil();
        let policy = payments(&s);
        assert_eq!(policy.eval(&typed).unwrap(), policy.eval(&value).unwrap());
    }

    #[test]
    fn an_input_serde_cant_encode_fails_before_the_module_is_touched() {
        struct Broken;
        impl serde::Serialize for Broken {
            fn serialize<S: serde::Serializer>(&self, _: S) -> Result<S::Ok, S::Error> {
                Err(serde::ser::Error::custom("no can do"))
            }
        }
        let s = sigil();
        let policy = payments(&s);
        let err = err_of(policy.eval(&Broken));
        assert!(err.to_string().contains("no can do"), "{err}");
        assert!(s.stopped().is_none());
    }
}

mod explain {
    use super::*;

    #[test]
    fn a_policys_explanation_equals_the_clis() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let explained = payments(&s).explain().unwrap();
        let expected = cli(&files, &["explain", "--policy", "payments.production"], &[]);
        assert_eq!(as_json(&explained), expected[0]);
    }

    #[test]
    fn explaining_files_equals_the_clis_for_one_policy_or_all() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let one = s.explain(&files, &ExplainOptions { policy: Some("access.main".into()), ..Default::default() }).unwrap();
        assert_eq!(as_json(&one), cli(&files, &["explain", "--policy", "access.main"], &[]));
        assert_eq!(as_json(&s.explain(&files, &ExplainOptions::default()).unwrap()), cli(&files, &["explain"], &[]));
    }
}

mod format {
    use super::*;

    #[test]
    fn leaves_the_canonical_example_sources_unchanged() {
        let s = sigil();
        for f in sigil_files(&deploy_gates()) {
            let options = FormatOptions { path: Some(f.path.clone()) };
            assert_eq!(s.format(&f.source, &options).unwrap(), f.source, "{}", f.path);
        }
    }

    #[test]
    fn formats_a_messy_source() {
        let s = sigil();
        let messy = "policy   a.b :DeployApproval@1\nwhen   true {approve(reason:payments_sre)}\n";
        let formatted = s.format(messy, &FormatOptions::default()).unwrap();
        assert_ne!(formatted, messy);
        assert_eq!(s.format(&formatted, &FormatOptions::default()).unwrap(), formatted);
    }

    #[test]
    fn a_source_that_doesnt_parse_is_an_error_with_its_diagnostics() {
        let err = err_of(sigil().format("policy x {", &FormatOptions { path: Some("x.sigil".into()) }));
        let sigil::Error::Sigil(err) = err else { panic!("not a SigilError") };
        assert!(!err.diagnostics.is_empty());
        assert_eq!(err.diagnostics[0].file.as_deref(), Some("x.sigil"));
    }
}

mod test_files {
    use super::*;
    use common::{TestWorkspace, alert_routing, test_testdata};
    use rstest::rstest;
    use sigil::{TestOptions, TestResult};

    /// The CLI's test fixtures: the access kind and policy, and a directory of
    /// test files per situation.
    fn fixtures(prefixes: &[&str]) -> TestWorkspace {
        TestWorkspace::read(&test_testdata()).pick(prefixes)
    }

    fn run(ws: &TestWorkspace, filter: Option<&str>) -> Vec<TestResult> {
        let options = TestOptions { data: ws.data.clone(), run: filter.map(String::from), trusted_files: ws.trusted.clone() };
        sigil().test(&ws.files, &ws.tests, &options).unwrap()
    }

    #[rstest]
    #[case::the_deploy_gates_example_with_a_host_function_no_stub_answers(TestWorkspace::read(&deploy_gates()), None)]
    #[case::the_alert_routing_example(TestWorkspace::read(&alert_routing()), None)]
    #[case::the_alert_routing_example_with_the_platforms_policies_trusted(TestWorkspace::read(&alert_routing()).trust(&["platform"]), None)]
    #[case::no_files(TestWorkspace { files: vec![], ..fixtures(&["access"]) }, None)]
    #[case::passing_cases(fixtures(&["access.sigil", "access"]), None)]
    #[case::failing_cases(fixtures(&["access.sigil", "access/main.sigil", "failing"]), None)]
    #[case::the_cases_run_selects(fixtures(&["access.sigil", "access/main.sigil", "failing"]), Some("^wrong"))]
    #[case::no_case_run_selects(fixtures(&["access.sigil", "access/main.sigil", "failing"]), Some("nothing"))]
    #[case::an_invalid_test_file(fixtures(&["access.sigil", "access/main.sigil", "invalid"]), None)]
    #[case::a_test_file_that_isnt_yaml(fixtures(&["access.sigil", "access/main.sigil", "badyaml"]), None)]
    #[case::a_policy_the_files_dont_define(fixtures(&["access.sigil", "access/main.sigil", "nopolicy"]), None)]
    #[case::a_policy_that_doesnt_compile(fixtures(&["access.sigil", "broken"]), None)]
    #[case::file_and_case_stubs(fixtures(&["access.sigil", "access/main.sigil", "stubbed"]), None)]
    #[case::stubs_that_fail(fixtures(&["access.sigil", "access/main.sigil", "stubfail"]), None)]
    #[case::stubs_that_dont_fit(fixtures(&["access.sigil", "access/main.sigil", "badstubs"]), None)]
    fn results_equal_the_clis(#[case] ws: TestWorkspace, #[case] filter: Option<&str>) {
        let results = run(&ws, filter);
        assert_eq!(results.len(), ws.tests.len());
        assert_eq!(as_json(&results), ws.cli(filter));
    }

    #[test]
    fn an_input_file_the_request_doesnt_hold_fails_its_case_like_a_missing_file_for_the_cli() {
        let ws = TestWorkspace { data: vec![], ..fixtures(&["access.sigil", "access"]) };
        let results = run(&ws, None);
        let missing = results[0].cases.iter().find(|c| c.error.is_some()).expect("a case with an input_file");
        assert!(!missing.passed);
        assert!(missing.error.as_deref().unwrap().contains("couldn't be read (input_file is relative to the test file)"), "{missing:?}");
        assert_eq!(as_json(&results), ws.cli(None));
    }

    #[test]
    fn reports_passing_failing_and_broken_test_files_apart() {
        let results = run(&fixtures(&["access.sigil", "access", "failing", "broken"]), None);
        let files: Vec<_> = results.iter().map(|r| r.file.as_str()).collect();
        assert_eq!(files, ["access/main_test.yaml", "broken/main_test.yaml", "failing/main_test.yaml"]);
        let [pass, broken, failing] = &results[..] else { unreachable!() };
        assert!(pass.cases.iter().all(|c| c.passed), "{pass:?}");
        assert!(broken.error.is_some() && broken.cases.is_empty(), "{broken:?}");
        assert!(failing.error.is_none());
        assert!(failing.cases.iter().any(|c| !c.passed && !c.failures.is_empty()), "{failing:?}");
    }

    #[test]
    fn results_survive_a_json_round_trip() {
        let results = run(&fixtures(&["access.sigil", "access", "failing", "broken"]), None);
        let back: Vec<TestResult> = serde_json::from_value(as_json(&results)).unwrap();
        assert_eq!(back, results);
    }

    #[rstest]
    #[case::no_test_files(vec![], TestOptions::default(), "the request holds no test files")]
    #[case::a_test_file_not_named_like_one(
        vec![SourceFile::new("access/main.yaml", "policy: access.main\ncases: []\n")],
        TestOptions::default(),
        "the test file access/main.yaml isn't named like one"
    )]
    #[case::a_test_file_without_a_path(vec![SourceFile::new("", "policy: access.main\ncases: []\n")], TestOptions::default(), "test file 1 has no path")]
    #[case::a_run_that_isnt_a_regular_expression(
        vec![SourceFile::new("access/main_test.yaml", "policy: access.main\ncases: []\n")],
        TestOptions { run: Some("(".into()), ..Default::default() },
        "run isn't a valid regular expression"
    )]
    #[case::a_path_given_twice_with_two_sources(
        vec![SourceFile::new("access/main_test.yaml", "policy: access.main\ncases: []\n")],
        TestOptions { data: vec![SourceFile::new("access/main_test.yaml", "{}")], ..Default::default() },
        "access/main_test.yaml is given twice, with two sources"
    )]
    #[case::a_path_among_the_files_and_the_trusted_files(
        vec![SourceFile::new("access/main_test.yaml", "policy: access.main\ncases: []\n")],
        TestOptions { trusted_files: fixtures(&["access/main.sigil"]).files, ..Default::default() },
        "access/main.sigil is among both the files and the trusted files"
    )]
    fn a_request_the_cli_couldnt_get_is_an_error(#[case] tests: Vec<SourceFile>, #[case] options: TestOptions, #[case] message: &str) {
        let files = fixtures(&["access.sigil", "access/main.sigil"]).files;
        let sigil::Error::Sigil(err) = err_of(sigil().test(&files, &tests, &options)) else { panic!("not a SigilError") };
        assert!(err.message.contains(message), "{err:?}");
        assert!(err.help.is_some(), "{err:?}");
    }
}

mod release {
    use super::*;

    #[test]
    fn releasing_frees_the_handle_and_dropping_does_too() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let first = s.compile(&files, compile_opts("access.main")).unwrap();
        let handle = first.handle();
        first.release().unwrap();
        // A dropped policy is released just the same; both leave the instance usable.
        drop(s.compile(&files, compile_opts("access.main")).unwrap());
        let again = s.compile(&files, compile_opts("access.main")).unwrap();
        assert!(again.eval(&json(&deploy_gates().join("access/testdata/sre.json"))).unwrap().error.is_none());
        assert!(handle > 0);
    }

    #[test]
    fn a_policy_outlives_its_sigil() {
        let files = sigil_files(&deploy_gates());
        let policy = {
            let s = sigil();
            s.compile(&files, compile_opts("access.main")).unwrap()
        };
        assert!(policy.eval(&json(&deploy_gates().join("access/testdata/sre.json"))).unwrap().error.is_none());
    }
}

mod memory {
    use super::*;

    #[test]
    fn stays_flat_across_10k_evaluations_with_host_function_calls() {
        let files = sigil_files(&deploy_gates());
        let s = sigil();
        let input = json(&deploy_gates().join("teams/payments/testdata/sre.json"));
        let run = |n: usize| {
            for _ in 0..n {
                let policy = s
                    .compile(
                        &files,
                        CompileOptions { functions: [("split".to_string(), split())].into(), ..compile_opts("payments.production") },
                    )
                    .unwrap();
                for _ in 0..100 {
                    policy.eval(&input).unwrap();
                }
            }
        };
        run(10); // warm up: the Go heap settles at its working size
        let before = s.memory_size().unwrap();
        run(100);
        let after = s.memory_size().unwrap();
        assert!(before > 0);
        // Allow the Go heap some slack, not growth per evaluation.
        assert!(after.saturating_sub(before) <= 4 * 1024 * 1024, "memory grew from {before} to {after}");
    }
}

mod deep_nesting {
    use super::*;

    /// A policy nested 100k deep ran the module out of stack before the parser
    /// limited nesting; with the limit it's a diagnostic. Either way the
    /// instance must end up usable or clearly stopped, never half-alive.
    #[test]
    fn is_rejected_with_a_diagnostic_or_stops_the_instance_for_good() {
        let s = sigil();
        let n = 100_000;
        let policy = format!("policy deep.nesting: Minimal@1\n\nwhen {}true{} {{\n  ok(reason: yes)\n}}\n", "(".repeat(n), ")".repeat(n));
        let minimal = crate::common::minimal_kind_file();
        let files = [minimal, SourceFile::new("deep.sigil", policy)];
        match s.check(&files, &CheckOptions::default()) {
            Ok(diagnostics) => {
                assert!(diagnostics.iter().any(|d| d.severity == Severity::Error));
                assert!(s.stopped().is_none());
                assert_eq!(s.version().unwrap().platform, "wasip1/wasm");
            }
            Err(err) => {
                assert!(err.is_stopped(), "{err}");
                assert!(s.stopped().is_some());
                assert!(matches!(s.version(), Err(sigil::Error::Stopped(_))));
            }
        }
    }
}

#[test]
fn compile_requirement_is_constructible_for_the_require_option() {
    assert_eq!(CompileRequirement::new("a.b").policy, "a.b");
}

#[test]
fn eval_results_survive_a_json_round_trip() {
    let s = sigil();
    let result = payments(&s).eval(&json(&deploy_gates().join("teams/payments/testdata/sre.json"))).unwrap();
    let back: EvalResult = serde_json::from_value(as_json(&result)).unwrap();
    assert_eq!(back, result);
}
