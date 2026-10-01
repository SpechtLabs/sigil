//! Helpers shared by the integration tests: the module (compiled once per test
//! binary, it takes seconds), the repository's fixtures as virtual files, and
//! the stock `sigil` CLI to compare answers with.
#![allow(dead_code)]

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

use serde_json::Value;
use sigil::{Module, SourceFile};

/// The repository root.
pub fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../..").canonicalize().expect("the crate lives in bindings/rust of the repository")
}

/// The module under test, compiled once: the bundled one.
pub fn module() -> &'static Module {
    static MODULE: OnceLock<Module> = OnceLock::new();
    MODULE.get_or_init(|| Module::bundled().expect("compiling the bundled sigil.wasm"))
}

pub fn deploy_gates() -> PathBuf {
    root().join("examples/deploy-gates/policies")
}

pub fn alert_routing() -> PathBuf {
    root().join("examples/alert-routing/policies")
}

/// Every `.sigil` file below `dir`, paths relative to it and `/`-separated, in a stable order.
pub fn sigil_files(dir: &Path) -> Vec<SourceFile> {
    let mut files = Vec::new();
    walk(dir, dir, &mut files);
    files
}

fn walk(base: &Path, dir: &Path, out: &mut Vec<SourceFile>) {
    let mut entries: Vec<_> = fs::read_dir(dir).unwrap_or_else(|e| panic!("reading {}: {e}", dir.display())).map(|e| e.unwrap()).collect();
    entries.sort_by_key(|e| e.file_name());
    for entry in entries {
        let path = entry.path();
        if path.is_dir() {
            walk(base, &path, out);
        } else if path.extension().is_some_and(|e| e == "sigil") {
            let rel = path
                .strip_prefix(base)
                .unwrap()
                .components()
                .map(|c| c.as_os_str().to_string_lossy().into_owned())
                .collect::<Vec<_>>()
                .join("/");
            out.push(SourceFile::new(rel, fs::read_to_string(&path).unwrap()));
        }
    }
}

pub fn json(path: &Path) -> Value {
    serde_json::from_str(&fs::read_to_string(path).unwrap_or_else(|e| panic!("reading {}: {e}", path.display()))).unwrap()
}

/// Builds the stock CLI once per test binary, from this checkout.
pub fn cli_binary() -> &'static Path {
    static CLI: OnceLock<PathBuf> = OnceLock::new();
    CLI.get_or_init(|| {
        let dir = std::env::temp_dir().join(format!("sigil-rust-cli-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        let out = dir.join("sigil");
        let build =
            Command::new("go").args(["build", "-o"]).arg(&out).arg("./cmd/sigil").current_dir(root()).output().expect("running go build");
        assert!(build.status.success(), "go build ./cmd/sigil failed:\n{}", String::from_utf8_lossy(&build.stderr));
        out
    })
}

/// Runs the stock CLI with `-o json` in a fresh directory holding exactly the
/// files (plus extra files, such as a `sigil.yaml`), so its paths are the
/// virtual files' paths, and returns the parsed output.
pub fn cli(files: &[SourceFile], args: &[&str], extra: &[(&str, &str)]) -> Value {
    let dir = tempfile::tempdir().unwrap();
    for (path, source) in files.iter().map(|f| (f.path.as_str(), f.source.as_str())).chain(extra.iter().copied()) {
        let full = dir.path().join(path);
        fs::create_dir_all(full.parent().unwrap()).unwrap();
        fs::write(full, source).unwrap();
    }
    let run = Command::new(cli_binary())
        .args(args)
        .args(["-o", "json"])
        .args(files.iter().map(|f| f.path.as_str()))
        .current_dir(dir.path())
        .env("NO_COLOR", "1")
        .output()
        .expect("running sigil");
    let stdout = String::from_utf8_lossy(&run.stdout);
    assert!(!stdout.trim().is_empty(), "sigil {} printed nothing:\n{}", args.join(" "), String::from_utf8_lossy(&run.stderr));
    serde_json::from_str(&stdout).unwrap_or_else(|e| panic!("sigil {} printed invalid JSON ({e}):\n{stdout}", args.join(" ")))
}

/// A fresh instance of the shared module.
pub fn sigil() -> sigil::Sigil {
    sigil::Sigil::new(module()).expect("instantiating the module")
}

/// `cmd/sigil/command/check/testdata`: the CLI's own fixtures.
pub fn check_testdata() -> PathBuf {
    root().join("cmd/sigil/command/check/testdata")
}

/// The deploy-gates kind file as the CLI's check fixtures hold it.
pub fn deploy_kind_file() -> SourceFile {
    SourceFile::new("deploy_approval.sigil", fs::read_to_string(check_testdata().join("deploy_approval.sigil")).unwrap())
}

/// The error of a result that must be one, without needing `Debug` on the ok side.
pub fn err_of<T>(result: Result<T, sigil::Error>) -> sigil::Error {
    match result {
        Ok(_) => panic!("expected an error, got Ok"),
        Err(e) => e,
    }
}

/// `split`, as the deploy-gates host implements it.
pub fn split() -> sigil::HostFunction {
    sigil::host_fn(|args| {
        let (Some(s), Some(sep)) = (args.first().and_then(Value::as_str), args.get(1).and_then(Value::as_str)) else {
            return Err("split takes two strings".into());
        };
        Ok(serde_json::json!(s.split(sep).collect::<Vec<_>>()))
    })
}

/// The stubs that answer `split` for an input the way the real function does.
pub fn split_stub(input: &Value) -> std::collections::BTreeMap<String, sigil::Stub> {
    let regions = input["service"]["labels"]["regions"].as_str().unwrap_or("");
    let stub = sigil::Stub {
        calls: vec![sigil::StubCall {
            args: vec![regions.into(), ",".into()],
            returns: Some(serde_json::json!(regions.split(',').collect::<Vec<_>>())),
            error: None,
        }],
        ..Default::default()
    };
    [("split".to_string(), stub)].into_iter().collect()
}

/// Every `*.json` in `dir` below the deploy-gates policies, sorted, as (relative path, input).
pub fn inputs(dir: &str) -> Vec<(String, Value)> {
    let mut names: Vec<_> = fs::read_dir(deploy_gates().join(dir))
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .filter(|n| n.ends_with(".json"))
        .collect();
    names.sort();
    names.into_iter().map(|n| (format!("{dir}/{n}"), json(&deploy_gates().join(dir).join(&n)))).collect()
}

/// A policy of the deploy-gates example, compiled with `split`.
pub fn payments(sigil: &sigil::Sigil) -> sigil::Policy {
    let files = sigil_files(&deploy_gates());
    sigil
        .compile(
            &files,
            sigil::CompileOptions {
                policy: Some("payments.production".into()),
                functions: [("split".to_string(), split())].into(),
                ..Default::default()
            },
        )
        .unwrap()
}

/// The `Minimal` kind's file, as the TypeScript tests' coverage kind defines it.
pub fn minimal_kind_file() -> SourceFile {
    SourceFile::new(
        "minimal.sigil",
        "kind Minimal version 1\n\ninput who: string\n\nfn upper(string) -> string\n\ndecision ok {\n  reason: yes\n}\n\ncollect one\n\ndefault ok(reason: yes)\n",
    )
}
