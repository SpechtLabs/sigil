//! In-memory telemetry for the suites that read spans and log lines. The
//! subscriber is process-global (tracing's one global), so each test binary
//! installs it once and its tests tell their own output apart by content.

use std::io;
use std::sync::{Arc, Mutex, OnceLock};

use featuregate::config::Config;
use featuregate::telemetry::Telemetry;
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider, SpanData};
use serde_json::Value;
use tracing_subscriber::fmt::MakeWriter;

#[derive(Clone, Default)]
struct Buffer(Arc<Mutex<Vec<u8>>>);

impl io::Write for Buffer {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

impl<'a> MakeWriter<'a> for Buffer {
    type Writer = Buffer;
    fn make_writer(&'a self) -> Buffer {
        self.clone()
    }
}

pub struct Observed {
    spans: InMemorySpanExporter,
    logs: Buffer,
}

/// Installs the in-memory telemetry for this process, once.
pub fn observed() -> &'static Observed {
    static OBSERVED: OnceLock<Observed> = OnceLock::new();
    OBSERVED.get_or_init(|| {
        let config = Config::from_lookup(|name| (name == "FEATUREGATE_VERSION").then(|| "test".to_owned())).unwrap();
        let spans = InMemorySpanExporter::default();
        let logs = Buffer::default();
        let provider = SdkTracerProvider::builder().with_simple_exporter(spans.clone()).build();
        let telemetry = Telemetry::with_provider(&config, provider, logs.clone());
        tracing::dispatcher::set_global_default(telemetry.dispatch()).expect("one global subscriber per test binary");
        Observed { spans, logs }
    })
}

impl Observed {
    /// Every span finished so far.
    pub fn spans(&self) -> Vec<SpanData> {
        self.spans.get_finished_spans().unwrap()
    }

    /// Every log line so far, parsed.
    pub fn lines(&self) -> Vec<Value> {
        let text = String::from_utf8(self.logs.0.lock().unwrap().clone()).unwrap();
        text.lines().map(|l| serde_json::from_str(l).unwrap_or_else(|e| panic!("log line isn't JSON ({e}): {l}"))).collect()
    }
}

/// A span attribute's value as text.
pub fn attr(span: &SpanData, key: &str) -> Option<String> {
    span.attributes.iter().find(|kv| kv.key.as_str() == key).map(|kv| kv.value.to_string())
}
