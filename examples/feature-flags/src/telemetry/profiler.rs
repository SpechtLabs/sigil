//! Pyroscope continuous CPU profiling, on when `PYROSCOPE_SERVER_ADDRESS` is set.

use pyroscope::pyroscope::PyroscopeAgentRunning;
use pyroscope::{PyroscopeAgent, PyroscopeError};
use pyroscope_pprofrs::{PprofConfig, pprof_backend};

use super::SERVICE_NAME;

/// A running profiling agent; stop it at shutdown to send the last profile.
pub struct Profiler {
    agent: PyroscopeAgent<PyroscopeAgentRunning>,
}

impl Profiler {
    /// Starts sampling at 100 Hz and uploading every ten seconds, tagged with
    /// the version so a deploy shows up as a tag change in the flame graph.
    pub fn start(address: &str, version: &str) -> Result<Self, PyroscopeError> {
        let backend = pprof_backend(PprofConfig::new().sample_rate(100));
        let agent = PyroscopeAgent::builder(address, SERVICE_NAME).backend(backend).tags(vec![("version", version)]).build()?.start()?;
        Ok(Self { agent })
    }

    /// Stops sampling and uploads what is left.
    pub fn stop(self) {
        match self.agent.stop() {
            Ok(ready) => ready.shutdown(),
            Err(e) => tracing::warn!(error = %e, "stopping the profiler failed"),
        }
    }
}
