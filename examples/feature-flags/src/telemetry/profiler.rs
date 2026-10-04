//! Pyroscope continuous CPU profiling, on when `PYROSCOPE_SERVER_ADDRESS` is set.

use pyroscope::backend::{BackendConfig, PprofConfig, pprof_backend};
use pyroscope::pyroscope::{PyroscopeAgentBuilder, PyroscopeAgentRunning, PyroscopeConfig};
use pyroscope::{PyroscopeAgent, PyroscopeError};

use super::SERVICE_NAME;

/// Samples per second, for the sampler and for the server's rate math.
const SAMPLE_RATE: u32 = 100;

/// A running profiling agent; stop it at shutdown to send the last profile.
pub struct Profiler {
    agent: PyroscopeAgent<PyroscopeAgentRunning>,
}

impl Profiler {
    /// Starts sampling at 100 Hz and uploading every ten seconds, tagged with
    /// the version so a deploy shows up as a tag change in the flame graph.
    pub fn start(address: &str, version: &str) -> Result<Self, PyroscopeError> {
        // The spy is pyroscope's bundled pprof-rs backend; its default config
        // carries that backend's name and the crate's own version.
        let spy = PyroscopeConfig::default();
        let backend = pprof_backend(PprofConfig { sample_rate: SAMPLE_RATE }, BackendConfig::default());
        let agent = PyroscopeAgentBuilder::new(address, SERVICE_NAME, SAMPLE_RATE, spy.spy_name, spy.spy_version, backend)
            .tags(vec![("version", version)])
            .build()?
            .start()?;
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
