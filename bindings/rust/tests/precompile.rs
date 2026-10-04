//! Startup: precompiled modules load in milliseconds and decide the same.

mod common;

use common::{deploy_gates, err_of, json, sigil_files};
use sigil::{CompileOptions, Module, ModuleConfig, Sigil};

fn eval_payments(module: &Module) -> sigil::EvalResult {
    let sigil = Sigil::new(module).unwrap();
    let files = sigil_files(&deploy_gates());
    let policy = sigil.compile(&files, CompileOptions { policy: Some("access.main".into()), ..Default::default() }).unwrap();
    policy.eval(&json(&deploy_gates().join("access/testdata/sre.json"))).unwrap()
}

#[test]
fn a_precompiled_module_loads_and_decides_the_same() {
    let compiled = common::module();
    assert!(!compiled.is_precompiled() || cfg!(feature = "precompiled"));
    let bytes = compiled.precompile().unwrap();
    assert!(bytes.len() > 1_000_000, "{}", bytes.len());

    // SAFETY: the bytes are what `precompile` just returned.
    let loaded = unsafe { Module::from_precompiled(&bytes, ModuleConfig::default()) }.unwrap();
    assert!(loaded.is_precompiled());
    assert_eq!(eval_payments(&loaded), eval_payments(compiled));
}

#[test]
fn a_precompiled_file_is_memory_mapped_and_decides_the_same() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("sigil.cwasm");
    std::fs::write(&path, common::module().precompile().unwrap()).unwrap();
    // SAFETY: the file is what `precompile` just returned, in a directory nobody else writes.
    let loaded = unsafe { Module::from_precompiled_file(&path, ModuleConfig::default()) }.unwrap();
    assert!(loaded.is_precompiled());
    assert_eq!(eval_payments(&loaded), eval_payments(common::module()));
}

#[test]
fn an_artifact_made_with_other_settings_is_refused_with_advice() {
    let bytes = common::module().precompile().unwrap();
    // SAFETY: the bytes are a genuine artifact; wasmtime refuses it for its settings.
    let err = err_of(unsafe { Module::from_precompiled(&bytes, ModuleConfig { fuel: true }) });
    let text = err.to_string();
    assert!(text.contains("can't be loaded") && text.contains("same ModuleConfig"), "{text}");
}

#[test]
fn bytes_that_are_not_an_artifact_are_refused_not_run() {
    // SAFETY: wasmtime refuses these bytes before anything runs; they are not an artifact.
    let err = err_of(unsafe { Module::from_precompiled(b"not an artifact", ModuleConfig::default()) });
    assert!(err.to_string().contains("can't be loaded"), "{err}");
    let err = err_of(unsafe { Module::from_precompiled_file("/no/such/artifact", ModuleConfig::default()) });
    assert!(err.to_string().contains("can't be loaded"), "{err}");
}

#[cfg(feature = "precompiled")]
mod bundled {
    use super::*;

    #[test]
    fn the_bundled_module_loads_precompiled_unless_fuel_is_on() {
        assert!(Module::bundled().unwrap().is_precompiled());
        let metered = Module::bundled_with(ModuleConfig { fuel: true }).unwrap();
        assert!(!metered.is_precompiled(), "fuel metering compiles at run time");
        // And it works: the metered module runs.
        assert!(Sigil::new(&metered).unwrap().version().is_ok());
        assert!(metered.fuel());
    }

    #[test]
    fn the_bundled_module_loads_precompiled_and_runs() {
        let module = Module::bundled().unwrap();
        assert!(module.is_precompiled());
        let sigil = Sigil::new(&module).unwrap();
        assert!(sigil.version().is_ok());
    }
}
