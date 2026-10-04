//! The `featuregate` binary: `serve`, `healthcheck`, `export-kind`, `version`.

use std::io::{Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::path::PathBuf;
use std::process::ExitCode;
use std::time::Duration;

use clap::{Parser, Subcommand};
use featuregate::config::Config;
use featuregate::kind;
use featuregate::metrics::Metrics;
use featuregate::service::Service;
use featuregate::telemetry::{Exporter, Profiler, Stdout, Telemetry};

#[derive(Parser)]
#[command(name = "featuregate", about = "An OFREP feature-flag service whose rollouts are Sigil policies", disable_version_flag = true)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Serve the OFREP API, configured by FEATUREGATE_* environment variables
    Serve,
    /// Exit 0 when the local instance's /readyz answers 200; the container's health check
    Healthcheck,
    /// Print the FeatureRollout kind file, or write it with --out
    ExportKind {
        /// Write the kind file here instead of printing it
        #[arg(long)]
        out: Option<PathBuf>,
        /// With --out: fail instead of writing when the file differs
        #[arg(long, requires = "out")]
        check: bool,
    },
    /// Print the service and Sigil engine versions
    Version,
}

fn main() -> ExitCode {
    match Cli::parse().command {
        Command::Serve => serve(),
        Command::Healthcheck => healthcheck(),
        Command::ExportKind { out, check } => export_kind(out.as_ref(), check),
        Command::Version => version(),
    }
}

fn serve() -> ExitCode {
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("{e}");
            return ExitCode::from(2);
        }
    };
    let runtime = tokio::runtime::Builder::new_multi_thread().enable_all().build().expect("a tokio runtime builds");
    match runtime.block_on(run(config)) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("featuregate: {e}");
            ExitCode::FAILURE
        }
    }
}

async fn run(config: Config) -> Result<(), String> {
    let exporter = config.otlp_endpoint.clone().map_or(Exporter::None, Exporter::Otlp);
    let telemetry = Telemetry::new(&config, exporter, Stdout)?;
    tracing::dispatcher::set_global_default(telemetry.dispatch()).map_err(|e| format!("installing the logger: {e}"))?;

    let profiler = config.pyroscope_address.as_deref().and_then(|address| match Profiler::start(address, &config.version) {
        Ok(p) => {
            tracing::info!(server = address, "profiling on");
            Some(p)
        }
        // Profiling is diagnostics: a missing Pyroscope doesn't stop flag serving.
        Err(e) => {
            tracing::warn!(error = %e, server = address, "profiling is off: couldn't start the Pyroscope agent");
            None
        }
    });

    let metrics = Metrics::new(&config.version);
    // Startup runs before anything is served, so blocking here costs nothing: the
    // module loads precompiled in milliseconds (it compiles, taking seconds of CPU,
    // only when fuel metering is on), and the first bundle compiles every flag in
    // every instance.
    let addr = config.bind_addr();
    let module = sigil::Module::bundled_with(sigil::ModuleConfig { fuel: config.evaluation_fuel.is_some() }).map_err(|e| e.to_string())?;
    let service = Service::new(config, module, metrics)?;
    let listener = tokio::net::TcpListener::bind(&addr)
        .await
        .map_err(|e| format!("can't listen on {addr}: {e}\n  help: set FEATUREGATE_ADDR to a free address, like :8080"))?;
    let served = service.serve(listener).await.map_err(|e| e.to_string());

    if let Some(p) = profiler {
        p.stop();
    }
    telemetry.shutdown();
    served
}

/// A probe with no HTTP client: one request over a TCP socket, which is all
/// `/readyz` on the loopback interface takes, and keeps the image small.
fn healthcheck() -> ExitCode {
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("{e}");
            return ExitCode::from(2);
        }
    };
    let port = config.bind_addr().rsplit(':').next().unwrap_or("8080").to_owned();
    let target = format!("127.0.0.1:{port}");
    let probe = || -> Result<bool, String> {
        let addr = target.to_socket_addrs().map_err(|e| e.to_string())?.next().ok_or("no address")?;
        let mut stream = TcpStream::connect_timeout(&addr, Duration::from_secs(2)).map_err(|e| e.to_string())?;
        stream.set_read_timeout(Some(Duration::from_secs(2))).map_err(|e| e.to_string())?;
        stream
            .write_all(format!("GET /readyz HTTP/1.1\r\nHost: {target}\r\nConnection: close\r\n\r\n").as_bytes())
            .map_err(|e| e.to_string())?;
        let mut head = [0u8; 12];
        stream.read_exact(&mut head).map_err(|e| e.to_string())?;
        Ok(&head == b"HTTP/1.1 200")
    };
    match probe() {
        Ok(true) => ExitCode::SUCCESS,
        Ok(false) => {
            eprintln!("healthcheck: {target}/readyz is not ready");
            ExitCode::FAILURE
        }
        Err(e) => {
            eprintln!("healthcheck: can't reach {target}/readyz: {e}");
            ExitCode::FAILURE
        }
    }
}

fn export_kind(out: Option<&PathBuf>, check: bool) -> ExitCode {
    let schema = kind::schema();
    let Some(out) = out else {
        print!("{schema}");
        return ExitCode::SUCCESS;
    };
    if check {
        return match std::fs::read_to_string(out) {
            Ok(current) if current == schema => ExitCode::SUCCESS,
            Ok(_) => {
                eprintln!("{} is stale; run `featuregate export-kind` and commit the result", out.display());
                ExitCode::FAILURE
            }
            Err(e) => {
                eprintln!("can't read {}: {e}", out.display());
                ExitCode::FAILURE
            }
        };
    }
    match std::fs::write(out, schema) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("can't write {}: {e}", out.display());
            ExitCode::FAILURE
        }
    }
}

fn version() -> ExitCode {
    println!("featuregate {}", env!("CARGO_PKG_VERSION"));
    match sigil::Sigil::bundled().and_then(|s| s.version()) {
        Ok(v) => println!("sigil {} ({}, {})", v.version, v.go_version, v.platform),
        Err(e) => {
            eprintln!("can't read the Sigil engine's version: {e}");
            return ExitCode::FAILURE;
        }
    }
    ExitCode::SUCCESS
}
