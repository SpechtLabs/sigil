// Finds sigil.wasm for the `bundled` feature and copies it next to the build's
// output, where src/bundled.rs includes it. With the `precompiled` feature it
// also compiles the module for the target, into sigil.cwasm.
//
// Lookup order: $SIGIL_WASM, then module/sigil.wasm (in the published package),
// then ../../dist/wasm/sigil.wasm (what `mise run wasm-build` writes, relative
// to this crate in the Sigil repository).

use std::env;
use std::fs;
#[cfg(feature = "precompiled")]
use std::path::Path;
use std::path::PathBuf;

// The engine configuration of the runtime, shared so the artifact built here
// is one the runtime's engine accepts.
#[cfg(feature = "precompiled")]
#[path = "src/engine.rs"]
mod engine;

fn main() {
    println!("cargo:rerun-if-env-changed=SIGIL_WASM");
    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-changed=src/engine.rs");
    if env::var_os("CARGO_FEATURE_BUNDLED").is_none() {
        return;
    }

    let manifest = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").expect("cargo sets CARGO_MANIFEST_DIR"));
    let candidates: Vec<PathBuf> = match env::var_os("SIGIL_WASM") {
        Some(path) => vec![PathBuf::from(path)],
        None => vec![manifest.join("module/sigil.wasm"), manifest.join("../../dist/wasm/sigil.wasm")],
    };
    let Some(found) = candidates.iter().find(|p| p.is_file()) else {
        let tried: Vec<String> = candidates.iter().map(|p| p.display().to_string()).collect();
        panic!(
            "sigil.wasm not found (looked at {}).\n  help: build it with `mise run wasm-build` from the Sigil repository, \
             point SIGIL_WASM at a module, or disable the default `bundled` feature and load the module yourself \
             with sigil::Module::from_file",
            tried.join(", ")
        );
    };
    println!("cargo:rerun-if-changed={}", found.display());

    let out_dir = PathBuf::from(env::var_os("OUT_DIR").expect("cargo sets OUT_DIR"));
    let out = out_dir.join("sigil.wasm");
    fs::copy(found, &out).unwrap_or_else(|err| panic!("copying {} to {}: {err}", found.display(), out.display()));

    #[cfg(feature = "precompiled")]
    precompile(&out, &out_dir.join("sigil.cwasm"));
}

/// Compiles the module for `$TARGET` into `out`. An empty file when it can't,
/// with a warning that says why: the crate then compiles at run time instead.
#[cfg(feature = "precompiled")]
fn precompile(wasm: &Path, out: &Path) {
    let bytes = match compile(wasm) {
        Ok(bytes) => bytes,
        Err(why) => {
            println!("cargo:warning=spechtlabs-sigil: not precompiling the module, Module::bundled() will compile it at run time: {why}");
            Vec::new()
        }
    };
    fs::write(out, bytes).unwrap_or_else(|err| panic!("writing {}: {err}", out.display()));
}

#[cfg(feature = "precompiled")]
fn compile(wasm: &Path) -> Result<Vec<u8>, String> {
    let target = env::var("TARGET").map_err(|e| e.to_string())?;
    let mut config = engine::config(false);
    // Only a cross build names its target. For the host, wasmtime detects the
    // CPU like the runtime's engine does, so the artifact uses the same
    // instructions a JIT would; naming it would compile for the baseline ISA.
    if env::var("HOST").ok().as_deref() != Some(target.as_str()) {
        config.target(&target).map_err(|e| format!("wasmtime can't target {target}: {e}"))?;
    }
    let engine = wasmtime::Engine::new(&config).map_err(|e| e.to_string())?;
    let wasm = fs::read(wasm).map_err(|e| e.to_string())?;
    engine.precompile_module(&wasm).map_err(|e| e.to_string())
}
