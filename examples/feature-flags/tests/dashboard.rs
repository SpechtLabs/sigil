//! The Grafana dashboard the compose stack provisions. Grafana loads it as it
//! is, so these tests are what catch a hand edit that breaks the JSON, stacks
//! one panel on another, or queries a metric featuregate doesn't export: such
//! a panel is empty in Grafana, without an error.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::PathBuf;

use featuregate::metrics::Metrics;
use serde_json::Value;

/// The width of Grafana's dashboard grid, in columns.
const GRID_WIDTH: u64 = 24;

/// Labels every scraped series carries besides its own: Alloy's job and
/// instance, and le on a histogram's buckets.
const SCRAPE_LABELS: &[&str] = &["job", "instance", "le"];

/// The datasources deploy/grafana/provisioning defines, by uid.
const DATASOURCES: &[&str] = &["mimir", "tempo", "loki", "pyroscope"];

/// The profile type pyroscope-rs uploads with its pprof-rs backend, as
/// Pyroscope names it: CPU time. Go's goroutines or Node's wall:cpu would
/// show nothing for this service.
const PROFILE_TYPES: &[&str] = &["process_cpu:cpu:nanoseconds:cpu:nanoseconds"];

/// The metric contract: every series featuregate exports that the dashboard
/// may query, with its labels. `contract_matches_the_registry` checks this
/// table against the real registry, so the two can't drift apart.
const CONTRACT: &[(&str, &[&str])] = &[
    ("featuregate_evaluations_total", &["flag", "decision", "reason"]),
    ("featuregate_evaluation_duration_seconds", &["flag"]),
    ("featuregate_evaluation_errors_total", &["kind"]),
    ("featuregate_reloads_total", &["result"]),
    ("featuregate_loaded_info", &["source", "digest"]),
    ("featuregate_flags_loaded", &[]),
    ("featuregate_killed_flags_unmatched", &[]),
    ("featuregate_pool_replacements_total", &[]),
    ("featuregate_http_requests_total", &["method", "route", "status"]),
    ("featuregate_http_request_duration_seconds", &["method", "route"]),
    ("featuregate_http_requests_in_flight", &[]),
    ("featuregate_ready", &[]),
    ("featuregate_build_info", &["version"]),
];

/// What the prometheus crate's process collector exports, on Linux, where the
/// image runs. They have no labels and don't exist in a test on macOS, so the
/// registry check can't cover them.
const PROCESS: &[&str] = &[
    "process_cpu_seconds_total",
    "process_resident_memory_bytes",
    "process_virtual_memory_bytes",
    "process_open_fds",
    "process_max_fds",
    "process_threads",
    "process_start_time_seconds",
];

fn dashboards() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("deploy/grafana")
}

fn dashboard() -> (String, Value) {
    let raw = fs::read_to_string(dashboards().join("dashboards/featuregate.json")).expect("read the dashboard");
    let parsed = serde_json::from_str(&raw).expect("the dashboard is JSON");
    (raw, parsed)
}

/// Every panel, rows included, in file order.
fn panels(dashboard: &Value) -> Vec<&Value> {
    dashboard["panels"].as_array().expect("panels").iter().collect()
}

fn title(panel: &Value) -> &str {
    panel["title"].as_str().unwrap_or("?")
}

/// Every query expression of a panel.
fn exprs(panel: &Value) -> Vec<&str> {
    panel["targets"].as_array().map(|ts| ts.iter().filter_map(|t| t["expr"].as_str()).collect()).unwrap_or_default()
}

/// One series selector found in a PromQL expression.
struct Selector<'a> {
    name: &'a str,
    labels: Vec<&'a str>,
}

/// Finds the selectors on featuregate_* and process_* series in an expression,
/// with the labels their matchers name. It scans rather than parses PromQL,
/// which is enough for the queries this dashboard writes.
fn selectors(expr: &str) -> Vec<Selector<'_>> {
    let bytes = expr.as_bytes();
    let is_ident = |b: u8| b.is_ascii_alphanumeric() || b == b'_';
    let mut found = Vec::new();
    let mut i = 0;
    while i < bytes.len() {
        if !is_ident(bytes[i]) || (i > 0 && is_ident(bytes[i - 1])) {
            i += 1;
            continue;
        }
        let start = i;
        while i < bytes.len() && is_ident(bytes[i]) {
            i += 1;
        }
        let name = &expr[start..i];
        if !(name.starts_with("featuregate_") || name.starts_with("process_")) {
            continue;
        }
        let mut labels = Vec::new();
        if bytes.get(i) == Some(&b'{') {
            let end = i + expr[i..].find('}').expect("a selector closes its braces");
            for matcher in split_matchers(&expr[i + 1..end]) {
                let label: &str = matcher.split(['=', '!', '~']).next().unwrap_or("").trim();
                if !label.is_empty() {
                    labels.push(label);
                }
            }
            i = end;
        }
        found.push(Selector { name, labels });
    }
    found
}

/// Splits the inside of a selector's braces at the commas outside quotes.
fn split_matchers(inside: &str) -> Vec<&str> {
    let mut parts = Vec::new();
    let (mut start, mut quoted, mut escaped) = (0, false, false);
    for (i, c) in inside.char_indices() {
        match c {
            _ if escaped => escaped = false,
            '\\' if quoted => escaped = true,
            '"' => quoted = !quoted,
            ',' if !quoted => {
                parts.push(&inside[start..i]);
                start = i + 1;
            }
            _ => {}
        }
    }
    parts.push(&inside[start..]);
    parts
}

/// Strips the suffix Prometheus adds to a histogram's series, so a _bucket
/// query finds its histogram.
fn series<'a>(name: &'a str, known: &BTreeMap<&str, Vec<&str>>) -> &'a str {
    for suffix in ["_bucket", "_sum", "_count"] {
        if let Some(base) = name.strip_suffix(suffix)
            && known.contains_key(base)
        {
            return base;
        }
    }
    name
}

fn known() -> BTreeMap<&'static str, Vec<&'static str>> {
    let mut known: BTreeMap<_, _> = CONTRACT.iter().map(|(n, l)| (*n, l.to_vec())).collect();
    known.extend(PROCESS.iter().map(|n| (*n, Vec::new())));
    known
}

#[test]
fn has_the_uid_the_readme_links_to() {
    assert_eq!(dashboard().1["uid"], "featuregate");
}

#[test]
fn gives_every_panel_its_own_id_and_keeps_every_row_open() {
    let (_, d) = dashboard();
    let ids: Vec<_> = panels(&d).iter().map(|p| p["id"].as_u64().expect("an id")).collect();
    assert_eq!(ids.iter().collect::<BTreeSet<_>>().len(), ids.len(), "ids repeat: {ids:?}");
    // A collapsed row carries its panels inline, at the positions they take
    // once it opens, which the overlap check below can't see.
    let collapsed: Vec<_> = panels(&d).into_iter().filter(|p| p["panels"].as_array().is_some_and(|a| !a.is_empty())).map(title).collect();
    assert!(collapsed.is_empty(), "collapsed rows: {collapsed:?}");
}

#[test]
fn every_panel_lies_inside_the_grid() {
    let (_, d) = dashboard();
    let mut wrong = Vec::new();
    for p in panels(&d) {
        let g = &p["gridPos"];
        let (x, w, h) = (g["x"].as_u64().unwrap(), g["w"].as_u64().unwrap(), g["h"].as_u64().unwrap());
        if w == 0 || h == 0 || x + w > GRID_WIDTH {
            wrong.push(title(p));
        }
    }
    assert!(wrong.is_empty(), "outside the grid: {wrong:?}");
}

#[test]
fn stacks_no_panel_on_another() {
    let (_, d) = dashboard();
    let cells: Vec<_> = panels(&d)
        .into_iter()
        .map(|p| {
            let g = &p["gridPos"];
            let n = |k: &str| g[k].as_u64().unwrap();
            (title(p), n("x"), n("y"), n("w"), n("h"))
        })
        .collect();
    let mut overlaps = Vec::new();
    for (i, a) in cells.iter().enumerate() {
        for b in &cells[i + 1..] {
            if a.1 < b.1 + b.3 && b.1 < a.1 + a.3 && a.2 < b.2 + b.4 && b.2 < a.2 + a.4 {
                overlaps.push(format!("{} and {}", a.0, b.0));
            }
        }
    }
    assert!(overlaps.is_empty(), "overlapping panels: {overlaps:?}");
}

#[test]
fn queries_only_metrics_featuregate_exports_with_labels_they_carry() {
    let (_, d) = dashboard();
    let known = known();
    let mut wrong = Vec::new();
    for p in panels(&d) {
        for expr in exprs(p) {
            for s in selectors(expr) {
                let name = series(s.name, &known);
                let Some(labels) = known.get(name) else {
                    wrong.push(format!("{} queries {}, which featuregate doesn't export", title(p), s.name));
                    continue;
                };
                for label in s.labels {
                    if !labels.contains(&label) && !SCRAPE_LABELS.contains(&label) {
                        wrong.push(format!("{} matches label {label} on {name}, which has only {labels:?}", title(p)));
                    }
                }
            }
        }
    }
    assert!(wrong.is_empty(), "{wrong:#?}");
}

#[test]
fn shows_every_featuregate_metric_on_some_panel() {
    let (_, d) = dashboard();
    let known = known();
    let queried: BTreeSet<_> =
        panels(&d).into_iter().flat_map(exprs).flat_map(selectors).map(|s| series(s.name, &known).to_owned()).collect();
    // A metric no panel shows is one an operator can't see without writing
    // the query themselves.
    let missing: Vec<_> = CONTRACT.iter().map(|(n, _)| *n).filter(|n| !queried.contains(*n)).collect();
    assert!(missing.is_empty(), "no panel shows {missing:?}");
}

#[test]
fn panels_and_variables_use_provisioned_datasources() {
    let (raw, _) = dashboard();
    let datasources = fs::read_to_string(dashboards().join("provisioning/datasources/datasources.yaml")).unwrap();
    for uid in DATASOURCES {
        assert!(datasources.contains(&format!("uid: {uid}\n")), "datasource {uid} is not provisioned");
    }
    let (_, d) = dashboard();
    let mut wrong = BTreeSet::new();
    let mut visit = |v: &Value| {
        if let Some(uid) = v["datasource"]["uid"].as_str()
            && !DATASOURCES.contains(&uid)
        {
            wrong.insert(uid.to_owned());
        }
    };
    for p in panels(&d) {
        visit(p);
        for t in p["targets"].as_array().into_iter().flatten() {
            visit(t);
        }
    }
    for v in d["templating"]["list"].as_array().unwrap() {
        visit(v);
    }
    assert!(wrong.is_empty(), "unprovisioned datasource uids {wrong:?}");
    assert!(raw.contains("\"uid\": \"mimir\""), "the dashboard queries Mimir");
}

#[test]
fn profile_queries_ask_only_for_types_the_service_uploads() {
    let (raw, d) = dashboard();
    let links: Vec<String> = d["links"].as_array().unwrap().iter().map(|l| urlencoding_decode(l["url"].as_str().unwrap())).collect();
    let datasources = fs::read_to_string(dashboards().join("provisioning/datasources/datasources.yaml")).unwrap();
    for (where_, text) in [
        ("the dashboard's panels and variables", raw.clone()),
        ("the dashboard's links", links.join("\n")),
        ("the Tempo datasource's traces-to-profiles link", datasources),
    ] {
        let asked: BTreeSet<_> = profile_types(&text).collect();
        assert!(!asked.is_empty(), "{where_} ask for no profile");
        let wrong: Vec<_> = asked.iter().filter(|t| !PROFILE_TYPES.contains(&t.as_str())).collect();
        assert!(wrong.is_empty(), "{where_} ask for {wrong:?}; the service uploads {PROFILE_TYPES:?}");
    }
}

/// The profile type ids in a text: name, sample type and unit, period type and
/// unit, joined by colons.
fn profile_types(text: &str) -> impl Iterator<Item = String> + '_ {
    text.split(|c: char| !(c.is_ascii_alphanumeric() || c == '_' || c == ':'))
        .filter(|w| w.split(':').count() == 5 && w.split(':').all(|p| !p.is_empty()))
        .map(str::to_owned)
}

/// Percent-decodes an Explore link, whose queries are JSON inside the URL.
fn urlencoding_decode(url: &str) -> String {
    let bytes = url.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%'
            && i + 2 < bytes.len()
            && let Ok(byte) = u8::from_str_radix(&url[i + 1..i + 3], 16)
        {
            out.push(byte);
            i += 3;
        } else {
            out.push(if bytes[i] == b'+' { b' ' } else { bytes[i] });
            i += 1;
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// Dummy values for labels, one per label name.
fn values(labels: &[&str]) -> Vec<String> {
    labels.iter().map(|l| format!("{l}-value")).collect()
}

fn refs(values: &[String]) -> Vec<&str> {
    values.iter().map(String::as_str).collect()
}

/// Pins the table above to the real registry: each series is recorded with
/// the contract's labels, which panics on a wrong arity, and the exposition
/// must then carry exactly those label names.
#[test]
fn contract_matches_the_registry() {
    let m = Metrics::new("test");
    let l = |name: &str| CONTRACT.iter().find(|(n, _)| *n == name).unwrap().1;

    let v = values(l("featuregate_evaluations_total"));
    m.evaluations.with_label_values(&refs(&v)).inc();
    let v = values(l("featuregate_evaluation_duration_seconds"));
    m.evaluation_duration.with_label_values(&refs(&v)).observe(0.001);
    let v = values(l("featuregate_evaluation_errors_total"));
    m.evaluation_errors.with_label_values(&refs(&v)).inc();
    let v = values(l("featuregate_reloads_total"));
    m.reloads.with_label_values(&refs(&v)).inc();
    let v = values(l("featuregate_loaded_info"));
    m.loaded_info.with_label_values(&refs(&v)).set(1);
    let v = values(l("featuregate_http_requests_total"));
    m.http_requests.with_label_values(&refs(&v)).inc();
    let v = values(l("featuregate_http_request_duration_seconds"));
    m.http_duration.with_label_values(&refs(&v)).observe(0.001);

    let text = m.render();
    for (name, labels) in CONTRACT {
        let lines: Vec<_> = text
            .lines()
            .filter(|line| !line.starts_with('#'))
            .filter(|line| {
                line.strip_prefix(name).is_some_and(|rest| rest.starts_with(['{', ' ']))
                    || ["_bucket", "_sum", "_count"].iter().any(|s| line.strip_prefix(&format!("{name}{s}")).is_some())
            })
            .collect();
        assert!(!lines.is_empty(), "the registry exports no series named {name}");
        for line in lines {
            let exported: BTreeSet<&str> = match (line.find('{'), line.find('}')) {
                (Some(a), Some(b)) => {
                    split_matchers(&line[a + 1..b]).into_iter().filter_map(|p| p.split('=').next()).filter(|l| *l != "le").collect()
                }
                _ => BTreeSet::new(),
            };
            assert_eq!(exported, labels.iter().copied().collect::<BTreeSet<_>>(), "labels of {name}: {line}");
        }
    }
}
