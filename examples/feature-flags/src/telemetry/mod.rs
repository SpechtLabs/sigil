//! Logs, traces and profiles. Nothing here is global: [`Telemetry::new`]
//! builds the pieces and a [`tracing::Dispatch`], and `main` installs it.
//! Tests build their own with an in-memory span exporter and log buffer.

mod logs;
mod profiler;

use std::io;

use opentelemetry::trace::TracerProvider as _;
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::trace::SdkTracerProvider;
use tracing::Dispatch;
use tracing_subscriber::fmt::MakeWriter;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::{EnvFilter, Registry};

pub use logs::JsonFormat;
pub use profiler::Profiler;

use crate::config::{Config, LogFormat};

/// The name traces and profiles carry.
pub const SERVICE_NAME: &str = "featuregate";

/// Where spans go.
pub enum Exporter {
    /// Spans get ids (logs carry them) but aren't sent anywhere.
    None,
    /// Batched to an OTLP/gRPC endpoint, such as Alloy's.
    Otlp(String),
}

/// The telemetry of one process: the tracer provider to flush at shutdown, and
/// the dispatcher to install.
pub struct Telemetry {
    provider: SdkTracerProvider,
    dispatch: Dispatch,
}

impl Telemetry {
    /// Builds the subscriber: a filter, the log layer on `writer`, and the
    /// OpenTelemetry layer feeding a tracer provider. Needs a running tokio
    /// runtime when `exporter` is [`Exporter::Otlp`].
    pub fn new<W>(config: &Config, exporter: Exporter, writer: W) -> Result<Self, String>
    where
        W: for<'a> MakeWriter<'a> + Send + Sync + 'static,
    {
        let resource = Resource::builder()
            .with_service_name(SERVICE_NAME)
            .with_attribute(opentelemetry::KeyValue::new("service.version", config.version.clone()))
            .build();
        let builder = SdkTracerProvider::builder().with_resource(resource);
        let provider = match exporter {
            Exporter::None => builder.build(),
            Exporter::Otlp(endpoint) => {
                let exporter = opentelemetry_otlp::SpanExporter::builder()
                    .with_tonic()
                    .with_endpoint(endpoint.clone())
                    .build()
                    .map_err(|e| format!("can't set up the OTLP trace exporter for {endpoint}: {e}\n  help: check OTEL_EXPORTER_OTLP_ENDPOINT, such as http://alloy:4317"))?;
                builder.with_batch_exporter(exporter).build()
            }
        };
        Ok(Self::with_provider(config, provider, writer))
    }

    /// Builds the subscriber around a tracer provider the caller made, which
    /// is how tests read spans from an in-memory exporter.
    pub fn with_provider<W>(config: &Config, provider: SdkTracerProvider, writer: W) -> Self
    where
        W: for<'a> MakeWriter<'a> + Send + Sync + 'static,
    {
        let tracer = provider.tracer(SERVICE_NAME);

        let default_level = if config.debug { "debug" } else { "info" };
        let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new(default_level));
        let otel = tracing_opentelemetry::layer().with_tracer(tracer);
        let dispatch = match config.log_format {
            LogFormat::Json => {
                let logs = tracing_subscriber::fmt::layer().event_format(JsonFormat).with_writer(writer);
                Dispatch::new(Registry::default().with(filter).with(otel).with(logs))
            }
            LogFormat::Console => {
                let logs = tracing_subscriber::fmt::layer().compact().with_ansi(false).with_writer(writer);
                Dispatch::new(Registry::default().with(filter).with(otel).with(logs))
            }
        };
        Self { provider, dispatch }
    }

    /// The dispatcher to install with `tracing::dispatcher::set_global_default`.
    pub fn dispatch(&self) -> Dispatch {
        self.dispatch.clone()
    }

    /// Sends the spans still buffered; called once at shutdown.
    pub fn shutdown(&self) {
        if let Err(e) = self.provider.shutdown() {
            // The logger may be gone by now, so this goes to stderr directly.
            eprintln!("flushing traces at shutdown failed: {e}");
        }
    }
}

/// Standard output as a log writer: a zero-sized `MakeWriter`.
#[derive(Clone, Copy)]
pub struct Stdout;

impl<'a> MakeWriter<'a> for Stdout {
    type Writer = io::Stdout;
    fn make_writer(&'a self) -> Self::Writer {
        io::stdout()
    }
}
