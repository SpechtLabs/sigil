//! Errors. A failed *evaluation* is not an error: [`crate::EvalResult::error`]
//! says why, like the CLI. Everything else is an [`Error`].

use std::error::Error as StdError;
use std::fmt;
use std::time::Duration;

use crate::types::Diagnostic;

/// What went wrong, and what to do about it.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// An op with `ok: false`, a kind that breaks a rule, a module that isn't
    /// Sigil's, or a request that can't be encoded. The instance still works.
    #[error("{0}")]
    Sigil(#[from] SigilError),
    /// The instance has stopped: Go's runtime exited or panicked, the module
    /// trapped, or a call was killed. Go can't resume after any of these, so
    /// every later call returns this same error. Build a new [`crate::Sigil`]
    /// (a [`crate::Pool`] does that by itself).
    #[error("{0}")]
    Stopped(StoppedError),
    /// A call ran past its hard deadline and was killed from outside by epoch
    /// interruption: the evaluation's timeout plus its grace period. The
    /// instance is stopped from now on.
    #[error(
        "the call ran past its hard deadline of {0:?} and was killed; the Sigil instance is stopped\n  help: raise the timeout, or look for a policy loop over a large input or a host function that blocks; build a new instance to go on"
    )]
    Timeout(Duration),
    /// A call used up its fuel, see [`crate::EvalOptions::fuel`]. The instance
    /// is stopped from now on.
    #[error(
        "the call ran out of fuel and was stopped; the Sigil instance is stopped\n  help: raise EvalOptions::fuel, or look for a policy loop over a large input; build a new instance to go on"
    )]
    OutOfFuel,
    /// [`crate::Pool`]: no policy of this name is installed.
    #[error("the pool has no policy named {0}\n  help: install it with Pool::install or Pool::compile before evaluating it")]
    NoPolicy(String),
    /// [`crate::Pool`]: every instance stayed busy for the whole
    /// [`crate::PoolOptions::acquire_timeout`].
    #[error(
        "every instance of the pool stayed busy for {0:?}\n  help: the pool is saturated; raise its size or shed load before evaluating"
    )]
    Busy(Duration),
}

impl Error {
    /// Whether the instance is dead after this error: every later call on it
    /// fails. True for [`Error::Stopped`], [`Error::Timeout`] and
    /// [`Error::OutOfFuel`].
    pub fn is_stopped(&self) -> bool {
        matches!(self, Error::Stopped(_) | Error::Timeout(_) | Error::OutOfFuel)
    }

    /// The diagnostics of a [`SigilError`], and none for the other kinds.
    pub fn diagnostics(&self) -> &[Diagnostic] {
        match self {
            Error::Sigil(e) => &e.diagnostics,
            _ => &[],
        }
    }

    pub(crate) fn sigil(message: impl Into<String>, help: impl Into<String>) -> Self {
        Error::Sigil(SigilError::new(message).with_help(help))
    }
}

/// Why an operation failed: files that don't compile, a request the module
/// rejects, a kind that breaks a rule. `diagnostics` holds the errors and
/// warnings when there are any, the records `sigil check -o json` prints.
#[derive(Debug)]
pub struct SigilError {
    pub message: String,
    /// How to fix it, when the module knows.
    pub help: Option<String>,
    pub diagnostics: Vec<Diagnostic>,
    source: Option<Box<dyn StdError + Send + Sync>>,
}

impl SigilError {
    pub fn new(message: impl Into<String>) -> Self {
        Self { message: message.into(), help: None, diagnostics: Vec::new(), source: None }
    }

    pub fn with_help(mut self, help: impl Into<String>) -> Self {
        let help = help.into();
        self.help = (!help.is_empty()).then_some(help);
        self
    }

    pub fn with_diagnostics(mut self, diagnostics: Vec<Diagnostic>) -> Self {
        self.diagnostics = diagnostics;
        self
    }

    pub fn with_source(mut self, source: impl StdError + Send + Sync + 'static) -> Self {
        self.source = Some(Box::new(source));
        self
    }
}

impl fmt::Display for SigilError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)?;
        if let Some(help) = &self.help {
            write!(f, "\n  help: {help}")?;
        }
        for d in &self.diagnostics {
            write!(f, "\n  {}", d.render())?;
        }
        Ok(())
    }
}

impl StdError for SigilError {
    fn source(&self) -> Option<&(dyn StdError + 'static)> {
        self.source.as_deref().map(|e| e as &(dyn StdError + 'static))
    }
}

/// The error of an instance that has stopped, see [`Error::Stopped`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StoppedError {
    /// Why it stopped, with the tail of the module's standard error.
    pub message: String,
    pub help: String,
}

impl fmt::Display for StoppedError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}\n  help: {}", self.message, self.help)
    }
}

impl StdError for StoppedError {}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::Severity;

    fn diagnostic() -> Diagnostic {
        Diagnostic {
            severity: Severity::Error,
            lint: None,
            file: Some("a.sigil".into()),
            document: None,
            message: "bad".into(),
            help: None,
            line: Some(1),
            column: Some(2),
        }
    }

    #[test]
    fn a_sigil_error_prints_its_help_and_diagnostics() {
        let err = SigilError::new("the compile failed").with_help("fix them").with_diagnostics(vec![diagnostic()]);
        assert_eq!(err.to_string(), "the compile failed\n  help: fix them\n  a.sigil:1:2: error: bad");
        assert_eq!(SigilError::new("x").with_help("").help, None);
    }

    #[test]
    fn only_some_errors_mean_a_dead_instance() {
        let stopped = Error::Stopped(StoppedError { message: "m".into(), help: "h".into() });
        assert!(stopped.is_stopped() && Error::Timeout(Duration::from_secs(1)).is_stopped() && Error::OutOfFuel.is_stopped());
        assert!(
            !Error::sigil("m", "h").is_stopped() && !Error::NoPolicy("p".into()).is_stopped() && !Error::Busy(Duration::ZERO).is_stopped()
        );
    }

    #[test]
    fn diagnostics_come_from_a_sigil_error_only() {
        let err = Error::Sigil(SigilError::new("m").with_diagnostics(vec![diagnostic()]));
        assert_eq!(err.diagnostics().len(), 1);
        assert!(Error::OutOfFuel.diagnostics().is_empty());
    }

    #[test]
    fn a_sigil_error_keeps_its_source() {
        let cause = std::io::Error::other("disk");
        let err = SigilError::new("reading").with_source(cause);
        assert_eq!(StdError::source(&err).unwrap().to_string(), "disk");
        assert!(StdError::source(&SigilError::new("x")).is_none());
    }

    #[test]
    fn stopped_errors_say_what_to_do() {
        let e = StoppedError { message: "the module trapped".into(), help: "build a new one".into() };
        assert_eq!(e.to_string(), "the module trapped\n  help: build a new one");
    }
}
