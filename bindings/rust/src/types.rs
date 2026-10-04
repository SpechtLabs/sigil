//! The records the module returns, typed field for field after the JSON the
//! stock `sigil` CLI prints with `-o json`, and the options of each op. The
//! module produces the records with the same Go code as the CLI, so a field
//! that is an `Option` or an empty `Vec` here is one the CLI leaves out when
//! it's empty.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

/// A host function: called with the Sigil arguments (durations as `1h30m`,
/// timestamps as RFC 3339 strings, enum values as their names, structs as
/// objects), returns the result, or an error message that fails the policy's
/// call with a runtime error quoting it. See [`host_fn`].
///
/// A host function runs synchronously, inside the evaluation, on the thread
/// that called [`crate::Policy::eval`]. It can't call back into the instance
/// that runs it, and the epoch deadline can't interrupt it while it runs:
/// the deadline is checked when it returns. Give one that may block its own
/// timeout.
pub type HostFunction = Arc<dyn Fn(Vec<Value>) -> Result<Value, String> + Send + Sync>;

/// Wraps a closure as a [`HostFunction`].
pub fn host_fn<F>(f: F) -> HostFunction
where
    F: Fn(Vec<Value>) -> Result<Value, String> + Send + Sync + 'static,
{
    Arc::new(f)
}

/// A typed host function: a closure over deserializable arguments that returns
/// a serializable result or an error message, of 0 to 4 arguments. Built with
/// [`host_fn_typed`]; implemented for the closures themselves, which is why
/// you never name it.
///
/// The `Args` parameter is a marker, the tuple of the argument types, so one
/// function can be implemented for every arity.
pub trait TypedHostFn<Args>: Send + Sync + 'static {
    /// Decodes `args`, calls the closure and encodes its result.
    fn call(&self, args: Vec<Value>) -> Result<Value, String>;
}

/// Wraps a typed closure as a [`HostFunction`]: the arguments are deserialized
/// with serde into the closure's parameter types and the result is serialized,
/// so the closure reads like the function it declares:
///
/// ```
/// use sigil::host_fn_typed;
///
/// let split = host_fn_typed(|s: String, sep: String| -> Result<Vec<String>, String> {
///     Ok(s.split(&sep).map(str::to_string).collect())
/// });
/// # let _ = split;
/// ```
///
/// A call with the wrong number of arguments, or an argument of the wrong type,
/// fails the call with an error message that says which: `expected 2 arguments,
/// got 1` or `argument 2: invalid type: integer `5`, expected a string`. That
/// message is all this adapter returns; the engine turns it into the failed
/// evaluation's runtime error, [`crate::EvalFailure::message`], by adding the
/// policy position and the function: `checkout/alerts.sigil:10:46: host function
/// split failed: argument 2: invalid type: integer `5`, expected a string`.
/// The closure returns `Result<R, E>` with `E: Display`.
pub fn host_fn_typed<Args, F>(f: F) -> HostFunction
where
    F: TypedHostFn<Args>,
    Args: 'static,
{
    Arc::new(move |args| f.call(args))
}

fn decode<T: serde::de::DeserializeOwned>(value: Value, position: usize) -> Result<T, String> {
    serde_json::from_value(value).map_err(|err| format!("argument {position}: {err}"))
}

macro_rules! typed_host_fn {
    ($count:expr; $($arg:ident),*) => {
        impl<Func, Ret, Err, $($arg),*> TypedHostFn<($($arg,)*)> for Func
        where
            Func: Fn($($arg),*) -> Result<Ret, Err> + Send + Sync + 'static,
            Ret: Serialize,
            Err: std::fmt::Display,
            $($arg: serde::de::DeserializeOwned,)*
        {
            #[allow(non_snake_case, unused_mut, unused_variables, unused_assignments)]
            fn call(&self, args: Vec<Value>) -> Result<Value, String> {
                if args.len() != $count {
                    return Err(format!("expected {} argument{}, got {}", $count, if $count == 1 { "" } else { "s" }, args.len()));
                }
                let mut args = args.into_iter();
                let mut position = 0;
                $(
                    position += 1;
                    let $arg = decode::<$arg>(args.next().expect("the count was checked"), position)?;
                )*
                let result = self($($arg),*).map_err(|err| err.to_string())?;
                serde_json::to_value(result).map_err(|err| format!("the result can't be encoded as JSON: {err}"))
            }
        }
    };
}

typed_host_fn!(0;);
typed_host_fn!(1; A1);
typed_host_fn!(2; A1, A2);
typed_host_fn!(3; A1, A2, A3);
typed_host_fn!(4; A1, A2, A3, A4);

/// One virtual file. Paths appear in diagnostics and positions exactly as the
/// CLI prints them for the same relative path.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SourceFile {
    pub path: String,
    pub source: String,
}

impl SourceFile {
    pub fn new(path: impl Into<String>, source: impl Into<String>) -> Self {
        Self { path: path.into(), source: source.into() }
    }
}

/// How a lint is reported, as a configuration file spells it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum LintLevel {
    Off,
    Warn,
    Error,
}

/// A requirement on one policy, like a `require` entry in `sigil.yaml`: the
/// policy must be trusted, or reachable only from the given roots.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Requirement {
    pub policy: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub trusted: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub roots: Vec<String>,
}

/// A policy a compiled policy must invoke, see [`CompileOptions::require`].
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct CompileRequirement {
    pub policy: String,
}

impl CompileRequirement {
    pub fn new(policy: impl Into<String>) -> Self {
        Self { policy: policy.into() }
    }
}

/// How serious a [`Diagnostic`] is.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Severity {
    Error,
    Warning,
}

/// One error or lint finding, as `sigil check -o json` prints it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Diagnostic {
    pub severity: Severity,
    /// The lint's name, for a lint finding.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lint: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub file: Option<String>,
    /// The document the diagnostic is in, when that's known.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub document: Option<String>,
    pub message: String,
    /// How to fix it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub help: Option<String>,
    /// From 1; `None` when the diagnostic has no position.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub line: Option<u32>,
    /// In characters, from 1.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub column: Option<u32>,
}

impl Diagnostic {
    /// The diagnostic on one line, as `file:line:column: severity: message`.
    pub fn render(&self) -> String {
        let severity = match self.severity {
            Severity::Error => "error",
            Severity::Warning => "warning",
        };
        let mut out = String::new();
        if let Some(file) = &self.file {
            out.push_str(file);
            if let Some(line) = self.line {
                out.push_str(&format!(":{line}"));
                if let Some(column) = self.column {
                    out.push_str(&format!(":{column}"));
                }
            }
            out.push_str(": ");
        }
        out.push_str(&format!("{severity}: {}", self.message));
        if let Some(help) = &self.help {
            out.push_str(&format!(" (help: {help})"));
        }
        out
    }
}

/// What `sigil version -o json` reports about the module's build.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VersionInfo {
    /// The release version, or `devel` for a build that isn't one.
    pub version: String,
    pub commit: String,
    pub commit_time: String,
    pub dirty: bool,
    pub go_version: String,
    /// `wasip1/wasm`.
    pub platform: String,
}

/// A stand-in for one host function, in the format of a test file's `stubs:`:
/// a fixed result, a fixed error, or results for particular arguments.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Stub {
    /// The result of a call no entry of `calls` matches.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub returns: Option<Value>,
    /// The message a call no entry of `calls` matches fails with.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    /// Results for particular args, matched in order; the first match wins.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub calls: Vec<StubCall>,
}

/// One entry of a [`Stub`]'s `calls`: exactly one of `returns` and `error`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct StubCall {
    /// The args the entry answers, one per param, compared as values.
    pub args: Vec<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub returns: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// One candidate or outcome entry of an evaluation.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EvalEntry {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub payload: Option<Map<String, Value>>,
    pub decision: String,
    pub reason: String,
    /// The policy whose rule produced it; `None` for the default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub policy: Option<String>,
    /// The rule's position, `file:line:column`; `None` for the default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub position: Option<String>,
    /// The invocations it was reached through, outermost first.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub chain: Vec<String>,
    /// The `when` conditions that held, for a candidate of a winning decision.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub conditions: Vec<String>,
    /// In the outcome the host acts on.
    #[serde(default, skip_serializing_if = "is_false")]
    pub outcome: bool,
}

/// One failing assert.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct FailedAssert {
    /// The assert's name.
    pub reason: String,
    /// The policy the assert is in.
    pub policy: String,
    /// Its position, after the invocations that reached it.
    pub position: String,
    /// The runtime error its condition raised.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cause: Option<String>,
    /// What to do about the cause, when it knows better than the failure's help.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub help: Option<String>,
    /// For an outcome assert, the candidates that formed the outcome it read.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub outcome: Vec<EvalEntry>,
}

/// What failed in an evaluation.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum FailureKind {
    Assertion,
    Conflict,
    Runtime,
    /// The evaluation ran past its timeout (not a policy bug).
    Canceled,
}

/// Which asserts failed.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum AssertPhase {
    Input,
    Outcome,
}

/// Why an evaluation didn't produce an outcome.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EvalFailure {
    pub kind: FailureKind,
    /// For an assertion failure: whether the input asserts or the outcome asserts failed.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub phase: Option<AssertPhase>,
    pub message: String,
    pub help: String,
    /// The failing asserts, for an assertion failure.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub asserts: Vec<FailedAssert>,
    /// The conflicting candidates, for a conflict.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub candidates: Vec<EvalEntry>,
}

/// One evaluation, as `sigil eval -o json` prints it. A failed evaluation
/// still returns a result: `error` says why, and the outcome is the kind's
/// fallback (its default, or its conflict outcome after a conflict).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EvalResult {
    /// For a kind that returns one decision, the outcome's payload.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub payload: Option<Map<String, Value>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<EvalFailure>,
    /// The root policy.
    pub policy: String,
    /// For a kind that returns one decision, the outcome's decision.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub decision: Option<String>,
    /// For a kind that returns one decision, the outcome's reason.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
    /// What the host acts on.
    pub outcome: Vec<EvalEntry>,
    /// Every candidate the rules produced, winners first.
    pub trace: Vec<EvalEntry>,
    /// The kind collects every candidate.
    #[serde(default, skip_serializing_if = "is_false")]
    pub collect: bool,
    /// How the kind's results read; set by [`crate::Kind::compile`].
    #[serde(skip)]
    pub(crate) shape: Option<crate::kind::ResultShape>,
}

/// One rule or assert of an explanation.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ExplainEntry {
    /// `decision` or `assert`.
    pub kind: String,
    /// The decision a rule returns; `None` for an assert.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub decision: Option<String>,
    /// A rule's reason, or an assert's name.
    pub reason: String,
    /// `input` or `outcome`, for an assert.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub phase: Option<AssertPhase>,
    /// `policy:line`, outermost call first, the rule last.
    #[serde(default)]
    pub chain: Vec<String>,
    /// Every `when` on the way, outermost first.
    #[serde(default)]
    pub conditions: Vec<String>,
    /// An assert's own condition.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check: Option<String>,
    /// A rule's payload arguments, as `name = expression`.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub payload: Vec<String>,
}

/// One policy flattened, as `sigil explain -o json` prints it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Explanation {
    pub policy: String,
    /// The policy itself and every one it invokes.
    pub policies: u32,
    /// Every module those policies use.
    pub modules: u32,
    /// Every rule, then every assert.
    pub rules: Vec<ExplainEntry>,
}

/// One test file's run, as `sigil test -o json` prints it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TestResult {
    /// The test file's path.
    pub file: String,
    /// The policy the file tests; empty when the file couldn't be read as a
    /// test file.
    pub policy: String,
    /// Why none of the file's cases ran: it isn't a valid test file, or its
    /// policy doesn't compile. `cases` is empty then.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    /// The cases [`TestOptions::run`] selects, in file order.
    #[serde(default)]
    pub cases: Vec<TestCaseResult>,
}

/// One test case's run.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TestCaseResult {
    pub name: String,
    /// Why the case couldn't run: its input can't be read or doesn't fit the kind.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    /// How the evaluation differs from what the case expects.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub failures: Vec<String>,
    /// The case's line in its test file, from 1.
    pub line: u32,
    pub passed: bool,
}

/// Options of [`crate::Sigil::check`].
#[derive(Debug, Clone, Default, Serialize)]
pub struct CheckOptions {
    /// Name patterns of the policies to check, like `sigil check`'s
    /// arguments; every policy without them.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub policies: Vec<String>,
    /// Policies every checked policy must invoke unconditionally, like
    /// `require:` in `sigil.yaml`. `trusted` paths may point into `trusted_files`.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub require: Vec<Requirement>,
    /// Documents read as trusted, like the files a Go host passes to `policy.From`.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub trusted_files: Vec<SourceFile>,
    /// Overrides each named lint's default level, like `lints:` in `sigil.yaml`.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub lints: BTreeMap<String, LintLevel>,
}

/// Options of [`crate::Sigil::compile`].
#[derive(Clone, Default)]
pub struct CompileOptions {
    /// The policy to compile; may be left out when the files hold exactly one.
    pub policy: Option<String>,
    /// Policies the compiled policy must invoke unconditionally at its top
    /// level, like Go's `policy.Require(name, policy.From(trusted))`. With
    /// `trusted_files`, a required policy must be defined there, and a policy
    /// of the files that omits, gates or redefines it, or passes a param out
    /// of its bounds, fails to compile. Without `trusted_files`, any policy of
    /// the files satisfies it.
    pub require: Vec<CompileRequirement>,
    /// The documents a required policy is read from: the host's own, which the
    /// files (say, a team's bundle) can't replace. A path is in `files` or
    /// here, not both; trust comes from the list, not the path.
    pub trusted_files: Vec<SourceFile>,
    /// Stand-ins for host functions, as a test file's `stubs:` gives them. A
    /// stub replaces an implementation in `functions` of the same name.
    pub stubs: BTreeMap<String, Stub>,
    /// Implementations of the kind's host functions.
    pub functions: std::collections::HashMap<String, HostFunction>,
}

impl std::fmt::Debug for CompileOptions {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let mut functions: Vec<_> = self.functions.keys().collect();
        functions.sort();
        f.debug_struct("CompileOptions")
            .field("policy", &self.policy)
            .field("require", &self.require)
            .field("trusted_files", &self.trusted_files.iter().map(|f| &f.path).collect::<Vec<_>>())
            .field("stubs", &self.stubs.keys().collect::<Vec<_>>())
            .field("functions", &functions)
            .finish()
    }
}

/// Options of [`crate::Policy::eval_with`].
#[derive(Debug, Clone, Default)]
pub struct EvalOptions {
    /// The ABI's `timeout_ms`: the evaluation stops at its next check after
    /// this, with a `canceled` failure. Rounded up to whole milliseconds.
    pub timeout: Option<Duration>,
    /// How long past `timeout` the call may run before epoch interruption
    /// kills it, which stops the instance. [`crate::Limits::grace`] when `None`.
    pub grace: Option<Duration>,
    /// Stops the call after this much fuel, which stops the instance. The
    /// module must be built with [`ModuleConfig::fuel`].
    ///
    /// [`ModuleConfig::fuel`]: crate::ModuleConfig::fuel
    pub fuel: Option<u64>,
}

impl EvalOptions {
    pub fn timeout(timeout: Duration) -> Self {
        Self { timeout: Some(timeout), ..Self::default() }
    }
}

/// Options of [`crate::Sigil::explain`].
#[derive(Debug, Clone, Default, Serialize)]
pub struct ExplainOptions {
    /// A name pattern of the policies to explain; every policy without it.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub policy: Option<String>,
    /// Documents read as trusted, as for [`CompileOptions::trusted_files`].
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub trusted_files: Vec<SourceFile>,
}

/// Options of [`crate::Sigil::format`].
#[derive(Debug, Clone, Default)]
pub struct FormatOptions {
    /// The file's path, for the diagnostics of a source that doesn't parse.
    pub path: Option<String>,
}

/// Options of [`crate::Sigil::test`].
#[derive(Debug, Clone, Default)]
pub struct TestOptions {
    /// The files a case's `input_file` names. A path is relative to the test
    /// file's, as on disk: `testdata/owner.json` from
    /// `checkout/alerts_test.yaml` is `checkout/testdata/owner.json`.
    pub data: Vec<SourceFile>,
    /// Runs only the cases whose names this Go regular expression matches,
    /// like `sigil test --run`.
    pub run: Option<String>,
    /// Documents read as trusted, as for [`CompileOptions::trusted_files`]:
    /// the files below a configuration file's `trusted` paths, for the CLI.
    pub trusted_files: Vec<SourceFile>,
}

fn is_false(b: &bool) -> bool {
    !*b
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;
    use serde_json::json;

    fn call<Args: 'static>(f: impl TypedHostFn<Args>, args: Vec<Value>) -> Result<Value, String> {
        host_fn_typed(f)(args)
    }

    #[test]
    fn typed_host_functions_decode_their_arguments_and_encode_the_result() {
        assert_eq!(call(|| -> Result<u32, String> { Ok(7) }, vec![]), Ok(json!(7)));
        assert_eq!(call(|a: String| -> Result<String, String> { Ok(a.to_uppercase()) }, vec![json!("x")]), Ok(json!("X")));
        assert_eq!(call(|a: i64, b: i64| -> Result<i64, String> { Ok(a + b) }, vec![json!(1), json!(2)]), Ok(json!(3)));
        assert_eq!(
            call(
                |a: bool, b: Vec<u8>, c: Option<String>| -> Result<usize, String> {
                    Ok(b.len() + usize::from(a) + c.map_or(0, |c| c.len()))
                },
                vec![json!(true), json!([1, 2]), json!("ab")]
            ),
            Ok(json!(5))
        );
        assert_eq!(
            call(
                |a: String, b: String, c: String, d: String| -> Result<String, String> { Ok(format!("{a}{b}{c}{d}")) },
                vec![json!("a"), json!("b"), json!("c"), json!("d")]
            ),
            Ok(json!("abcd"))
        );
        assert_eq!(call(|v: Value| -> Result<Value, std::convert::Infallible> { Ok(v) }, vec![json!({"k": [1]})]), Ok(json!({"k": [1]})));
    }

    #[rstest]
    #[case(vec![], "expected 1 argument, got 0")]
    #[case(vec![json!("a"), json!("b")], "expected 1 argument, got 2")]
    #[case(vec![json!(5)], "argument 1: invalid type: integer `5`, expected a string")]
    #[case(vec![json!(null)], "argument 1: invalid type: null, expected a string")]
    fn a_wrong_arity_or_type_names_the_argument(#[case] args: Vec<Value>, #[case] want: &str) {
        assert_eq!(call(|s: String| -> Result<String, String> { Ok(s) }, args), Err(want.to_string()));
    }

    #[test]
    fn a_later_argument_is_named_by_position_and_zero_arguments_say_so() {
        let err = call(|_: String, _: u8| -> Result<(), String> { Ok(()) }, vec![json!("a"), json!(300)]).unwrap_err();
        assert!(err.starts_with("argument 2: "), "{err}");
        assert_eq!(call(|| -> Result<(), String> { Ok(()) }, vec![json!(1)]), Err("expected 0 arguments, got 1".into()));
        assert_eq!(call(|_: u8, _: u8| -> Result<(), String> { Ok(()) }, vec![json!(1)]), Err("expected 2 arguments, got 1".into()));
    }

    #[test]
    fn an_error_is_its_display_and_an_unserializable_result_is_reported() {
        assert_eq!(
            call(|| -> Result<(), std::fmt::Error> { Err(std::fmt::Error) }, vec![]),
            Err("an error occurred when formatting an argument".into())
        );
        struct NoJson;
        impl Serialize for NoJson {
            fn serialize<S: serde::Serializer>(&self, _: S) -> Result<S::Ok, S::Error> {
                Err(serde::ser::Error::custom("nope"))
            }
        }
        let err = call(|| -> Result<NoJson, String> { Ok(NoJson) }, vec![]).unwrap_err();
        assert!(err.contains("can't be encoded as JSON: nope"), "{err}");
    }

    #[test]
    fn a_diagnostic_renders_on_one_line() {
        let d = Diagnostic {
            severity: Severity::Error,
            lint: None,
            file: Some("a.sigil".into()),
            document: None,
            message: "unknown field".into(),
            help: Some("did you mean x?".into()),
            line: Some(2),
            column: Some(12),
        };
        assert_eq!(d.render(), "a.sigil:2:12: error: unknown field (help: did you mean x?)");
        let bare = Diagnostic { file: None, help: None, line: None, column: None, severity: Severity::Warning, ..d.clone() };
        assert_eq!(bare.render(), "warning: unknown field");
        let no_column = Diagnostic { column: None, help: None, ..d };
        assert_eq!(no_column.render(), "a.sigil:2: error: unknown field");
    }

    #[test]
    fn records_read_what_the_cli_leaves_out_as_empty() {
        let entry: EvalEntry = serde_json::from_value(json!({"decision": "d", "reason": "r"})).unwrap();
        assert!(entry.payload.is_none() && entry.chain.is_empty() && !entry.outcome);
        assert_eq!(serde_json::to_value(&entry).unwrap(), json!({"decision": "d", "reason": "r"}));
    }

    #[test]
    fn the_version_record_uses_the_clis_camel_case() {
        let v: VersionInfo = serde_json::from_value(json!({
            "version": "devel", "commit": "c", "commitTime": "t", "dirty": true, "goVersion": "go1", "platform": "wasip1/wasm"
        }))
        .unwrap();
        assert_eq!((v.commit_time.as_str(), v.go_version.as_str()), ("t", "go1"));
        assert_eq!(serde_json::to_value(&v).unwrap()["commitTime"], "t");
    }

    #[test]
    fn options_serialize_only_what_is_set() {
        assert_eq!(serde_json::to_value(CheckOptions::default()).unwrap(), json!({}));
        let options = CheckOptions {
            policies: vec!["a.*".into()],
            lints: [("unused-let".to_string(), LintLevel::Error)].into(),
            require: vec![Requirement { policy: "p".into(), trusted: vec!["t".into()], roots: vec![] }],
            trusted_files: vec![SourceFile::new("p.sigil", "x")],
        };
        assert_eq!(
            serde_json::to_value(options).unwrap(),
            json!({
                "policies": ["a.*"],
                "require": [{"policy": "p", "trusted": ["t"]}],
                "trusted_files": [{"path": "p.sigil", "source": "x"}],
                "lints": {"unused-let": "error"},
            })
        );
        assert_eq!(serde_json::to_value(ExplainOptions::default()).unwrap(), json!({}));
    }

    #[test]
    fn stubs_serialize_in_the_format_of_a_test_files_stubs() {
        let stub = Stub {
            calls: vec![StubCall { args: vec![json!("a"), json!(",")], returns: Some(json!(["a"])), error: None }],
            error: Some("down".into()),
            ..Default::default()
        };
        assert_eq!(serde_json::to_value(stub).unwrap(), json!({"error": "down", "calls": [{"args": ["a", ","], "returns": ["a"]}]}));
    }

    #[test]
    fn compile_options_debug_lists_names_not_closures() {
        let options = CompileOptions { functions: [("split".to_string(), host_fn(|_| Ok(Value::Null)))].into(), ..Default::default() };
        let shown = format!("{options:?}");
        assert!(shown.contains("\"split\""), "{shown}");
    }

    #[test]
    fn eval_options_timeout_sets_only_the_timeout() {
        let o = EvalOptions::timeout(Duration::from_millis(5));
        assert_eq!((o.timeout, o.grace, o.fuel), (Some(Duration::from_millis(5)), None, None));
    }
}
