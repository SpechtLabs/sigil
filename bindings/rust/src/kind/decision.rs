//! Decisions and their reasons as typed handles, the twin of Go's
//! `policy.NewDecision`: a kind declares them, and a host reads a result
//! through them, getting a typed payload back instead of a bag of JSON.

use serde::de::DeserializeOwned;
use serde_json::Value;

use super::ty::Type;
use crate::error::Error;
use crate::types::EvalResult;

/// How a kind's results read, which [`crate::Kind::compile`] attaches to each
/// result so a decision handle can tell a single outcome from a collection.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ResultShape {
    pub collect: bool,
    /// A collecting kind with precedence: its outcome is the top rank.
    pub ranked: bool,
}

/// A decision: its name, its reasons and its payload fields. Declare one with
/// [`Decision::new`].
///
/// ```
/// use serde_json::json;
/// use sigil::kind::{Decision, Type};
///
/// let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));
/// assert_eq!(notify.reason("unrouted").to_string(), "notify.unrouted");
/// ```
#[derive(Debug, Clone, PartialEq)]
pub struct Decision {
    pub(crate) name: String,
    pub(crate) reasons: Vec<String>,
    pub(crate) fields: Vec<Field>,
}

/// A payload field of a decision: a type, and optionally its default, which
/// the field takes when a rule leaves it out and which the kind's default and
/// conflict decisions take for every field.
#[derive(Debug, Clone, PartialEq)]
pub struct Field {
    pub name: String,
    pub ty: Type,
    pub default: Option<Value>,
}

/// A reason of one decision, `approve.release_manager` in a kind file: what a
/// kind ranks, picks as its default, or marks exclusive, and what a host
/// checks a result for.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct Outcome {
    pub(crate) decision: String,
    pub(crate) reason: String,
}

/// One entry of an outcome, with its payload typed.
#[derive(Debug, Clone, PartialEq)]
pub struct Matched<P> {
    pub payload: P,
    pub reason: String,
    /// The policy whose rule produced it; `None` for the default.
    pub policy: Option<String>,
    /// The rule's position, `file:line:column`; `None` for the default.
    pub position: Option<String>,
}

/// What `exclusive` can name: a whole decision, or one of its reasons.
#[derive(Debug, Clone, PartialEq)]
pub enum OutcomeRef {
    Decision(String),
    Reason(Outcome),
}

impl Decision {
    /// A decision with its reasons, in declaration order.
    pub fn new<R: Into<String>>(name: impl Into<String>, reasons: impl IntoIterator<Item = R>) -> Self {
        Self { name: name.into(), reasons: reasons.into_iter().map(Into::into).collect(), fields: Vec::new() }
    }

    /// A payload field without a default: every rule must give it.
    pub fn field(mut self, name: impl Into<String>, ty: Type) -> Self {
        self.fields.push(Field { name: name.into(), ty, default: None });
        self
    }

    /// A payload field with a default, given as the JSON value a host would
    /// pass for its type.
    pub fn field_default(mut self, name: impl Into<String>, ty: Type, default: Value) -> Self {
        self.fields.push(Field { name: name.into(), ty, default: Some(default) });
        self
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    pub fn reasons(&self) -> &[String] {
        &self.reasons
    }

    pub fn fields(&self) -> &[Field] {
        &self.fields
    }

    /// One of the decision's reasons, as a handle.
    ///
    /// # Panics
    ///
    /// For a reason the decision doesn't declare, with a did-you-mean: a kind
    /// is defined once at startup, where a typo should stop the program. Use
    /// [`Decision::try_reason`] to handle it.
    #[track_caller]
    pub fn reason(&self, name: &str) -> Outcome {
        match self.try_reason(name) {
            Ok(outcome) => outcome,
            Err(err) => panic!("{err}"),
        }
    }

    /// [`Decision::reason`] that reports an undeclared reason.
    pub fn try_reason(&self, name: &str) -> Result<Outcome, Error> {
        if !self.reasons.iter().any(|r| r == name) {
            return Err(Error::sigil(
                format!("decision {} has no reason {name:?}", self.name),
                format!("{}{} declares: {}", did_you_mean(name, &self.reasons), self.name, self.reasons.join(", ")),
            ));
        }
        Ok(Outcome { decision: self.name.clone(), reason: name.to_string() })
    }

    /// The payload, when the result's outcome is exactly one entry of this
    /// decision, and `None` otherwise. A failed evaluation's result holds the
    /// kind's fallback, so check `error` first. Fails on the result of a
    /// collecting kind without precedence; use [`Decision::match_all`] there.
    pub fn matches<P: DeserializeOwned>(&self, res: &EvalResult) -> Result<Option<P>, Error> {
        single(res, "matches")?;
        match res.outcome.as_slice() {
            [entry] if entry.decision == self.name => Ok(Some(payload(&self.name, entry.payload.as_ref())?)),
            _ => Ok(None),
        }
    }

    /// Every entry of this decision in the result's outcome, in outcome order:
    /// how a collecting kind, which can grant a decision more than once, is read.
    pub fn match_all<P: DeserializeOwned>(&self, res: &EvalResult) -> Result<Vec<Matched<P>>, Error> {
        res.outcome
            .iter()
            .filter(|e| e.decision == self.name)
            .map(|e| {
                Ok(Matched {
                    payload: payload(&self.name, e.payload.as_ref())?,
                    reason: e.reason.clone(),
                    policy: e.policy.clone(),
                    position: e.position.clone(),
                })
            })
            .collect()
    }
}

impl Outcome {
    /// A handle by names, checked when the kind builds: use [`Decision::reason`] where
    /// the decision is at hand.
    pub fn new(decision: impl Into<String>, reason: impl Into<String>) -> Self {
        Self { decision: decision.into(), reason: reason.into() }
    }

    pub fn decision(&self) -> &str {
        &self.decision
    }

    pub fn reason(&self) -> &str {
        &self.reason
    }

    /// Whether the result's outcome is exactly one entry, of this decision
    /// with this reason. A failed evaluation's result holds the kind's
    /// fallback, so check `error` first. Fails on the result of a collecting
    /// kind without precedence, whose outcome has no single entry to compare.
    pub fn is(&self, res: &EvalResult) -> Result<bool, Error> {
        single(res, "is")?;
        Ok(matches!(res.outcome.as_slice(), [e] if e.decision == self.decision && e.reason == self.reason))
    }
}

impl std::fmt::Display for Outcome {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}.{}", self.decision, self.reason)
    }
}

impl From<&Decision> for OutcomeRef {
    fn from(d: &Decision) -> Self {
        OutcomeRef::Decision(d.name.clone())
    }
}

impl From<Outcome> for OutcomeRef {
    fn from(o: Outcome) -> Self {
        OutcomeRef::Reason(o)
    }
}

fn payload<P: DeserializeOwned>(decision: &str, payload: Option<&serde_json::Map<String, Value>>) -> Result<P, Error> {
    let value = Value::Object(payload.cloned().unwrap_or_default());
    serde_json::from_value(value).map_err(|err| {
        Error::sigil(
            format!("the payload of decision {decision} doesn't fit the type asked for: {err}"),
            "give the type the decision's fields, with the same names and types; a duration or timestamp is a String",
        )
    })
}

/// Fails for a result whose outcome isn't one decision: a collecting kind
/// without precedence's. Without the kind's mark, `collect` alone says so.
fn single(res: &EvalResult, method: &str) -> Result<(), Error> {
    let unranked = match res.shape {
        Some(shape) => shape.collect && !shape.ranked,
        None => res.collect,
    };
    if unranked {
        return Err(Error::sigil(
            format!("{method} on a collecting kind's result, which has no single outcome; read it with match_all"),
            "a collecting kind applies every decision that fired; with precedence, the top rank reads with matches",
        ));
    }
    Ok(())
}

/// A did-you-mean for a misspelled name, or nothing.
pub(crate) fn did_you_mean(name: &str, names: &[String]) -> String {
    let mut best: Option<&String> = None;
    let mut best_distance = (name.chars().count() / 3).max(2) + 1;
    for candidate in names {
        let d = distance(name, candidate);
        if d < best_distance {
            best = Some(candidate);
            best_distance = d;
        }
    }
    best.map_or_else(String::new, |b| format!("did you mean {b:?}? "))
}

fn distance(a: &str, b: &str) -> usize {
    let a: Vec<char> = a.chars().collect();
    let b: Vec<char> = b.chars().collect();
    let mut row: Vec<usize> = (0..=b.len()).collect();
    for i in 1..=a.len() {
        let mut prev = row[0];
        row[0] = i;
        for j in 1..=b.len() {
            let cur = row[j];
            row[j] = (cur + 1).min(row[j - 1] + 1).min(prev + usize::from(a[i - 1] != b[j - 1]));
            prev = cur;
        }
    }
    row[b.len()]
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;

    #[rstest]
    #[case("unroutd", &["routine", "unrouted"], "did you mean \"unrouted\"? ")]
    #[case("zzzzzz", &["routine", "unrouted"], "")]
    #[case("a", &["b"], "did you mean \"b\"? ")]
    #[case("", &[], "")]
    fn suggests_the_near_miss(#[case] name: &str, #[case] names: &[&str], #[case] want: &str) {
        let names: Vec<String> = names.iter().map(|s| (*s).to_string()).collect();
        assert_eq!(did_you_mean(name, &names), want);
    }

    #[rstest]
    #[case("", "", 0)]
    #[case("kitten", "sitting", 3)]
    #[case("same", "same", 0)]
    #[case("ä", "a", 1)]
    fn measures_edit_distance(#[case] a: &str, #[case] b: &str, #[case] want: usize) {
        assert_eq!(distance(a, b), want);
    }

    fn result(outcome: Vec<(&str, &str)>, collect: bool, shape: Option<ResultShape>) -> EvalResult {
        let entry = |d: &str, r: &str| crate::types::EvalEntry {
            payload: Some(serde_json::json!({"n": 1}).as_object().unwrap().clone()),
            decision: d.into(),
            reason: r.into(),
            policy: Some("p".into()),
            position: Some("f:1:1".into()),
            chain: vec![],
            conditions: vec![],
            outcome: true,
        };
        EvalResult {
            payload: None,
            error: None,
            policy: "p".into(),
            decision: None,
            reason: None,
            outcome: outcome.iter().map(|(d, r)| entry(d, r)).collect(),
            trace: vec![],
            collect,
            shape,
        }
    }

    #[derive(serde::Deserialize, PartialEq, Debug)]
    struct N {
        n: u32,
    }

    #[test]
    fn matches_needs_exactly_one_entry_of_the_decision() {
        let d = Decision::new("d", ["a", "b"]);
        let one = result(vec![("d", "a")], false, None);
        assert_eq!(d.matches::<N>(&one).unwrap(), Some(N { n: 1 }));
        assert!(d.reason("a").is(&one).unwrap());
        assert!(!d.reason("b").is(&one).unwrap());
        let other = result(vec![("e", "a")], false, None);
        assert_eq!(d.matches::<N>(&other).unwrap(), None);
        let two = result(vec![("d", "a"), ("d", "b")], false, Some(ResultShape { collect: true, ranked: true }));
        assert_eq!(d.matches::<N>(&two).unwrap(), None);
        assert!(!d.reason("a").is(&two).unwrap());
        assert_eq!(d.match_all::<N>(&two).unwrap().len(), 2);
        assert!(d.match_all::<N>(&result(vec![], false, None)).unwrap().is_empty());
    }

    #[rstest]
    #[case(None, true, true)]
    #[case(None, false, false)]
    #[case(Some(ResultShape { collect: true, ranked: false }), false, true)]
    #[case(Some(ResultShape { collect: true, ranked: true }), true, false)]
    #[case(Some(ResultShape { collect: false, ranked: false }), true, false)]
    fn a_collecting_result_without_precedence_has_no_single_outcome(
        #[case] shape: Option<ResultShape>,
        #[case] collect: bool,
        #[case] refuses: bool,
    ) {
        let d = Decision::new("d", ["a"]);
        let res = result(vec![("d", "a")], collect, shape);
        assert_eq!(d.matches::<N>(&res).is_err(), refuses);
        assert_eq!(d.reason("a").is(&res).is_err(), refuses);
        assert!(d.match_all::<N>(&res).is_ok());
    }

    #[test]
    fn handles_print_as_the_kind_file_writes_them() {
        let d = Decision::new("page", ["sustained"]);
        assert_eq!(d.reason("sustained").to_string(), "page.sustained");
        assert_eq!((d.reason("sustained").decision(), d.reason("sustained").reason()), ("page", "sustained"));
        assert_eq!(OutcomeRef::from(&d), OutcomeRef::Decision("page".into()));
    }
}
