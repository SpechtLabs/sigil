//! The JSON log line: one object per event with `time`, `level`, `msg`, the
//! event's own fields, and `trace_id` and `span_id` once, taken from the
//! span the event happened in.

use std::fmt;

use opentelemetry::trace::TraceContextExt;
use serde_json::{Map, Number, Value};
use tracing::field::{Field, Visit};
use tracing::{Event, Subscriber};
use tracing_opentelemetry::OpenTelemetrySpanExt;
use tracing_subscriber::fmt::format::Writer;
use tracing_subscriber::fmt::time::{FormatTime, SystemTime};
use tracing_subscriber::fmt::{FmtContext, FormatEvent, FormatFields};
use tracing_subscriber::registry::LookupSpan;

pub struct JsonFormat;

impl<S, N> FormatEvent<S, N> for JsonFormat
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    N: for<'a> FormatFields<'a> + 'static,
{
    fn format_event(&self, _ctx: &FmtContext<'_, S, N>, mut writer: Writer<'_>, event: &Event<'_>) -> fmt::Result {
        let mut line = Map::new();
        let mut time = String::new();
        SystemTime.format_time(&mut Writer::new(&mut time))?;
        line.insert("time".into(), time.into());
        line.insert("level".into(), event.metadata().level().as_str().to_lowercase().into());
        let mut fields = Fields(&mut line);
        event.record(&mut fields);
        line.insert("target".into(), event.metadata().target().into());

        let cx = tracing::Span::current().context();
        let span = cx.span();
        let sc = span.span_context();
        if sc.is_valid() {
            line.insert("trace_id".into(), sc.trace_id().to_string().into());
            line.insert("span_id".into(), sc.span_id().to_string().into());
        }
        writeln!(writer, "{}", Value::Object(line))
    }
}

/// Collects an event's fields; the `message` field becomes `msg`.
struct Fields<'a>(&'a mut Map<String, Value>);

impl Visit for Fields<'_> {
    fn record_debug(&mut self, field: &Field, value: &dyn fmt::Debug) {
        let name = if field.name() == "message" { "msg" } else { field.name() };
        self.0.insert(name.into(), format!("{value:?}").into());
    }
    fn record_str(&mut self, field: &Field, value: &str) {
        let name = if field.name() == "message" { "msg" } else { field.name() };
        self.0.insert(name.into(), value.into());
    }
    fn record_i64(&mut self, field: &Field, value: i64) {
        self.0.insert(field.name().into(), value.into());
    }
    fn record_u64(&mut self, field: &Field, value: u64) {
        self.0.insert(field.name().into(), value.into());
    }
    fn record_bool(&mut self, field: &Field, value: bool) {
        self.0.insert(field.name().into(), value.into());
    }
    fn record_f64(&mut self, field: &Field, value: f64) {
        self.0.insert(field.name().into(), Number::from_f64(value).map_or(Value::Null, Value::Number));
    }
    fn record_error(&mut self, field: &Field, value: &(dyn std::error::Error + 'static)) {
        self.0.insert(field.name().into(), value.to_string().into());
    }
}
