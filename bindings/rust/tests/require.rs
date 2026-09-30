//! `compile`'s `require` with trusted files: the platform's guardrails come
//! from the host's own documents, and a team bundle can't omit, gate,
//! redefine or loosen them. The deploy-gates example's payments policy stands
//! in for a team bundle, `platform/deploy` for the platform.

mod common;
mod kinds;

use common::{deploy_gates, err_of, inputs, json, sigil, sigil_files};
use rstest::rstest;
use serde_json::Value;
use sigil::{CheckOptions, CompileOptions, CompileRequirement, ExplainOptions, KindCompileOptions, Policy, Sigil, SourceFile};

fn platform() -> Vec<SourceFile> {
    sigil_files(&deploy_gates()).into_iter().filter(|f| f.path.starts_with("platform/deploy/")).collect()
}

fn payments_file() -> SourceFile {
    sigil_files(&deploy_gates()).into_iter().find(|f| f.path == "teams/payments/production.sigil").unwrap()
}

/// The payments bundle with its source rewritten, and extra files.
fn team(rewrite: impl Fn(&str) -> String, extra: Vec<SourceFile>) -> Vec<SourceFile> {
    let mut f = payments_file();
    f.source = rewrite(&f.source);
    let mut files = vec![f];
    files.extend(extra);
    files
}

fn same(s: &str) -> String {
    s.to_string()
}

fn options(trusted: Option<Vec<SourceFile>>) -> KindCompileOptions {
    KindCompileOptions {
        compile: CompileOptions {
            policy: Some("payments.production".into()),
            require: vec![CompileRequirement::new("deploy.guardrails")],
            trusted_files: trusted.unwrap_or_default(),
            ..Default::default()
        },
        kind_file: None,
    }
}

fn compile(s: &Sigil, files: &[SourceFile], trusted: Option<Vec<SourceFile>>) -> Result<Policy, sigil::Error> {
    kinds::deploy_approval().compile(s, files, options(trusted))
}

fn sre() -> Value {
    json(&deploy_gates().join("teams/payments/testdata/sre.json"))
}

#[test]
fn a_bundle_that_invokes_the_trusted_guardrails_compiles_and_evaluates_as_before() {
    let s = sigil();
    let policy = compile(&s, &team(same, vec![]), Some(platform())).unwrap();
    let plain = kinds::deploy_approval()
        .compile(
            &s,
            &sigil_files(&deploy_gates()),
            KindCompileOptions {
                compile: CompileOptions { policy: Some("payments.production".into()), ..Default::default() },
                kind_file: None,
            },
        )
        .unwrap();
    let result = policy.eval(&sre()).unwrap();
    let (_, _, approve) = kinds::deploy_decisions();
    assert!(approve.reason("payments_sre").is(&result).unwrap());
    assert_eq!(result, plain.eval(&sre()).unwrap());
}

fn omit(s: &str) -> String {
    s.replace("use deploy.guardrails\n", "").replace("guardrails(min_soak: 4h)\n", "")
}

#[rstest]
#[case::omits_the_guardrails(team(omit, vec![]), "the policy doesn't compile, so nothing was compiled", "payments.production doesn't invoke deploy.guardrails")]
#[case::gates_the_guardrails(
    team(|s| s.replace("guardrails(min_soak: 4h)\n", "when environment == \"production\" {\n  guardrails(min_soak: 4h)\n}\n"), vec![]),
    "the policy doesn't compile, so nothing was compiled",
    "deploy.guardrails must be invoked unconditionally"
)]
#[case::passes_a_param_below_its_minimum(
    team(|s| s.replace("guardrails(min_soak: 4h)", "guardrails(min_soak: 1m)"), vec![]),
    "the policy doesn't compile, so nothing was compiled",
    "min_soak: 1m is below the minimum 1h"
)]
#[case::redefines_the_guardrails(
    team(same, vec![SourceFile::new("teams/payments/guardrails.sigil", "policy deploy.guardrails: DeployApproval@1\n")]),
    "doesn't check, so nothing was compiled",
    "policy deploy.guardrails is defined twice"
)]
fn a_bundle_that_breaks_the_guardrails_doesnt_compile(#[case] files: Vec<SourceFile>, #[case] message: &str, #[case] diagnostic: &str) {
    let err = err_of(compile(&sigil(), &files, Some(platform())));
    assert!(err.to_string().contains(message), "{err}");
    let messages: Vec<_> = err.diagnostics().iter().map(|d| d.message.as_str()).collect();
    assert!(messages.contains(&diagnostic), "{messages:?}");
    let at = err.diagnostics().iter().find(|d| d.message == diagnostic).unwrap();
    assert!(at.file.as_deref().unwrap().starts_with("teams/payments/"));
}

#[test]
fn the_required_policy_must_come_from_the_trusted_files() {
    let (guardrails, rest): (Vec<_>, Vec<_>) = platform().into_iter().partition(|f| f.path.ends_with("guardrails.sigil"));
    let err = err_of(compile(&sigil(), &team(same, guardrails), Some(rest)));
    let sigil::Error::Sigil(e) = &err else { panic!("{err:?}") };
    assert_eq!(e.message, "require[0]: deploy.guardrails isn't among the trusted files");
    assert!(err.diagnostics().iter().any(|d| d.message == "deploy.guardrails must come from the trusted files, but it's defined here"));
}

#[test]
fn a_required_policy_nobody_defines_fails() {
    let rest: Vec<_> = platform().into_iter().filter(|f| !f.path.ends_with("guardrails.sigil")).collect();
    let err = err_of(compile(&sigil(), &team(same, vec![]), Some(rest)));
    let sigil::Error::Sigil(e) = &err else { panic!("{err:?}") };
    assert_eq!(e.message, "require[0]: deploy.guardrails is required, but the trusted files define no policy deploy.guardrails");
}

#[test]
fn a_path_is_in_the_files_or_the_trusted_files_not_both() {
    let err = err_of(compile(&sigil(), &team(same, platform()), Some(platform())));
    let text = err.to_string();
    assert!(text.contains("is among both the files and the trusted files"), "{text}");
}

#[test]
fn without_trusted_files_any_policy_of_the_bundle_satisfies_a_requirement() {
    let s = sigil();
    let policy = compile(&s, &team(same, platform()), None).unwrap();
    assert_eq!(policy.name(), "payments.production");
    let err = err_of(compile(&s, &team(omit, platform()), None));
    assert!(err.diagnostics().iter().any(|d| d.message == "payments.production doesn't invoke deploy.guardrails"));
}

#[test]
fn the_kind_file_may_be_among_the_trusted_files() {
    let s = sigil();
    let mut trusted = vec![kinds::deploy_approval().file(None)];
    trusted.extend(platform());
    let policy = compile(&s, &team(same, vec![]), Some(trusted)).unwrap();
    let (_, _, approve) = kinds::deploy_decisions();
    #[derive(serde::Deserialize, PartialEq, Debug)]
    struct Bake {
        bake: String,
    }
    assert_eq!(approve.matches::<Bake>(&policy.eval(&sre()).unwrap()).unwrap(), Some(Bake { bake: "15m".into() }));
}

#[test]
fn a_stale_kind_file_among_the_trusted_files_fails() {
    let mut stale = kinds::deploy_approval().file(None);
    stale.source = stale.source.replace("= 1h", "= 2h");
    let mut trusted = vec![stale];
    trusted.extend(platform());
    let err = err_of(compile(&sigil(), &team(same, vec![]), Some(trusted)));
    assert!(err.to_string().contains("declares kind DeployApproval, but not as this program defines it"), "{err}");
}

#[test]
fn a_requirement_with_trusted_files_compiles_against_a_bare_kind_file() {
    let s = sigil();
    let mut files = vec![kinds::deploy_approval().file(None)];
    files.extend(team(same, vec![]));
    let options = CompileOptions {
        policy: Some("payments.production".into()),
        require: vec![CompileRequirement::new("deploy.guardrails")],
        trusted_files: platform(),
        ..Default::default()
    };
    assert!(s.compile(&files, options).is_ok());
}

#[test]
fn check_and_explain_read_trusted_files_too() {
    let s = sigil();
    let mut files = vec![kinds::deploy_approval().file(None)];
    files.extend(team(same, vec![]));
    let with_trusted = CheckOptions { trusted_files: platform(), ..Default::default() };
    assert!(s.check(&files, &with_trusted).unwrap().is_empty());
    let explained = s.explain(&files, &ExplainOptions { policy: Some("payments.production".into()), trusted_files: platform() }).unwrap();
    let mut all = files.clone();
    all.extend(platform());
    let direct = s.explain(&all, &ExplainOptions { policy: Some("payments.production".into()), ..Default::default() }).unwrap();
    assert_eq!(explained, direct);
}

#[test]
fn every_example_input_still_evaluates_through_the_trusted_guardrails() {
    let s = sigil();
    let policy = compile(&s, &team(same, vec![]), Some(platform())).unwrap();
    for (path, input) in inputs("teams/payments/testdata") {
        assert!(policy.eval(&input).unwrap().error.is_none(), "{path}");
    }
}
