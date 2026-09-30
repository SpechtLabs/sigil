//! The policy store: reads the flag documents, compiles them into an
//! evaluation pool, and swaps the result in only when everything compiled.
//!
//! A reload builds a whole new [`Bundle`] (its own pool, every flag compiled
//! in every instance) and replaces the serving one in one step, so a request
//! sees the old bundle or the new one, never a mix, and a broken directory
//! leaves the last known good one serving.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt::Write as _;
use std::ops::Deref;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, PoisonError, RwLock};
use std::time::SystemTime;

use sha2::{Digest, Sha256};
use sigil::{CompileOptions, CompileRequirement, ExplainOptions, Module, Pool, PoolOptions, Sigil, SourceFile};

use crate::manifest::{self, FlagSpec, MANIFEST_FILE};
use crate::{embedded, flags, kind};

/// The platform policy every flag policy must invoke, unconditionally.
pub const GUARDRAILS: &str = "platform.guardrails";

/// The most one policy file may hold: a flag's rollout rules are a page, and a
/// mount that holds something else by mistake shouldn't be read whole.
const MAX_FILE_BYTES: u64 = 256 * 1024;

/// How many directories deep the policies may be nested.
const MAX_DEPTH: usize = 8;

/// Where the flag documents come from.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Source {
    /// The sample bundle compiled into the binary.
    Embedded,
    /// A directory of `*.sigil` files, read recursively.
    Directory(PathBuf),
}

impl Source {
    pub fn label(&self) -> &'static str {
        match self {
            Self::Embedded => "embedded",
            Self::Directory(_) => "directory",
        }
    }
}

/// One served flag.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FlagInfo {
    pub key: String,
    pub policy: String,
    /// The value type and off value from `flags.yaml`.
    pub spec: FlagSpec,
}

/// A value dropped off the async threads. Tearing down a [`Pool`] releases
/// every policy in every instance, which blocks, and the last reference to a
/// retired bundle is often a request on a tokio worker; this moves the drop
/// to a blocking thread when there is a runtime to do it.
pub struct Retired<T: Send + 'static>(Option<T>);

impl<T: Send + 'static> Retired<T> {
    pub fn new(value: T) -> Self {
        Self(Some(value))
    }
}

impl<T: Send + 'static> Deref for Retired<T> {
    type Target = T;
    fn deref(&self) -> &T {
        self.0.as_ref().expect("only Drop empties it")
    }
}

impl<T: Send + 'static> Drop for Retired<T> {
    fn drop(&mut self) {
        let Some(value) = self.0.take() else { return };
        match tokio::runtime::Handle::try_current() {
            Ok(runtime) => {
                runtime.spawn_blocking(move || drop(value));
            }
            // No runtime: this thread is not an async worker, so dropping here is fine.
            Err(_) => drop(value),
        }
    }
}

/// One compiled set of flag policies, immutable once built.
pub struct Bundle {
    pub pool: Retired<Pool>,
    /// The served flags, by key.
    pub flags: BTreeMap<String, FlagInfo>,
    /// A short content digest of the source documents.
    pub digest: String,
    pub source: Source,
    pub loaded_at: SystemTime,
}

/// What a reload did.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ReloadOutcome {
    /// A new bundle is serving.
    Loaded { digest: String, flags: Vec<String> },
    /// The documents haven't changed since the serving bundle was built.
    Unchanged { digest: String },
    /// The documents didn't load; the previous bundle keeps serving.
    Failed { error: String },
}

/// Holds the serving bundle and loads the next.
pub struct Store {
    module: Module,
    source: Source,
    workers: usize,
    acquire_timeout: std::time::Duration,
    current: RwLock<Arc<Bundle>>,
    /// The error of the last failed reload, cleared by the next good one.
    last_error: Mutex<Option<String>>,
    /// Serializes reloads: two at once would build two pools for nothing.
    reloading: Mutex<()>,
}

impl Bundle {
    /// The names in `names` that no flag of this bundle has.
    pub fn unserved(&self, names: &BTreeSet<String>) -> Vec<String> {
        names.iter().filter(|n| !self.flags.contains_key(*n)).cloned().collect()
    }
}

impl Store {
    /// Loads the first bundle. Blocking: it compiles every flag in every
    /// instance. An error is what to fix before the service can start.
    pub fn open(module: Module, source: Source, workers: usize, acquire_timeout: std::time::Duration) -> Result<Self, String> {
        let bundle = build(&module, &source, workers, acquire_timeout)?;
        Ok(Self {
            module,
            source,
            workers,
            acquire_timeout,
            current: RwLock::new(Arc::new(bundle)),
            last_error: Mutex::new(None),
            reloading: Mutex::new(()),
        })
    }

    /// The bundle serving now.
    pub fn current(&self) -> Arc<Bundle> {
        Arc::clone(&self.current.read().unwrap_or_else(PoisonError::into_inner))
    }

    /// The error of the last failed reload, if the serving bundle is older than it.
    pub fn last_error(&self) -> Option<String> {
        self.last_error.lock().unwrap_or_else(PoisonError::into_inner).clone()
    }

    /// Reads the source again and swaps in a new bundle when it changed, or
    /// when `force` is set. Blocking.
    pub fn reload(&self, force: bool) -> ReloadOutcome {
        let _one_at_a_time = self.reloading.lock().unwrap_or_else(PoisonError::into_inner);
        let outcome = self.reload_locked(force);
        let mut last = self.last_error.lock().unwrap_or_else(PoisonError::into_inner);
        match &outcome {
            ReloadOutcome::Failed { error } => *last = Some(error.clone()),
            ReloadOutcome::Loaded { .. } => *last = None,
            ReloadOutcome::Unchanged { .. } => {}
        }
        outcome
    }

    fn reload_locked(&self, force: bool) -> ReloadOutcome {
        let documents = match read_source(&self.source) {
            Ok(d) => d,
            Err(error) => return ReloadOutcome::Failed { error },
        };
        let digest = digest(&documents);
        if !force && digest == self.current().digest {
            return ReloadOutcome::Unchanged { digest };
        }
        match compile(&self.module, &self.source, documents, digest.clone(), self.workers, self.acquire_timeout) {
            Ok(bundle) => {
                let flags = bundle.flags.keys().cloned().collect();
                *self.current.write().unwrap_or_else(PoisonError::into_inner) = Arc::new(bundle);
                ReloadOutcome::Loaded { digest, flags }
            }
            Err(error) => ReloadOutcome::Failed { error },
        }
    }
}

fn build(module: &Module, source: &Source, workers: usize, acquire_timeout: std::time::Duration) -> Result<Bundle, String> {
    let documents = read_source(source)?;
    let digest = digest(&documents);
    compile(module, source, documents, digest, workers, acquire_timeout)
}

fn compile(
    module: &Module,
    source: &Source,
    documents: Vec<SourceFile>,
    digest: String,
    workers: usize,
    acquire_timeout: std::time::Duration,
) -> Result<Bundle, String> {
    // An empty mount (a ConfigMap that failed to attach, a wrong path) would switch
    // every flag off at once; refusing it keeps the last known good bundle serving.
    let (manifest_files, documents): (Vec<_>, Vec<_>) = documents.into_iter().partition(|f| f.path == MANIFEST_FILE);
    if documents.is_empty() {
        return Err("the flag policies source holds no *.sigil files\n  help: check that FEATUREGATE_POLICIES points at the mounted flags directory".to_owned());
    }
    let specs = match manifest_files.first() {
        Some(f) => manifest::parse(&f.source)?,
        None => BTreeMap::new(),
    };
    let mut files = vec![SourceFile::new(kind::KIND_PATH, kind::schema())];
    files.extend(documents);
    let trusted = embedded::platform();

    // Which policies the documents define: the explanation of a bundle lists
    // every policy in it, before any is compiled.
    let scratch = Sigil::new(module).map_err(|e| e.to_string())?;
    let explained = scratch
        .explain(&files, &ExplainOptions { policy: None, trusted_files: trusted.clone() })
        .map_err(|e| format!("the flag policies don't load:\n{e}"))?;
    let mut served = BTreeMap::new();
    for e in explained {
        if !e.policy.starts_with(flags::POLICY_PREFIX) {
            // Not a flag: a helper a flag policy may invoke, served by nothing.
            continue;
        }
        let key = flags::key_of_policy(&e.policy).ok_or_else(|| {
            format!(
                "the policy {} is in the flags namespace but names no flag key\n  help: name it flags.<key> with the key's hyphens written as underscores, like flags.new_checkout for new-checkout",
                e.policy
            )
        })?;
        let spec = specs.get(&key).cloned().unwrap_or_default();
        served.insert(key.clone(), FlagInfo { key, policy: e.policy, spec });
    }
    let unknown: Vec<_> = specs.keys().filter(|k| !served.contains_key(*k)).collect();
    if !unknown.is_empty() {
        return Err(format!(
            "{MANIFEST_FILE} declares flags no policy serves: {}\n  help: remove them, or add the flags.<key> policies; a typo in a key is the usual cause",
            unknown.iter().map(|k| k.as_str()).collect::<Vec<_>>().join(", ")
        ));
    }
    drop(scratch);

    let pool = Pool::with_options(module, PoolOptions { size: workers, acquire_timeout: Some(acquire_timeout), ..PoolOptions::default() })
        .map_err(|e| e.to_string())?;
    for info in served.values() {
        let options = CompileOptions {
            policy: Some(info.policy.clone()),
            // The guardrail is required, unconditionally, from the trusted
            // documents: a flag policy that omits it, gates it or defines its
            // own platform.guardrails doesn't compile, so the bundle doesn't load.
            require: vec![CompileRequirement::new(GUARDRAILS)],
            trusted_files: trusted.clone(),
            ..CompileOptions::default()
        };
        pool.compile(&info.policy, &files, options).map_err(|e| format!("the flag policy {} doesn't compile:\n{e}", info.policy))?;
    }
    Ok(Bundle { pool: Retired::new(pool), flags: served, digest, source: source.clone(), loaded_at: SystemTime::now() })
}

/// Reads the documents of a source, sorted by path so the digest is stable.
fn read_source(source: &Source) -> Result<Vec<SourceFile>, String> {
    match source {
        Source::Embedded => Ok(embedded::sample_flags()),
        Source::Directory(dir) => {
            let mut files = Vec::new();
            read_dir(dir, dir, &mut files)?;
            files.sort_by(|a, b| a.path.cmp(&b.path));
            Ok(files)
        }
    }
}

fn read_dir(root: &Path, dir: &Path, out: &mut Vec<SourceFile>) -> Result<(), String> {
    let canonical_root = std::fs::canonicalize(root).map_err(|e| {
        format!(
            "can't read the policies directory {}: {e}\n  help: set FEATUREGATE_POLICIES to a directory of flag policies",
            root.display()
        )
    })?;
    walk(root, &canonical_root, dir, 0, &mut BTreeSet::new(), out)
}

/// Walks `dir`, following symlinks only to places under the root: a link that
/// leaves it (a mounted directory must not be able to pull in /etc), a cycle,
/// and a tree deeper than [`MAX_DEPTH`] are refused or skipped.
fn walk(
    root: &Path,
    canonical_root: &Path,
    dir: &Path,
    depth: usize,
    visited: &mut BTreeSet<PathBuf>,
    out: &mut Vec<SourceFile>,
) -> Result<(), String> {
    if depth > MAX_DEPTH {
        return Err(format!(
            "{} is nested more than {MAX_DEPTH} directories deep\n  help: keep flag policies in a flat directory, or a few levels of team directories",
            dir.display()
        ));
    }
    let canonical = std::fs::canonicalize(dir).map_err(|e| {
        format!(
            "can't read the policies directory {}: {e}\n  help: set FEATUREGATE_POLICIES to a directory of flag policies",
            dir.display()
        )
    })?;
    if !canonical.starts_with(canonical_root) {
        return Err(format!(
            "{} resolves to {}, outside the policies directory\n  help: remove the symlink, or move what it points at under {}",
            dir.display(),
            canonical.display(),
            root.display()
        ));
    }
    if !visited.insert(canonical) {
        // Already walked through another path: a symlink cycle or a repeat.
        return Ok(());
    }
    let entries = std::fs::read_dir(dir).map_err(|e| format!("can't read {}: {e}", dir.display()))?;
    for entry in entries {
        let entry = entry.map_err(|e| format!("can't list {}: {e}", dir.display()))?;
        let path = entry.path();
        // Kubernetes mounts a ConfigMap as symlinks, including `..data`: skip
        // hidden entries, which are the mount's own bookkeeping. The visible
        // names are links into `..data/`, which resolve under the root.
        if entry.file_name().to_string_lossy().starts_with('.') {
            continue;
        }
        let meta = std::fs::metadata(&path).map_err(|e| format!("can't stat {}: {e}", path.display()))?;
        if meta.is_dir() {
            walk(root, canonical_root, &path, depth + 1, visited, out)?;
        } else if path.extension().is_some_and(|e| e == "sigil") || path.file_name().is_some_and(|n| n == MANIFEST_FILE) {
            let real = std::fs::canonicalize(&path).map_err(|e| format!("can't resolve {}: {e}", path.display()))?;
            if !real.starts_with(canonical_root) {
                return Err(format!(
                    "{} resolves to {}, outside the policies directory\n  help: remove the symlink, or copy the file in",
                    path.display(),
                    real.display()
                ));
            }
            if meta.len() > MAX_FILE_BYTES {
                return Err(format!("{} is {} bytes, past the {MAX_FILE_BYTES} a policy file may hold", path.display(), meta.len()));
            }
            let text = std::fs::read_to_string(&path).map_err(|e| format!("can't read {}: {e}", path.display()))?;
            let rel = path.strip_prefix(root).unwrap_or(&path).to_string_lossy().replace('\\', "/");
            out.push(SourceFile::new(rel, text));
        }
    }
    Ok(())
}

/// The first 12 hex digits of a hash over every path and text, in order.
fn digest(files: &[SourceFile]) -> String {
    let mut h = Sha256::new();
    for f in files {
        h.update((f.path.len() as u64).to_be_bytes());
        h.update(f.path.as_bytes());
        h.update((f.source.len() as u64).to_be_bytes());
        h.update(f.source.as_bytes());
    }
    let mut out = String::new();
    for b in &h.finalize()[..6] {
        let _ = write!(out, "{b:02x}");
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn digests_follow_content_and_path() {
        let a = vec![SourceFile::new("a.sigil", "x")];
        assert_eq!(digest(&a), digest(&a.clone()));
        assert_ne!(digest(&a), digest(&[SourceFile::new("a.sigil", "y")]));
        assert_ne!(digest(&a), digest(&[SourceFile::new("b.sigil", "x")]));
        // Length prefixes keep the boundary between path and text.
        assert_ne!(digest(&[SourceFile::new("ab", "c")]), digest(&[SourceFile::new("a", "bc")]));
        assert_eq!(digest(&a).len(), 12);
    }

    #[test]
    fn a_directory_is_read_recursively_sorted_and_without_bookkeeping() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("team")).unwrap();
        std::fs::create_dir(dir.path().join("..data")).unwrap();
        std::fs::write(dir.path().join("b.sigil"), "b").unwrap();
        std::fs::write(dir.path().join("team/a.sigil"), "a").unwrap();
        std::fs::write(dir.path().join("a_test.yaml"), "not a policy").unwrap();
        std::fs::write(dir.path().join("..data/x.sigil"), "hidden").unwrap();
        std::fs::write(dir.path().join(".hidden.sigil"), "hidden").unwrap();
        let files = read_source(&Source::Directory(dir.path().to_owned())).unwrap();
        assert_eq!(files.iter().map(|f| f.path.as_str()).collect::<Vec<_>>(), ["b.sigil", "team/a.sigil"]);
    }

    #[test]
    fn a_missing_directory_says_what_to_set() {
        let err = read_source(&Source::Directory("/nonexistent/flags".into())).unwrap_err();
        assert!(err.contains("FEATUREGATE_POLICIES"), "{err}");
    }

    #[test]
    fn an_oversized_file_is_refused() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("big.sigil"), vec![b'#'; MAX_FILE_BYTES as usize + 1]).unwrap();
        let err = read_source(&Source::Directory(dir.path().to_owned())).unwrap_err();
        assert!(err.contains("past the"), "{err}");
    }
}

#[cfg(test)]
mod symlink_tests {
    use std::os::unix::fs::symlink;

    use super::*;

    fn paths(dir: &Path) -> Result<Vec<String>, String> {
        read_source(&Source::Directory(dir.to_owned())).map(|f| f.into_iter().map(|f| f.path).collect())
    }

    #[test]
    fn a_kubernetes_configmap_mount_layout_is_read() {
        // What a ConfigMap volume looks like: ..data -> ..<timestamp>/, and each
        // visible name a link through ..data.
        let dir = tempfile::tempdir().unwrap();
        let stamp = dir.path().join("..2026_10_01");
        std::fs::create_dir(&stamp).unwrap();
        std::fs::write(stamp.join("dark_mode.sigil"), "policy").unwrap();
        std::fs::write(stamp.join("flags.yaml"), "flags: {}").unwrap();
        symlink("..2026_10_01", dir.path().join("..data")).unwrap();
        symlink("..data/dark_mode.sigil", dir.path().join("dark_mode.sigil")).unwrap();
        symlink("..data/flags.yaml", dir.path().join("flags.yaml")).unwrap();
        assert_eq!(paths(dir.path()).unwrap(), ["dark_mode.sigil", "flags.yaml"]);
        assert_eq!(read_source(&Source::Directory(dir.path().to_owned())).unwrap()[0].source, "policy");
    }

    #[test]
    fn a_symlink_cycle_is_walked_once() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("team")).unwrap();
        std::fs::write(dir.path().join("team/a.sigil"), "a").unwrap();
        symlink("..", dir.path().join("team/up")).unwrap();
        symlink(".", dir.path().join("self")).unwrap();
        assert_eq!(paths(dir.path()).unwrap(), ["team/a.sigil"]);
    }

    #[test]
    fn a_link_out_of_the_root_is_refused() {
        let outside = tempfile::tempdir().unwrap();
        std::fs::write(outside.path().join("secret.sigil"), "x").unwrap();
        let dir = tempfile::tempdir().unwrap();
        symlink(outside.path().join("secret.sigil"), dir.path().join("linked.sigil")).unwrap();
        let err = paths(dir.path()).unwrap_err();
        assert!(err.contains("outside the policies directory"), "{err}");

        let dir = tempfile::tempdir().unwrap();
        symlink(outside.path(), dir.path().join("elsewhere")).unwrap();
        let err = paths(dir.path()).unwrap_err();
        assert!(err.contains("outside the policies directory"), "{err}");
    }

    #[test]
    fn nesting_is_capped() {
        let dir = tempfile::tempdir().unwrap();
        let mut deep = dir.path().to_owned();
        for i in 0..=MAX_DEPTH + 1 {
            deep = deep.join(format!("d{i}"));
        }
        std::fs::create_dir_all(&deep).unwrap();
        std::fs::write(deep.join("x.sigil"), "x").unwrap();
        let err = paths(dir.path()).unwrap_err();
        assert!(err.contains("nested more than"), "{err}");
    }

    #[test]
    fn a_dangling_link_is_an_error_naming_it() {
        let dir = tempfile::tempdir().unwrap();
        symlink("missing.sigil", dir.path().join("dangling.sigil")).unwrap();
        let err = paths(dir.path()).unwrap_err();
        assert!(err.contains("dangling.sigil"), "{err}");
    }
}

#[cfg(test)]
mod retired_tests {
    use std::sync::mpsc;
    use std::thread::ThreadId;

    use super::*;

    struct ReportsItsThread(mpsc::Sender<ThreadId>);

    impl Drop for ReportsItsThread {
        fn drop(&mut self) {
            self.0.send(std::thread::current().id()).unwrap();
        }
    }

    #[tokio::test]
    async fn a_retired_value_is_dropped_off_the_async_thread() {
        let (tx, rx) = mpsc::channel();
        let here = std::thread::current().id();
        drop(Retired::new(ReportsItsThread(tx)));
        let dropped_on = tokio::task::spawn_blocking(move || rx.recv_timeout(std::time::Duration::from_secs(5)).unwrap()).await.unwrap();
        assert_ne!(dropped_on, here, "the drop ran on the async worker");
    }

    #[test]
    fn without_a_runtime_it_drops_in_place() {
        let (tx, rx) = mpsc::channel();
        drop(Retired::new(ReportsItsThread(tx)));
        assert_eq!(rx.try_recv().unwrap(), std::thread::current().id());
    }

    #[test]
    fn it_derefs_to_the_value() {
        let r = Retired::new(String::from("x"));
        assert_eq!(r.len(), 1);
    }
}
