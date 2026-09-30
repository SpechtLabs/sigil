//! A kind defined in Rust, the twin of Go's `policy.NewKind` and TypeScript's
//! `defineKind`: the inputs a policy reads, the decisions it may construct,
//! the host functions it may call. [`Kind::schema`] writes it as a kind file,
//! byte for byte what Go's `Kind.Schema` writes for the same kind, so a
//! repository can check the file in and the CLI can type-check policies
//! against it.
//!
//! ```
//! use serde_json::json;
//! use sigil::kind::{Decision, Kind, Type};
//!
//! let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));
//! let kind = Kind::builder("Routing")
//!     .version(1)
//!     .input("team", Type::string())
//!     .decisions([&notify])
//!     .default_outcome(notify.reason("unrouted"))
//!     .build()
//!     .unwrap();
//! assert!(kind.schema().starts_with("kind Routing version 1\n"));
//! ```

mod constant;
mod decision;
mod ty;

use std::collections::HashMap;
use std::sync::Arc;

pub use decision::{Decision, Field, Matched, Outcome, OutcomeRef, ResultShape};
pub use ty::{EnumDef, StructDef, Type};

use crate::error::{Error, SigilError};
use crate::sigil::{Policy, Sigil};
use crate::types::{CheckOptions, CompileOptions, Diagnostic, HostFunction, SourceFile};

/// A host function's declaration: its parameter and result types, and
/// optionally its implementation.
///
/// The implementation runs synchronously inside evaluations of policies the
/// kind compiles; leave it out to only declare the signature, for stubs or
/// another host.
#[derive(Clone)]
pub struct FnDecl {
    pub params: Vec<Type>,
    pub result: Type,
    pub implementation: Option<HostFunction>,
}

impl FnDecl {
    /// `fn split(string, string) -> list<string>` in a kind file.
    pub fn new(params: impl IntoIterator<Item = Type>, result: Type) -> Self {
        Self { params: params.into_iter().collect(), result, implementation: None }
    }

    /// Adds the implementation, see [`crate::host_fn`].
    pub fn implement(mut self, implementation: HostFunction) -> Self {
        self.implementation = Some(implementation);
        self
    }
}

/// The options of [`Kind::compile`]: [`CompileOptions`], with the kind's host
/// functions filled in.
#[derive(Clone, Default, Debug)]
pub struct KindCompileOptions {
    /// `functions` here are used instead of, or in addition to, the kind's
    /// implementations.
    pub compile: CompileOptions,
    /// The path of the kind file added to the files when they don't hold it;
    /// `<snake_case name>.sigil` by default.
    pub kind_file: Option<String>,
}

/// A kind: the contract policies are checked against. Build one with
/// [`Kind::builder`], once, at startup.
#[derive(Clone)]
pub struct Kind {
    name: String,
    version: u32,
    source: Arc<str>,
    functions: HashMap<String, HostFunction>,
    shape: ResultShape,
}

/// What [`Kind::builder`] collects; [`KindBuilder::build`] checks it.
#[derive(Clone)]
pub struct KindBuilder {
    name: String,
    version: u32,
    accepts: Option<u32>,
    inputs: Vec<(String, Type)>,
    enums: Vec<Type>,
    functions: Vec<(String, FnDecl)>,
    decisions: Vec<Decision>,
    collect: bool,
    precedence: Option<Vec<String>>,
    reason_precedence: Vec<Vec<Outcome>>,
    exclusive: Vec<Vec<OutcomeRef>>,
    default: Option<Outcome>,
    conflict: Option<Outcome>,
    /// What went wrong while building, for `build` to report together.
    problems: Vec<String>,
}

impl Kind {
    /// Starts a kind.
    pub fn builder(name: impl Into<String>) -> KindBuilder {
        KindBuilder {
            name: name.into(),
            version: 0,
            accepts: None,
            inputs: Vec::new(),
            enums: Vec::new(),
            functions: Vec::new(),
            decisions: Vec::new(),
            collect: false,
            precedence: None,
            reason_precedence: Vec::new(),
            exclusive: Vec::new(),
            default: None,
            conflict: None,
            problems: Vec::new(),
        }
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    /// The kind version.
    pub fn version(&self) -> u32 {
        self.version
    }

    /// The kind as a kind file, in `sigil fmt`'s canonical style: byte for
    /// byte what Go's `Kind.Schema` writes for the same kind. Check it in (see
    /// [`Kind::file`]) so the CLI and other hosts can type-check policies.
    pub fn schema(&self) -> &str {
        &self.source
    }

    /// The kind file as a virtual file: `alert_routing.sigil` for
    /// `AlertRouting`, unless `path` says otherwise.
    pub fn file(&self, path: Option<&str>) -> SourceFile {
        SourceFile::new(path.map_or_else(|| format!("{}.sigil", snake_case(&self.name)), str::to_string), self.source.to_string())
    }

    /// The implementations of the kind's host functions.
    pub fn functions(&self) -> HashMap<String, HostFunction> {
        self.functions.clone()
    }

    /// Checks the kind file with the real engine: every rule of the model that
    /// the builder leaves to it, such as reserved words or a cycle of struct
    /// types. Empty means the kind is sound.
    pub fn check(&self, sigil: &Sigil) -> Result<Vec<Diagnostic>, Error> {
        sigil.check(&[self.file(None)], &CheckOptions::default())
    }

    /// Compiles one policy of the files against this kind. The files, or the
    /// trusted files, may hold the kind file; if neither does, it's added to
    /// the files, and if one holds a kind file that differs from
    /// [`Kind::schema`], the compile fails, which catches a stale export. The
    /// kind's host function implementations are passed along, and the policy's
    /// results read through the kind's decisions.
    pub fn compile(&self, sigil: &Sigil, files: &[SourceFile], options: KindCompileOptions) -> Result<Policy, Error> {
        let KindCompileOptions { mut compile, kind_file } = options;
        let all = self.with_kind_file(files, &compile.trusted_files, kind_file.as_deref())?;
        let mut functions = self.functions.clone();
        functions.extend(std::mem::take(&mut compile.functions));
        compile.functions = functions;
        Ok(sigil.compile(&all, compile)?.with_shape(self.shape))
    }

    fn with_kind_file(&self, files: &[SourceFile], trusted: &[SourceFile], path: Option<&str>) -> Result<Vec<SourceFile>, Error> {
        let mut found = false;
        for f in files.iter().chain(trusted) {
            if kind_name_of(&f.source).is_some_and(|n| n == self.name) {
                if *f.source != *self.source {
                    return Err(Error::sigil(
                        format!("{} declares kind {}, but not as this program defines it", f.path, self.name),
                        "the file is stale: export the kind again, or drop the file and let compile add the current one",
                    ));
                }
                found = true;
            }
        }
        if found {
            return Ok(files.to_vec());
        }
        let mut all = vec![self.file(path)];
        all.extend_from_slice(files);
        Ok(all)
    }
}

impl std::fmt::Debug for Kind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Kind").field("name", &self.name).field("version", &self.version).finish_non_exhaustive()
    }
}

impl KindBuilder {
    /// The contract version, from 1; every change bumps it.
    pub fn version(mut self, version: u32) -> Self {
        self.version = version;
        self
    }

    /// The oldest version a policy may pin with `Kind@N`, from 1 to the
    /// version; every version when left out.
    pub fn accepts(mut self, accepts: u32) -> Self {
        self.accepts = Some(accepts);
        self
    }

    /// An input, `input alert: Alert`, in declaration order.
    pub fn input(mut self, name: impl Into<String>, ty: Type) -> Self {
        let name = name.into();
        if self.inputs.iter().any(|(n, _)| *n == name) {
            self.problems.push(format!("input {name} is declared twice"));
        }
        self.inputs.push((name, ty));
        self
    }

    /// Declares an enum even though no input, function or payload uses it,
    /// like Go's `WithEnum` for an enum nothing reaches. They print after the
    /// ones something uses.
    pub fn enumeration(mut self, ty: Type) -> Self {
        if matches!(ty, Type::Enum(_)) {
            self.enums.push(ty);
        } else {
            self.problems.push(format!("enumeration({ty}): not an enum"));
        }
        self
    }

    /// A host function, in declaration order: `fn split(string, string) -> list<string>`.
    /// A result can't be optional.
    pub fn function(mut self, name: impl Into<String>, decl: FnDecl) -> Self {
        let name = name.into();
        if self.functions.iter().any(|(n, _)| *n == name) {
            self.problems.push(format!("function {name} is declared twice"));
        }
        self.functions.push((name, decl));
        self
    }

    /// The decisions of a `collect one` kind, highest precedence first: the
    /// outcome is the one candidate of the highest decision that fired, or the
    /// default.
    pub fn decisions<'a>(mut self, decisions: impl IntoIterator<Item = &'a Decision>) -> Self {
        if !self.decisions.is_empty() {
            self.problems.push("the kind sets both decisions and collect".into());
        }
        self.decisions.extend(decisions.into_iter().cloned());
        self.collect = false;
        self
    }

    /// The decisions of a `collect all` kind, where every decision that fired
    /// applies; read results with [`Decision::match_all`].
    pub fn collect_all<'a>(mut self, decisions: impl IntoIterator<Item = &'a Decision>) -> Self {
        if !self.decisions.is_empty() {
            self.problems.push("the kind sets both decisions and collect".into());
        }
        self.decisions.extend(decisions.into_iter().cloned());
        self.collect = true;
        self
    }

    /// Ranks a collecting kind's decisions, which makes its outcome the top
    /// rank. Lists every decision, highest first.
    pub fn precedence<'a>(mut self, decisions: impl IntoIterator<Item = &'a Decision>) -> Self {
        self.precedence = Some(decisions.into_iter().map(|d| d.name.clone()).collect());
        self
    }

    /// Ranks all of a decision's reasons in declared order, highest first:
    /// `precedence page: critical_alert > sustained`.
    pub fn rank_reasons(mut self, decision: &Decision) -> Self {
        self.reason_precedence
            .push(decision.reasons.iter().map(|r| Outcome { decision: decision.name.clone(), reason: r.clone() }).collect());
        self
    }

    /// Ranks one decision's reasons in the order given, highest first.
    pub fn rank_outcomes(mut self, reasons: impl IntoIterator<Item = Outcome>) -> Self {
        self.reason_precedence.push(reasons.into_iter().collect());
        self
    }

    /// A set of outcomes that can't fire together; names at least two
    /// decisions or reasons.
    pub fn exclusive<R: Into<OutcomeRef>>(mut self, set: impl IntoIterator<Item = R>) -> Self {
        self.exclusive.push(set.into_iter().map(Into::into).collect());
        self
    }

    /// The outcome when no rule fires; required for a `collect one` kind.
    pub fn default_outcome(mut self, outcome: Outcome) -> Self {
        self.default = Some(outcome);
        self
    }

    /// The outcome of a conflict, instead of the default; `collect one` only.
    pub fn conflict(mut self, outcome: Outcome) -> Self {
        self.conflict = Some(outcome);
        self
    }

    /// Checks the kind and writes it. Fails with a [`SigilError`] listing every
    /// problem when the kind can't be exported, so a bad kind fails at startup.
    pub fn build(self) -> Result<Kind, Error> {
        let mut problems = self.problems.clone();
        let source = render(&self, &mut problems);
        if !problems.is_empty() {
            return Err(Error::Sigil(
                SigilError::new(format!(
                    "Kind::builder({}): invalid kind:\n{}",
                    self.name,
                    problems.iter().map(|p| format!("  {p}")).collect::<Vec<_>>().join("\n")
                ))
                .with_help("fix every problem listed; the kind can't be exported until then"),
            ));
        }
        let functions = self.functions.iter().filter_map(|(n, f)| f.implementation.clone().map(|i| (n.clone(), i))).collect();
        let ranked = self.precedence.as_ref().is_some_and(|p| !p.is_empty());
        Ok(Kind {
            name: self.name,
            version: self.version,
            source: source.into(),
            functions,
            shape: ResultShape { collect: self.collect, ranked },
        })
    }
}

/// `AlertRouting` as `alert_routing`, as `sigil export` names a kind's file.
pub fn snake_case(name: &str) -> String {
    let chars: Vec<char> = name.chars().collect();
    let mut out = String::new();
    for (i, &c) in chars.iter().enumerate() {
        if c.is_uppercase() && i > 0 {
            let prev = chars[i - 1];
            let next_lower = chars.get(i + 1).is_some_and(|n| n.is_lowercase());
            // A boundary before a capital that follows a lower case letter or
            // a digit, and before the last capital of an acronym.
            if prev.is_lowercase() || prev.is_ascii_digit() || (prev.is_uppercase() && next_lower) {
                out.push('_');
            }
        }
        out.extend(c.to_lowercase());
    }
    out
}

/// The name of the kind a source declares, if it's a kind file.
fn kind_name_of(source: &str) -> Option<&str> {
    let mut rest = source;
    loop {
        rest = rest.trim_start();
        match rest.strip_prefix("//") {
            Some(comment) => rest = &comment[comment.find('\n')? + 1..],
            None => break,
        }
    }
    let rest = rest.strip_prefix("kind")?;
    let after = rest.trim_start();
    if after.len() == rest.len() {
        return None;
    }
    let end = after.find(|c: char| !(c.is_ascii_alphanumeric() || c == '_')).unwrap_or(after.len());
    let name = &after[..end];
    if !is_ident(name) {
        return None;
    }
    let tail = after[end..].trim_start();
    let word = tail.strip_prefix("version")?;
    if word.starts_with(|c: char| c.is_ascii_alphanumeric() || c == '_') || tail.len() == after[end..].len() {
        return None;
    }
    Some(name)
}

fn is_ident(s: &str) -> bool {
    let mut chars = s.chars();
    chars.next().is_some_and(|c| c.is_ascii_alphabetic() || c == '_') && chars.all(|c| c.is_ascii_alphanumeric() || c == '_')
}

/// Renders the kind file, walking the kind in the order Go's builder does so
/// enums and struct types come out in the same order, and collects the
/// problems it meets.
fn render(spec: &KindBuilder, problems: &mut Vec<String>) -> String {
    let mut problem = |msg: String, help: Option<&str>| problems.push(help.map_or(msg.clone(), |h| format!("{msg} ({h})")));
    let name = &spec.name;

    if !is_ident(name) {
        problem(format!("kind name {name:?} isn't an identifier"), Some("use letters, digits and underscores, like AlertRouting"));
    }
    if spec.version < 1 {
        problem(format!("version {} isn't a whole number from 1", spec.version), None);
    }
    let accepts = spec.accepts.unwrap_or(1);
    if accepts < 1 || accepts > spec.version {
        problem(format!("accepts {accepts} isn't from 1 to the version, {}", spec.version), None);
    }

    // Decisions and how they collect.
    let decisions = &spec.decisions;
    let collect = spec.collect;
    if decisions.is_empty() {
        problem("the kind declares no decisions".into(), Some("set decisions, or collect_all for a collecting kind"));
    }
    let mut by_name: HashMap<&str, &Decision> = HashMap::new();
    for d in decisions {
        if by_name.insert(&d.name, d).is_some() {
            problem(format!("decision {} is declared twice", d.name), None);
        }
        if !is_ident(&d.name) {
            problem(format!("decision name {:?} isn't an identifier", d.name), None);
        }
        if d.reasons.is_empty() {
            problem(format!("decision {} declares no reasons", d.name), None);
        }
        let mut seen: Vec<&String> = Vec::new();
        for r in &d.reasons {
            if seen.contains(&r) {
                problem(format!("decision {} declares reason {r} twice", d.name), None);
            }
            seen.push(r);
            if !is_ident(r) {
                problem(format!("decision {}: reason {r:?} isn't an identifier", d.name), None);
            }
        }
        let mut fields: Vec<&str> = Vec::new();
        for f in &d.fields {
            if fields.contains(&f.name.as_str()) {
                problem(format!("decision {}: field {} is declared twice", d.name, f.name), None);
            }
            fields.push(&f.name);
        }
    }
    let mut precedence: Vec<String> = Vec::new();
    match (&spec.precedence, collect) {
        (Some(_), false) => problem("precedence is for a collecting kind".into(), Some("decisions already ranks them in order")),
        (Some(p), true) if !p.is_empty() => {
            precedence.clone_from(p);
            let names: Vec<&str> = decisions.iter().map(|d| d.name.as_str()).collect();
            let unique = p.iter().collect::<std::collections::HashSet<_>>().len() == p.len();
            if !unique || names.iter().any(|n| !p.iter().any(|x| x == n)) || p.iter().any(|x| !by_name.contains_key(x.as_str())) {
                problem(
                    "precedence must list every decision of the kind once".into(),
                    Some(&format!("the decisions are {}", names.join(", "))),
                );
            }
        }
        (_, false) => precedence = decisions.iter().map(|d| d.name.clone()).collect(),
        _ => {}
    }

    let declared = |o: &Outcome, what: &str, problem: &mut dyn FnMut(String, Option<&str>)| -> Option<&Decision> {
        match by_name.get(o.decision.as_str()) {
            None => {
                problem(format!("{what} {o}: decision {} isn't one of the kind's", o.decision), None);
                None
            }
            Some(d) => {
                if !d.reasons.contains(&o.reason) {
                    problem(format!("{what} {o}: decision {} has no reason {}", o.decision, o.reason), None);
                }
                Some(d)
            }
        }
    };
    let fallback = |o: &Option<Outcome>, what: &str, problem: &mut dyn FnMut(String, Option<&str>)| {
        let Some(o) = o else { return };
        if let Some(d) = declared(o, what, problem) {
            for f in &d.fields {
                if f.default.is_none() {
                    problem(format!("{what} {o}: payload field {} has no default", f.name), Some("give it one with field_default"));
                }
            }
        }
    };
    if !collect && spec.default.is_none() && !decisions.is_empty() {
        problem("a `collect one` kind needs a default".into(), Some("set default_outcome to the outcome when no rule fires"));
    }
    if collect && spec.conflict.is_some() {
        problem(
            "conflict is for a `collect one` kind".into(),
            Some("a collecting kind applies every decision, so nothing conflicts over the one outcome"),
        );
    }
    fallback(&spec.default, "default", &mut problem);
    fallback(&spec.conflict, "conflict", &mut problem);

    // Reason rankings.
    let mut ranked: HashMap<String, Vec<String>> = HashMap::new();
    for reasons in &spec.reason_precedence {
        let Some(first) = reasons.first() else {
            problem("a reason ranking names no reasons".into(), None);
            continue;
        };
        let Some(d) = by_name.get(first.decision.as_str()) else {
            problem(format!("reason ranking: decision {} isn't one of the kind's", first.decision), None);
            continue;
        };
        for o in reasons {
            if o.decision != d.name {
                problem(
                    format!("reason ranking {}: reason {} belongs to decision {}", d.name, o.reason, o.decision),
                    Some("rank each decision's reasons in a ranking of its own"),
                );
            }
        }
        let names: Vec<String> = reasons.iter().filter(|o| o.decision == d.name).map(|o| o.reason.clone()).collect();
        if ranked.contains_key(&d.name) {
            problem(format!("reasons of {} are ranked twice", d.name), None);
        }
        let unique = names.iter().collect::<std::collections::HashSet<_>>().len() == names.len();
        if !unique || d.reasons.iter().any(|x| !names.contains(x)) || names.iter().any(|x| !d.reasons.contains(x)) {
            problem(
                format!("the reason ranking of {} must name every reason of {} once", d.name, d.name),
                Some(&format!("{} declares: {}", d.name, d.reasons.join(", "))),
            );
        }
        ranked.insert(d.name.clone(), names);
    }

    // Exclusive sets.
    let mut exclusive: Vec<String> = Vec::new();
    for set in &spec.exclusive {
        if set.len() < 2 {
            problem("an exclusive set names at least two outcomes".into(), None);
        }
        let mut parts = Vec::new();
        for o in set {
            match o {
                OutcomeRef::Reason(o) => {
                    declared(o, "exclusive", &mut problem);
                    parts.push(o.to_string());
                }
                OutcomeRef::Decision(n) => {
                    if !by_name.contains_key(n.as_str()) {
                        problem(format!("exclusive: decision {n} isn't one of the kind's"), None);
                    }
                    parts.push(n.clone());
                }
            }
        }
        exclusive.push(parts.join(", "));
    }

    // Walk the types as Go's builder converts them: the inputs, then each
    // function's parameters and result, then each decision's payload. A struct
    // registers before its fields, so a nested struct follows the struct it's
    // in; an enum lands where it's first reached.
    let mut walk = Walk::default();
    for (_, ty) in &spec.inputs {
        walk.visit(ty, &mut problem);
    }
    for (fname, f) in &spec.functions {
        if !is_ident(fname) {
            problem(format!("function name {fname:?} isn't an identifier"), None);
        }
        for p in &f.params {
            walk.visit(p, &mut problem);
        }
        walk.visit(&f.result, &mut problem);
        if matches!(f.result, Type::Optional(_)) {
            problem(
                format!("function {fname}: the result can't be optional"),
                Some("return the zero value and let the policy compare, or return a list"),
            );
        }
    }
    for d in decisions {
        for f in &d.fields {
            walk.visit(&f.ty, &mut problem);
        }
    }
    for e in &spec.enums {
        walk.visit(e, &mut problem);
    }
    for e in &walk.enums {
        if e.values.is_empty() {
            problem(format!("enum {} declares no values", e.name), None);
        }
        if e.values.iter().collect::<std::collections::HashSet<_>>().len() != e.values.len() {
            problem(format!("enum {} declares a value twice", e.name), None);
        }
    }
    for (name, ty) in walk.structs.iter().map(|s| (&s.name, s)) {
        let mut seen: Vec<&str> = Vec::new();
        for (field, _) in &ty.fields {
            if seen.contains(&field.as_str()) {
                problem(format!("struct {name}: field {field} is declared twice"), None);
            }
            seen.push(field);
        }
    }

    // The kind file, as internal/kind's Source writes it.
    let mut out = format!("kind {name} version {}", spec.version);
    if accepts > 1 {
        out.push_str(&format!(", accepts: {accepts}"));
    }
    out.push('\n');
    if !walk.enums.is_empty() {
        out.push('\n');
    }
    for e in &walk.enums {
        out.push_str(&format!("enum {}: {}\n", e.name, e.values.join(" | ")));
    }
    for s in &walk.structs {
        out.push_str(&format!("\ntype {} {{{}", s.name, if s.fields.is_empty() { "" } else { "\n" }));
        for (field, ty) in &s.fields {
            out.push_str(&format!("  {field}: {}\n", ty.sigil()));
        }
        out.push_str("}\n");
    }
    if !spec.inputs.is_empty() {
        out.push('\n');
    }
    for (input, ty) in &spec.inputs {
        out.push_str(&format!("input {input}: {}\n", ty.sigil()));
    }
    if !spec.functions.is_empty() {
        out.push('\n');
    }
    for (fname, f) in &spec.functions {
        let params: Vec<String> = f.params.iter().map(Type::sigil).collect();
        out.push_str(&format!("fn {fname}({}) -> {}\n", params.join(", "), f.result.sigil()));
    }
    for d in decisions {
        out.push_str(&format!("\ndecision {} {{\n  reason: {}\n", d.name, d.reasons.join(" | ")));
        for f in &d.fields {
            match &f.default {
                Some(default) => {
                    let value = match (&f.ty, default) {
                        (Type::Optional(_), serde_json::Value::Null) => {
                            problem(
                                format!(
                                    "decision {}: field {}: `none` isn't a constant: leave the default out; a field without one is required",
                                    d.name, f.name
                                ),
                                None,
                            );
                            "?".to_string()
                        }
                        (ty, v) => constant::format(ty, v).unwrap_or_else(|err| {
                            problem(format!("decision {}: field {}: {err}", d.name, f.name), None);
                            "?".to_string()
                        }),
                    };
                    out.push_str(&format!("  {}: {} = {value}\n", f.name, f.ty.sigil()));
                }
                None => out.push_str(&format!("  {}: {}\n", f.name, f.ty.sigil())),
            }
        }
        out.push_str("}\n");
    }
    let mut resolution: Vec<String> = Vec::new();
    if !decisions.is_empty() {
        resolution.push(if collect { "collect all" } else { "collect one" }.into());
    }
    if !precedence.is_empty() {
        resolution.push(format!("precedence {}", precedence.join(" > ")));
    }
    for d in decisions {
        if let Some(r) = ranked.get(&d.name) {
            resolution.push(format!("precedence {}: {}", d.name, r.join(" > ")));
        }
    }
    resolution.extend(exclusive.iter().map(|set| format!("exclusive {set}")));
    if !resolution.is_empty() {
        out.push_str(&format!("\n{}\n", resolution.join("\n")));
    }
    if let Some(d) = &spec.default {
        out.push_str(&format!("\ndefault {}(reason: {})\n", d.decision, d.reason));
    }
    if let Some(c) = &spec.conflict {
        if spec.default.is_none() {
            out.push('\n');
        }
        out.push_str(&format!("conflict {}(reason: {})\n", c.decision, c.reason));
    }
    out
}

/// The enums and struct types a kind reaches, in the order Go registers them.
#[derive(Default)]
struct Walk {
    enums: Vec<Arc<EnumDef>>,
    structs: Vec<Arc<StructDef>>,
}

impl Walk {
    fn visit(&mut self, ty: &Type, problem: &mut impl FnMut(String, Option<&str>)) {
        match ty {
            Type::Enum(e) => {
                if self.claim(&e.name, ty, problem) {
                    self.enums.push(Arc::clone(e));
                }
            }
            Type::Struct(s) => {
                if self.claim(&s.name, ty, problem) {
                    self.structs.push(Arc::clone(s));
                    for (_, f) in &s.fields {
                        self.visit(f, problem);
                    }
                }
            }
            other => {
                for inner in other.inner() {
                    self.visit(inner, problem);
                }
            }
        }
    }

    /// Whether `ty` is new; a different type under a taken name is a problem.
    fn claim(&self, name: &str, ty: &Type, problem: &mut impl FnMut(String, Option<&str>)) -> bool {
        let known = self
            .enums
            .iter()
            .find(|e| e.name == name)
            .map(|e| Type::Enum(Arc::clone(e)))
            .or_else(|| self.structs.iter().find(|s| s.name == name).map(|s| Type::Struct(Arc::clone(s))));
        match known {
            None => true,
            Some(prev) => {
                if prev != *ty {
                    problem(
                        format!("two different types are both named {name}"),
                        Some("rename one; each enum and struct needs a name of its own"),
                    );
                }
                false
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;

    #[rstest]
    #[case("AlertRouting", "alert_routing")]
    #[case("DeployApproval", "deploy_approval")]
    #[case("Minimal", "minimal")]
    #[case("HTTPServer", "http_server")]
    #[case("Kind2Go", "kind2_go")]
    #[case("already_snake", "already_snake")]
    #[case("A", "a")]
    #[case("ABC", "abc")]
    fn names_the_kind_file_as_sigil_export_does(#[case] name: &str, #[case] file: &str) {
        assert_eq!(snake_case(name), file);
    }

    #[rstest]
    #[case("kind A version 1\n", Some("A"))]
    #[case("// a comment\n// another\nkind Alert_Routing version 3, accepts: 2\n", Some("Alert_Routing"))]
    #[case("  \n\n  kind   K   version 1", Some("K"))]
    #[case("policy a.b: K@1\n", None)]
    #[case("kindred K version 1\n", None)]
    #[case("kind K versions 1\n", None)]
    #[case("kind K\n", None)]
    #[case("kind 1K version 1\n", None)]
    #[case("kindK version 1\n", None)]
    #[case("kind Kversion 1\n", None)]
    #[case("// only a comment", None)]
    #[case("", None)]
    fn reads_the_kind_a_source_declares(#[case] source: &str, #[case] name: Option<&str>) {
        assert_eq!(kind_name_of(source), name);
    }

    #[test]
    fn a_kind_is_cloneable_and_prints_without_its_source() {
        let ok = Decision::new("ok", ["yes"]);
        let kind = Kind::builder("K").version(1).decisions([&ok]).default_outcome(ok.reason("yes")).build().unwrap();
        let copy = kind.clone();
        assert_eq!(copy.schema(), kind.schema());
        assert_eq!(copy.file(None).path, "k.sigil");
        assert_eq!(copy.file(Some("x/k.sigil")).path, "x/k.sigil");
        assert_eq!((copy.name(), copy.version()), ("K", 1));
        assert_eq!(format!("{kind:?}"), "Kind { name: \"K\", version: 1, .. }");
    }
}
