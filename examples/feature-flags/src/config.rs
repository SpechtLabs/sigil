//! Configuration: every setting is a `FEATUREGATE_*` environment variable,
//! checked once at startup with advice on what to set. Durations use Go's
//! syntax (`30s`, `1m30s`), like the other example services.

use std::collections::BTreeSet;
use std::path::PathBuf;
use std::time::Duration;

use crate::error::ConfigError;

/// The validated settings of one process.
#[derive(Debug, Clone, PartialEq)]
pub struct Config {
    /// Listen address of the one HTTP port.
    pub addr: String,
    /// Flag policies directory; `None` serves the bundle compiled into the binary.
    pub policies: Option<PathBuf>,
    /// How often the policies directory is polled; zero disables polling.
    pub reload_interval: Duration,
    /// How long a graceful shutdown waits for requests in flight.
    pub shutdown_timeout: Duration,
    /// How long one evaluation may take.
    pub evaluation_timeout: Duration,
    /// Fuel one evaluation may use, on top of the deadline; `None` meters none.
    /// Needs a module built with fuel, which `main` does when this is set.
    pub evaluation_fuel: Option<u64>,
    /// Instances in the evaluation pool.
    pub workers: usize,
    /// Flags switched off for everyone: the platform kill switch.
    pub killed_flags: BTreeSet<String>,
    pub log_format: LogFormat,
    pub debug: bool,
    /// The version on logs, spans and profiles.
    pub version: String,
    /// OTLP traces endpoint; `None` leaves tracing off.
    pub otlp_endpoint: Option<String>,
    /// Pyroscope server; `None` leaves profiling off.
    pub pyroscope_address: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LogFormat {
    Json,
    Console,
}

impl Config {
    /// Reads the process environment.
    pub fn from_env() -> Result<Self, ConfigError> {
        Self::from_lookup(|name| std::env::var(name).ok())
    }

    /// Reads settings through `lookup`, which tests replace. Every problem is
    /// reported at once, so one restart fixes them all.
    pub fn from_lookup(lookup: impl Fn(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let mut problems = Vec::new();
        let get = |name: &str| lookup(name).map(|v| v.trim().to_owned()).filter(|v| !v.is_empty());

        let addr = get("FEATUREGATE_ADDR").unwrap_or_else(|| ":8080".to_owned());
        if let Err(e) = normalize_addr(&addr) {
            problems.push(format!("FEATUREGATE_ADDR: {e}"));
        }
        let mut duration = |name: &str, default: Duration, allow_zero: bool| -> Duration {
            match get(name) {
                None => default,
                Some(v) => match parse_duration(&v) {
                    Ok(d) if d.is_zero() && !allow_zero => {
                        problems.push(format!("{name}: {v:?} is zero; set a positive duration such as 50ms"));
                        default
                    }
                    Ok(d) => d,
                    Err(e) => {
                        problems.push(format!("{name}: {e}"));
                        default
                    }
                },
            }
        };
        let reload_interval = duration("FEATUREGATE_RELOAD_INTERVAL", Duration::from_secs(30), true);
        let shutdown_timeout = duration("FEATUREGATE_SHUTDOWN_TIMEOUT", Duration::from_secs(15), false);
        let evaluation_timeout = duration("FEATUREGATE_EVALUATION_TIMEOUT", Duration::from_millis(50), false);

        let evaluation_fuel = match get("FEATUREGATE_EVALUATION_FUEL") {
            None => None,
            Some(v) => match v.parse::<u64>() {
                Ok(n) if n > 0 => Some(n),
                _ => {
                    problems.push(format!(
                        "FEATUREGATE_EVALUATION_FUEL: {v:?} is not a positive whole number; leave it unset to meter no fuel"
                    ));
                    None
                }
            },
        };

        let workers = match get("FEATUREGATE_WORKERS") {
            None => default_workers(),
            Some(v) => match v.parse::<usize>() {
                Ok(n) if (1..=64).contains(&n) => n,
                _ => {
                    problems.push(format!(
                        "FEATUREGATE_WORKERS: {v:?} is not a whole number from 1 to 64; set it to the number of CPUs you can spare"
                    ));
                    default_workers()
                }
            },
        };

        let mut killed_flags = BTreeSet::new();
        for key in get("FEATUREGATE_KILLED_FLAGS").iter().flat_map(|v| v.split(',')) {
            let key = key.trim();
            if key.is_empty() {
                continue;
            }
            if let Err(e) = crate::flags::validate_key(key) {
                problems.push(format!("FEATUREGATE_KILLED_FLAGS: {e}"));
            }
            killed_flags.insert(key.to_owned());
        }

        let log_format = match get("FEATUREGATE_LOG_FORMAT").as_deref() {
            None | Some("json") => LogFormat::Json,
            Some("console") => LogFormat::Console,
            Some(other) => {
                problems.push(format!("FEATUREGATE_LOG_FORMAT: {other:?} is not json or console"));
                LogFormat::Json
            }
        };
        let debug = match get("FEATUREGATE_DEBUG").as_deref() {
            None | Some("false") | Some("0") => false,
            Some("true") | Some("1") => true,
            Some(other) => {
                problems.push(format!("FEATUREGATE_DEBUG: {other:?} is not true or false"));
                false
            }
        };

        let otlp_endpoint = get("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT").or_else(|| get("OTEL_EXPORTER_OTLP_ENDPOINT"));
        if let Some(e) = &otlp_endpoint
            && !(e.starts_with("http://") || e.starts_with("https://"))
        {
            problems.push(format!("OTEL_EXPORTER_OTLP_ENDPOINT: {e:?} needs a scheme, such as http://alloy:4317"));
        }

        if !problems.is_empty() {
            return Err(ConfigError { problems });
        }
        Ok(Self {
            addr,
            policies: get("FEATUREGATE_POLICIES").map(PathBuf::from),
            reload_interval,
            shutdown_timeout,
            evaluation_timeout,
            evaluation_fuel,
            workers,
            killed_flags,
            log_format,
            debug,
            version: get("FEATUREGATE_VERSION").unwrap_or_else(|| "dev".to_owned()),
            otlp_endpoint,
            pyroscope_address: get("PYROSCOPE_SERVER_ADDRESS"),
        })
    }

    /// The address to bind: `:8080` means every interface, as in Go.
    pub fn bind_addr(&self) -> String {
        normalize_addr(&self.addr).expect("validated at startup")
    }
}

/// Two to four workers by CPUs: evaluations take microseconds, so the pool
/// only has to cover a slow policy holding one instance.
fn default_workers() -> usize {
    std::thread::available_parallelism().map_or(2, |n| n.get()).clamp(2, 4)
}

fn normalize_addr(addr: &str) -> Result<String, String> {
    let full = if addr.starts_with(':') { format!("0.0.0.0{addr}") } else { addr.to_owned() };
    match full.rsplit_once(':') {
        Some((host, port)) if !host.is_empty() && port.parse::<u16>().is_ok() => Ok(full),
        _ => Err(format!("{addr:?} is not host:port; set it like :8080 or 127.0.0.1:8080")),
    }
}

/// Parses Go's duration syntax: a sequence of decimal numbers with units
/// `ns`, `us`, `µs`, `ms`, `s`, `m`, `h`; a bare `0` is zero.
pub fn parse_duration(input: &str) -> Result<Duration, String> {
    let bad = || format!("{input:?} is not a duration; write it like 50ms, 30s or 1m30s");
    if input == "0" {
        return Ok(Duration::ZERO);
    }
    let mut rest = input;
    let mut total = 0f64;
    if rest.is_empty() {
        return Err(bad());
    }
    while !rest.is_empty() {
        let digits = rest.find(|c: char| !(c.is_ascii_digit() || c == '.')).ok_or_else(bad)?;
        if digits == 0 {
            return Err(bad());
        }
        let n: f64 = rest[..digits].parse().map_err(|_| bad())?;
        rest = &rest[digits..];
        let unit_len = rest.find(|c: char| c.is_ascii_digit() || c == '.').unwrap_or(rest.len());
        let secs = match &rest[..unit_len] {
            "ns" => 1e-9,
            "us" | "µs" => 1e-6,
            "ms" => 1e-3,
            "s" => 1.0,
            "m" => 60.0,
            "h" => 3600.0,
            _ => return Err(bad()),
        };
        total += n * secs;
        rest = &rest[unit_len..];
    }
    Ok(Duration::from_secs_f64(total))
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use rstest::rstest;

    use super::*;

    fn lookup(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let map: HashMap<String, String> = pairs.iter().map(|(k, v)| ((*k).to_owned(), (*v).to_owned())).collect();
        move |name| map.get(name).cloned()
    }

    #[rstest]
    #[case("50ms", Some(Duration::from_millis(50)))]
    #[case("30s", Some(Duration::from_secs(30)))]
    #[case("1m30s", Some(Duration::from_secs(90)))]
    #[case("1.5s", Some(Duration::from_millis(1500)))]
    #[case("2h", Some(Duration::from_secs(7200)))]
    #[case("0", Some(Duration::ZERO))]
    #[case("", None)]
    #[case("5", None)]
    #[case("ms", None)]
    #[case("10 s", None)]
    #[case("3d", None)]
    fn durations(#[case] input: &str, #[case] want: Option<Duration>) {
        assert_eq!(parse_duration(input).ok(), want, "{input}");
    }

    #[test]
    fn defaults() {
        let cfg = Config::from_lookup(lookup(&[])).unwrap();
        assert_eq!(cfg.bind_addr(), "0.0.0.0:8080");
        assert_eq!(cfg.evaluation_timeout, Duration::from_millis(50));
        assert_eq!(cfg.reload_interval, Duration::from_secs(30));
        assert_eq!(cfg.policies, None);
        assert!(cfg.killed_flags.is_empty());
        assert_eq!(cfg.log_format, LogFormat::Json);
        assert_eq!(cfg.otlp_endpoint, None);
    }

    #[test]
    fn explicit_values() {
        let cfg = Config::from_lookup(lookup(&[
            ("FEATUREGATE_ADDR", "127.0.0.1:9000"),
            ("FEATUREGATE_POLICIES", "/policies"),
            ("FEATUREGATE_RELOAD_INTERVAL", "0"),
            ("FEATUREGATE_KILLED_FLAGS", "dark-mode, beta-api,"),
            ("FEATUREGATE_WORKERS", "3"),
            ("FEATUREGATE_LOG_FORMAT", "console"),
            ("OTEL_EXPORTER_OTLP_ENDPOINT", "http://alloy:4317"),
            ("PYROSCOPE_SERVER_ADDRESS", "http://pyroscope:4040"),
        ]))
        .unwrap();
        assert_eq!(cfg.bind_addr(), "127.0.0.1:9000");
        assert_eq!(cfg.policies, Some(PathBuf::from("/policies")));
        assert!(cfg.reload_interval.is_zero());
        assert_eq!(cfg.killed_flags.iter().map(String::as_str).collect::<Vec<_>>(), ["beta-api", "dark-mode"]);
        assert_eq!(cfg.workers, 3);
        assert_eq!(cfg.log_format, LogFormat::Console);
        assert_eq!(cfg.otlp_endpoint.as_deref(), Some("http://alloy:4317"));
        assert_eq!(cfg.pyroscope_address.as_deref(), Some("http://pyroscope:4040"));
    }

    #[rstest]
    #[case("FEATUREGATE_ADDR", "8080", "host:port")]
    #[case("FEATUREGATE_ADDR", ":http", "host:port")]
    #[case("FEATUREGATE_EVALUATION_TIMEOUT", "0", "zero")]
    #[case("FEATUREGATE_EVALUATION_TIMEOUT", "fast", "not a duration")]
    #[case("FEATUREGATE_RELOAD_INTERVAL", "-1s", "not a duration")]
    #[case("FEATUREGATE_WORKERS", "0", "1 to 64")]
    #[case("FEATUREGATE_EVALUATION_FUEL", "0", "positive whole number")]
    #[case("FEATUREGATE_WORKERS", "many", "1 to 64")]
    #[case("FEATUREGATE_KILLED_FLAGS", "Bad Flag", "flag key")]
    #[case("FEATUREGATE_LOG_FORMAT", "xml", "json or console")]
    #[case("FEATUREGATE_DEBUG", "yes", "true or false")]
    #[case("OTEL_EXPORTER_OTLP_ENDPOINT", "alloy:4317", "scheme")]
    fn invalid_settings_name_the_variable(#[case] name: &str, #[case] value: &str, #[case] hint: &str) {
        let err = Config::from_lookup(lookup(&[(name, value)])).unwrap_err().to_string();
        assert!(err.contains(name), "{err}");
        assert!(err.contains(hint), "{err}");
    }

    #[test]
    fn every_problem_is_reported_at_once() {
        let err = Config::from_lookup(lookup(&[("FEATUREGATE_WORKERS", "x"), ("FEATUREGATE_DEBUG", "maybe")])).unwrap_err();
        assert_eq!(err.problems.len(), 2);
    }

    #[test]
    fn blank_values_count_as_unset() {
        let cfg = Config::from_lookup(lookup(&[("FEATUREGATE_POLICIES", "  "), ("FEATUREGATE_ADDR", "")])).unwrap();
        assert_eq!(cfg.policies, None);
        assert_eq!(cfg.addr, ":8080");
    }
}
