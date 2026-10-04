//! Prometheus metrics, all prefixed `featuregate_`. Labels are bounded: a
//! flag label only ever holds a key that has a loaded policy (an unknown
//! flag is `-`), and routes are templates, never raw paths.

use prometheus::{Encoder, HistogramOpts, HistogramVec, IntCounter, IntCounterVec, IntGauge, IntGaugeVec, Opts, Registry, TextEncoder};

/// The label value for "no flag": a request that named an unknown one, or a
/// bulk evaluation.
pub const NO_FLAG: &str = "-";

/// Evaluation latencies are microseconds to tens of milliseconds.
const EVAL_BUCKETS: &[f64] = &[0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25];
const HTTP_BUCKETS: &[f64] = &[0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5];

/// Every metric of the process, owned by the service rather than a global
/// registry, so two services in one test process don't share counters.
#[derive(Clone)]
pub struct Metrics {
    registry: Registry,
    pub evaluations: IntCounterVec,
    pub evaluation_duration: HistogramVec,
    pub evaluation_errors: IntCounterVec,
    pub reloads: IntCounterVec,
    pub loaded_info: IntGaugeVec,
    pub flags_loaded: IntGauge,
    pub killed_flags_unmatched: IntGauge,
    pub pool_replacements: IntCounter,
    pub http_requests: IntCounterVec,
    pub http_duration: HistogramVec,
    pub http_in_flight: IntGauge,
    pub ready: IntGauge,
    pub module_precompiled: IntGauge,
    pub pool_rebuilding: IntGauge,
}

impl Metrics {
    pub fn new(version: &str) -> Self {
        let registry = Registry::new();
        let evaluations = IntCounterVec::new(
            Opts::new("featuregate_evaluations_total", "Flag evaluations, by flag, Sigil decision and reason"),
            &["flag", "decision", "reason"],
        )
        .unwrap();
        let evaluation_duration = HistogramVec::new(
            HistogramOpts::new(
                "featuregate_evaluation_duration_seconds",
                "Time one flag evaluation takes, waiting for a pool instance included",
            )
            .buckets(EVAL_BUCKETS.to_vec()),
            &["flag"],
        )
        .unwrap();
        let evaluation_errors = IntCounterVec::new(
            Opts::new(
                "featuregate_evaluation_errors_total",
                "Evaluations that failed and answered off, by kind: timeout, conflict, assertion, runtime, stopped, busy, internal",
            ),
            &["kind"],
        )
        .unwrap();
        let reloads =
            IntCounterVec::new(Opts::new("featuregate_reloads_total", "Policy reloads, by result: loaded, unchanged, failed"), &["result"])
                .unwrap();
        let loaded_info = IntGaugeVec::new(
            Opts::new("featuregate_loaded_info", "1 for the policy bundle being served: its source and content digest"),
            &["source", "digest"],
        )
        .unwrap();
        let flags_loaded = IntGauge::new("featuregate_flags_loaded", "Flags the loaded bundle serves").unwrap();
        let killed_flags_unmatched = IntGauge::new(
            "featuregate_killed_flags_unmatched",
            "Names in FEATUREGATE_KILLED_FLAGS that no loaded flag has: a kill switch that switches nothing off",
        )
        .unwrap();
        let pool_replacements =
            IntCounter::new("featuregate_pool_replacements_total", "Sigil instances replaced after they stopped").unwrap();
        let http_requests = IntCounterVec::new(
            Opts::new("featuregate_http_requests_total", "HTTP requests, by method, route template and status"),
            &["method", "route", "status"],
        )
        .unwrap();
        let http_duration = HistogramVec::new(
            HistogramOpts::new("featuregate_http_request_duration_seconds", "HTTP request duration, by method and route template")
                .buckets(HTTP_BUCKETS.to_vec()),
            &["method", "route"],
        )
        .unwrap();
        let http_in_flight = IntGauge::new("featuregate_http_requests_in_flight", "HTTP requests being served").unwrap();
        let pool_rebuilding = IntGauge::new(
            "featuregate_pool_rebuilding",
            "Pool instances being rebuilt after a kill or trap; the pool serves with that many fewer meanwhile",
        )
        .unwrap();
        let module_precompiled =
            IntGauge::new("featuregate_module_precompiled", "1 when the Sigil module loaded precompiled, 0 when this start compiled it")
                .unwrap();
        let ready = IntGauge::new("featuregate_ready", "1 while /readyz answers 200").unwrap();
        let build_info =
            IntGaugeVec::new(Opts::new("featuregate_build_info", "1, labelled with the running version"), &["version"]).unwrap();
        build_info.with_label_values(&[version]).set(1);

        let collectors: Vec<Box<dyn prometheus::core::Collector>> = vec![
            Box::new(evaluations.clone()),
            Box::new(evaluation_duration.clone()),
            Box::new(evaluation_errors.clone()),
            Box::new(reloads.clone()),
            Box::new(loaded_info.clone()),
            Box::new(flags_loaded.clone()),
            Box::new(killed_flags_unmatched.clone()),
            Box::new(pool_replacements.clone()),
            Box::new(http_requests.clone()),
            Box::new(http_duration.clone()),
            Box::new(http_in_flight.clone()),
            Box::new(ready.clone()),
            Box::new(module_precompiled.clone()),
            Box::new(pool_rebuilding.clone()),
            Box::new(build_info),
        ];
        // Process metrics (CPU, memory, file descriptors) read /proc, so Linux only.
        #[cfg(target_os = "linux")]
        registry.register(Box::new(prometheus::process_collector::ProcessCollector::for_self())).expect("metric names are unique");
        for c in collectors {
            registry.register(c).expect("metric names are unique");
        }
        Self {
            registry,
            evaluations,
            evaluation_duration,
            evaluation_errors,
            reloads,
            loaded_info,
            flags_loaded,
            killed_flags_unmatched,
            pool_replacements,
            http_requests,
            http_duration,
            http_in_flight,
            ready,
            module_precompiled,
            pool_rebuilding,
        }
    }

    /// The exposition text `/metrics` serves.
    pub fn render(&self) -> String {
        let mut buf = Vec::new();
        TextEncoder::new().encode(&self.registry.gather(), &mut buf).expect("text encoding can't fail");
        String::from_utf8(buf).expect("the text format is UTF-8")
    }

    /// Records which bundle is being served, dropping the previous one's series.
    pub fn set_loaded(&self, source: &str, digest: &str, flags: usize) {
        self.loaded_info.reset();
        self.loaded_info.with_label_values(&[source, digest]).set(1);
        self.flags_loaded.set(flags as i64);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn renders_every_family_once_used() {
        let m = Metrics::new("1.2.3");
        m.evaluations.with_label_values(&["dark-mode", "enable", "rollout"]).inc();
        m.evaluation_duration.with_label_values(&["dark-mode"]).observe(0.0002);
        m.evaluation_errors.with_label_values(&["timeout"]).inc();
        m.reloads.with_label_values(&["loaded"]).inc();
        m.set_loaded("directory", "abc123", 4);
        m.pool_replacements.inc();
        m.http_requests.with_label_values(&["POST", "/ofrep/v1/evaluate/flags/{key}", "200"]).inc();
        m.http_duration.with_label_values(&["POST", "/ofrep/v1/evaluate/flags/{key}"]).observe(0.001);
        let text = m.render();
        for name in [
            "featuregate_evaluations_total{decision=\"enable\",flag=\"dark-mode\",reason=\"rollout\"} 1",
            "featuregate_evaluation_duration_seconds_bucket",
            "featuregate_evaluation_errors_total{kind=\"timeout\"} 1",
            "featuregate_reloads_total{result=\"loaded\"} 1",
            "featuregate_loaded_info{digest=\"abc123\",source=\"directory\"} 1",
            "featuregate_flags_loaded 4",
            "featuregate_killed_flags_unmatched 0",
            "featuregate_pool_replacements_total 1",
            "featuregate_http_requests_total{method=\"POST\",route=\"/ofrep/v1/evaluate/flags/{key}\",status=\"200\"} 1",
            "featuregate_http_request_duration_seconds_bucket",
            "featuregate_http_requests_in_flight 0",
            "featuregate_ready 0",
            "featuregate_module_precompiled 0",
            "featuregate_pool_rebuilding 0",
            "featuregate_build_info{version=\"1.2.3\"} 1",
        ] {
            assert!(text.contains(name), "missing {name} in:\n{text}");
        }
    }

    #[test]
    fn a_reload_replaces_the_info_series() {
        let m = Metrics::new("dev");
        m.set_loaded("directory", "old", 3);
        m.set_loaded("directory", "new", 4);
        let text = m.render();
        assert!(!text.contains("digest=\"old\""), "{text}");
        assert!(text.contains("digest=\"new\""));
    }

    #[test]
    fn registries_are_per_instance() {
        let a = Metrics::new("dev");
        let b = Metrics::new("dev");
        a.reloads.with_label_values(&["loaded"]).inc();
        assert!(!b.render().contains("featuregate_reloads_total{result=\"loaded\"}"));
    }
}
