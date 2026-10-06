// Package releasecheck checks that release-please versions everything the
// repository ships. Every package published with a Sigil release carries the
// release's version (the npm package, the crate, the VS Code extension, the
// tree-sitter grammar), and release-please bumps those versions through the
// extra-files of .release-please-config.json. A manifest missing from that
// list keeps its old version forever, and nobody notices until a registry
// rejects the upload or users install a stale one.
//
// [Check] walks the repository for versioned manifests that ship, and
// reports the ones release-please doesn't bump and every bumped version that
// differs from .release-please-manifest.json. The test in this package runs
// it on the repository, so CI fails on the next forgotten manifest.
package releasecheck

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/sierrasoftworks/humane-errors-go"
)

// The release-please files, at the repository root.
const (
	ConfigFile   = ".release-please-config.json"
	ManifestFile = ".release-please-manifest.json"
)

// The extra-file types release-please and this check share.
const (
	TypeJSON    = "json"
	TypeTOML    = "toml"
	TypeGeneric = "generic"
)

// The manifests this check looks for.
const (
	packageJSON    = "package.json"
	cargoToml      = "Cargo.toml"
	cargoLock      = "Cargo.lock"
	treeSitterJSON = "tree-sitter.json"
)

var (
	// skipDirs are never searched: dependencies, build output, downloads
	// and test inputs.
	skipDirs = []string{".git", "node_modules", "target", "dist", "coverage", ".vscode-test", "testdata"}

	// unshippedDirs hold manifests that never ship, at the repository root:
	// the docs site and the examples. Lockfiles there still pin the shipped
	// crate, so [Discover] reads their Cargo.lock files.
	unshippedDirs = []string{"docs", "examples"}

	// lockFilter matches the one filter jsonpath this package understands,
	// the one release-please's own Cargo.lock entries use.
	lockFilter = regexp.MustCompile(`^\$\.package\[\?\(@\.name\.value=='([^']+)'\)\]\.version$`)

	// marker matches a line release-please's generic updater rewrites: the
	// first integer on an x-release-please-major, -minor or -patch line, or
	// the version on an x-release-please-version line.
	marker = regexp.MustCompile(`x-release-please-(major|minor|patch|version)\b`)

	// firstInt and semver are what the generic updater replaces on a marked line.
	firstInt = regexp.MustCompile(`\b\d+\b`)
	semver   = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

// ExtraFile is an entry of the root package's extra-files.
type ExtraFile struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	JSONPath string `json:"jsonpath,omitempty"`
}

// String renders the entry as it belongs in .release-please-config.json.
func (f ExtraFile) String() string {
	b, _ := json.Marshal(f)
	return string(b)
}

// Config is what the check reads from the release-please files.
type Config struct {
	// ExtraFiles are the files release-please bumps with the release.
	ExtraFiles []ExtraFile
	// Version is the release version in .release-please-manifest.json.
	Version string
}

// Problem is a versioned file release-please doesn't keep in step.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string {
	return p.Path + ": " + p.Message
}

// Load reads the root package's extra-files and the manifest version.
func Load(root string) (Config, humane.Error) {
	var config struct {
		Packages map[string]struct {
			ExtraFiles []ExtraFile `json:"extra-files"`
		} `json:"packages"`
	}
	if err := readJSON(root, ConfigFile, &config); err != nil {
		return Config{}, err
	}
	var manifest map[string]string
	if err := readJSON(root, ManifestFile, &manifest); err != nil {
		return Config{}, err
	}
	version, ok := manifest["."]
	if !ok {
		return Config{}, humane.New(ManifestFile+` has no version for the root package "."`, "release-please writes it on every release; restore it from git")
	}
	return Config{ExtraFiles: config.Packages["."].ExtraFiles, Version: version}, nil
}

// Discover finds every versioned manifest that ships: package.json files
// that aren't private, Cargo.toml files with a [package], tree-sitter.json
// files and the src/parser.c next to each, whose metadata carries the
// grammar's version, and in every Cargo.lock the entry of a crate the
// repository builds from source. It returns each as the extra-file entry
// release-please needs to bump it, sorted by path.
func Discover(root string) ([]ExtraFile, humane.Error) {
	manifests, locks, err := find(root)
	if err != nil {
		return nil, err
	}

	var found []ExtraFile
	var crates []string
	for _, rel := range manifests {
		f, crate, ok, err := shipped(root, rel)
		if err != nil {
			return nil, err
		}
		if ok {
			found = append(found, f)
		}
		if filepath.Base(rel) == treeSitterJSON {
			found = append(found, ExtraFile{Type: TypeGeneric, Path: filepath.ToSlash(filepath.Join(filepath.Dir(rel), "src", "parser.c"))})
		}
		if crate != "" {
			crates = append(crates, crate)
		}
	}
	for _, rel := range locks {
		entries, err := lockedCrates(root, rel, crates)
		if err != nil {
			return nil, err
		}
		found = append(found, entries...)
	}
	slices.SortFunc(found, func(a, b ExtraFile) int { return strings.Compare(a.Path+a.JSONPath, b.Path+b.JSONPath) })
	return found, nil
}

// Read returns the version an extra-file holds at its jsonpath, or on its
// marked lines for a generic one.
func Read(root string, f ExtraFile) (string, humane.Error) {
	var doc any
	switch f.Type {
	case TypeJSON:
		if err := readJSON(root, f.Path, &doc); err != nil {
			return "", err
		}
	case TypeTOML:
		if err := readTOML(root, f.Path, &doc); err != nil {
			return "", err
		}
	case TypeGeneric:
		data, err := readFile(root, f.Path)
		if err != nil {
			return "", err
		}
		return markedVersion(f.Path, string(data))
	default:
		return "", humane.New(fmt.Sprintf("%s has extra-file type %q, which this check can't read", f.Path, f.Type), "teach releasecheck.Read the type, or use json, toml or generic")
	}
	return lookup(f.Path, doc, f.JSONPath)
}

// Check reports every shipped manifest release-please doesn't bump, and
// every extra-file whose version isn't the manifest's.
func Check(root string) ([]Problem, humane.Error) {
	config, err := Load(root)
	if err != nil {
		return nil, err
	}
	shipped, err := Discover(root)
	if err != nil {
		return nil, err
	}

	var problems []Problem
	for _, f := range shipped {
		if !slices.Contains(config.ExtraFiles, f) {
			problems = append(problems, Problem{
				Path: f.Path,
				Message: fmt.Sprintf("ships with a version release-please doesn't bump; add %s to packages[\".\"][\"extra-files\"] in %s, and set its version to %s",
					f, ConfigFile, config.Version),
			})
		}
	}
	for _, f := range config.ExtraFiles {
		version, err := Read(root, f)
		if err != nil {
			problems = append(problems, Problem{Path: f.Path, Message: strings.TrimPrefix(err.Display(), f.Path+": ")})
			continue
		}
		if version != config.Version {
			at := f.JSONPath
			if at == "" {
				at = "on its x-release-please lines"
			}
			problems = append(problems, Problem{
				Path: f.Path,
				Message: fmt.Sprintf("is at %s (%s), but %s is at %s; set it to %s, and release-please keeps it in step from then on",
					version, at, ManifestFile, config.Version, config.Version),
			})
		}
	}
	return problems, nil
}

// find walks the repository for the manifests that may ship and for every
// Cargo.lock.
func find(root string) (manifests, locks []string, _ humane.Error) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		switch d.Name() {
		case packageJSON, cargoToml, treeSitterJSON:
			if top, _, _ := strings.Cut(rel, "/"); !slices.Contains(unshippedDirs, top) {
				manifests = append(manifests, rel)
			}
		case cargoLock:
			locks = append(locks, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, humane.Wrap(err, "failed to search the repository for versioned manifests", "check that the repository is readable")
	}
	return manifests, locks, nil
}

// shipped reports the extra-file entry a manifest needs, if it ships, and the
// crate name of a Cargo.toml.
func shipped(root, rel string) (ExtraFile, string, bool, humane.Error) {
	switch filepath.Base(rel) {
	case packageJSON:
		var pkg struct {
			Version *string `json:"version"`
			Private bool    `json:"private"`
		}
		if err := readJSON(root, rel, &pkg); err != nil {
			return ExtraFile{}, "", false, err
		}
		return ExtraFile{Type: TypeJSON, Path: rel, JSONPath: "$.version"}, "", !pkg.Private && pkg.Version != nil, nil
	case treeSitterJSON:
		return ExtraFile{Type: TypeJSON, Path: rel, JSONPath: "$.metadata.version"}, "", true, nil
	default: // Cargo.toml
		var cargo struct {
			Package *struct {
				Name    string `toml:"name"`
				Version any    `toml:"version"`
				Publish *bool  `toml:"publish"`
			} `toml:"package"`
		}
		if err := readTOML(root, rel, &cargo); err != nil {
			return ExtraFile{}, "", false, err
		}
		if cargo.Package == nil || cargo.Package.Version == nil || (cargo.Package.Publish != nil && !*cargo.Package.Publish) {
			return ExtraFile{}, "", false, nil
		}
		return ExtraFile{Type: TypeTOML, Path: rel, JSONPath: "$.package.version"}, cargo.Package.Name, true, nil
	}
}

// lockedCrates returns the entries of a Cargo.lock that pin one of crates
// built from this repository: an entry without a source, which is a path
// dependency or the workspace's own crate.
func lockedCrates(root, rel string, crates []string) ([]ExtraFile, humane.Error) {
	var lock struct {
		Package []struct {
			Name   string `toml:"name"`
			Source string `toml:"source"`
		} `toml:"package"`
	}
	if err := readTOML(root, rel, &lock); err != nil {
		return nil, err
	}
	var out []ExtraFile
	for _, p := range lock.Package {
		if p.Source == "" && slices.Contains(crates, p.Name) {
			out = append(out, ExtraFile{Type: TypeTOML, Path: rel, JSONPath: fmt.Sprintf("$.package[?(@.name.value=='%s')].version", p.Name)})
		}
	}
	return out, nil
}

// lookup evaluates the jsonpaths release-please's extra-files use here: a
// chain of keys such as $.metadata.version, and the Cargo.lock filter.
func lookup(rel string, doc any, path string) (string, humane.Error) {
	fix := "fix the jsonpath in " + ConfigFile
	if m := lockFilter.FindStringSubmatch(path); m != nil {
		return lockEntry(rel, doc, m[1])
	}
	if !strings.HasPrefix(path, "$.") {
		return "", humane.New(fmt.Sprintf("%s: jsonpath %q isn't $.key.key or the Cargo.lock filter", rel, path), fix)
	}
	cur := doc
	for key := range strings.SplitSeq(strings.TrimPrefix(path, "$."), ".") {
		m, ok := cur.(map[string]any)
		if !ok || key == "" {
			return "", humane.New(fmt.Sprintf("%s: jsonpath %q doesn't resolve", rel, path), fix)
		}
		if cur, ok = m[key]; !ok {
			return "", humane.New(fmt.Sprintf("%s: jsonpath %q doesn't resolve: no %q", rel, path, key), fix)
		}
	}
	v, ok := cur.(string)
	if !ok {
		return "", humane.New(fmt.Sprintf("%s: jsonpath %q isn't a string", rel, path), fix)
	}
	return v, nil
}

// lockEntry returns the version of the one Cargo.lock entry named name. In
// release-please's filter, @.name.value is its spelling of a TOML string's
// value, which is plain name in a decoded document.
func lockEntry(rel string, doc any, name string) (string, humane.Error) {
	root, _ := doc.(map[string]any)
	packages, _ := root["package"].([]any)
	var found []string
	for _, p := range packages {
		entry, _ := p.(map[string]any)
		if v, ok := entry["version"].(string); ok && entry["name"] == name {
			found = append(found, v)
		}
	}
	if len(found) != 1 {
		return "", humane.New(fmt.Sprintf("%s: %d entries named %q have a version, want exactly one", rel, len(found), name), "fix the jsonpath in "+ConfigFile+", or regenerate the lockfile")
	}
	return found[0], nil
}

// markedVersion reads the version off the lines release-please's generic
// updater rewrites.
func markedVersion(rel, text string) (string, humane.Error) {
	fix := "mark the version lines with x-release-please-major, -minor and -patch, or one x-release-please-version"
	parts := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		m := marker.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		scope, code := line[m[2]:m[3]], line[:m[0]]
		v := firstInt.FindString(code)
		if scope == "version" {
			v = semver.FindString(code)
		}
		if v == "" {
			return "", humane.New(fmt.Sprintf("%s: the x-release-please-%s line has no version", rel, scope), fix)
		}
		parts[scope] = v
	}
	if v, ok := parts["version"]; ok {
		return v, nil
	}
	if parts["major"] == "" || parts["minor"] == "" || parts["patch"] == "" {
		return "", humane.New(rel+": no x-release-please-version line, and not all of the -major, -minor and -patch lines", fix)
	}
	return parts["major"] + "." + parts["minor"] + "." + parts["patch"], nil
}

// readFile reads a file of the repository by its slash-separated path.
func readFile(root, rel string) ([]byte, humane.Error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // rel is a path the repository walk found or release-please lists
	if err != nil {
		return nil, humane.Wrap(err, "failed to read "+rel, "check that the file exists and is readable")
	}
	return data, nil
}

func readJSON(root, rel string, v any) humane.Error {
	data, err := readFile(root, rel)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return humane.Wrap(err, rel+" isn't valid JSON", "fix the file")
	}
	return nil
}

func readTOML(root, rel string, v any) humane.Error {
	data, err := readFile(root, rel)
	if err != nil {
		return err
	}
	if err := toml.Unmarshal(data, v); err != nil {
		return humane.Wrap(err, rel+" isn't valid TOML", "fix the file")
	}
	return nil
}
