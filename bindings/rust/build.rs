// Finds sigil.wasm for the `bundled` feature and copies it next to the build's
// output, where src/bundled.rs includes it.
//
// Lookup order: $SIGIL_WASM, then ../../dist/wasm/sigil.wasm (what
// `mise run wasm-build` writes, relative to this crate in the Sigil repository).
// A published crate would ship the module inside the package, at
// `module/sigil.wasm`, and this script would prefer that path; the repository
// checkout has no such file and uses the build output.

use std::env;
use std::fs;
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-env-changed=SIGIL_WASM");
    println!("cargo:rerun-if-changed=build.rs");
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

    let out = PathBuf::from(env::var_os("OUT_DIR").expect("cargo sets OUT_DIR")).join("sigil.wasm");
    fs::copy(found, &out).unwrap_or_else(|err| panic!("copying {} to {}: {err}", found.display(), out.display()));
}
