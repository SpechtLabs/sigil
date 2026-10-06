// Package lsp implements the `sigil lsp` command, which runs the Sigil
// language server over stdin and stdout. The server itself is package
// github.com/spechtlabs/sigil/internal/lsp; this package registers the
// command and gives the server its [loader], which reads a project the way
// `sigil check` reads it.
package lsp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/diagnose"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	server "github.com/spechtlabs/sigil/internal/lsp"
)

// maxUndeclared is the most `.sigil` files a workspace folder without a
// configuration file may hold for the server to read it as one project.
const maxUndeclared = 200

// loader finds and loads projects for the server as `sigil check` does,
// with the kinds linked into the binary.
type loader struct {
	kinds []project.Linked
}

// NewCommand returns the lsp command, configured by opts.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "lsp",
		SuggestFor: []string{"language-server", "server"},
		Short:      "Run the Sigil language server",
		Long: `Runs the Sigil language server, which editors start in the background and
talk to over stdin and stdout with the Language Server Protocol.

For each document the editor opens, the server reads the project the way
check does from the nearest sigil.yaml, sigil.json or sigil.toml (or .sigil.*)
at or above the document: its directory, kind files, trusted paths,
requirements and lint levels. Without one it reads the workspace folder the
document is in, or the document alone outside every folder. Open buffers
replace their files on disk. It needs the kind files only, not the host's Go
code, and a host binary's linked kinds count too.

It publishes the diagnostics check reports, a moment after the last edit, and
offers completion, hover, go-to-definition, and formatting the way fmt
formats. Logs go to stderr.`,
		Example: `# Start the language server the way an editor does
sigil lsp --stdio`,
		Args:              usage.None(),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			s := server.New(loader{kinds: o.kinds}, server.WithLog(cmd.ErrOrStderr()), server.WithVersion(o.version))
			return s.Serve(ctx, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	// Editors pass --stdio by convention; stdio is the only transport.
	cmd.Flags().Bool("stdio", true, "Talk to the editor over stdin and stdout, the only transport")

	return cmd
}

// Root returns the root of the project the file at path belongs to:
//
//   - the directory of the nearest configuration file at or above it,
//     which check run from there would read, a declared project;
//   - else the deepest workspace folder holding it, when the folder holds
//     at most maxUndeclared `.sigil` files;
//   - else, for a larger folder, the file's own directory, if that holds
//     few enough, or else the file alone, with a note that says so;
//   - and for a file outside every folder, the file alone, so the server
//     doesn't walk the directories around it.
//
// A project without a configuration file reports on its open documents
// only. The bound keeps a folder that merely contains policies, such as a
// home directory or a large monorepo, from being checked whole on every
// change.
func (l loader) Root(path string, folders []string) server.Root {
	if cfg, err := config.Find(filepath.Dir(path)); err == nil && cfg != "" {
		if abs, err := filepath.Abs(cfg); err == nil {
			return server.Root{Path: filepath.Dir(abs), Declared: true}
		}
	}
	best := ""
	for _, f := range folders {
		if inside(path, f) && len(f) > len(best) {
			best = f
		}
	}
	switch {
	case best == "":
		return server.Root{Path: path}
	case fits(best):
		return server.Root{Path: best}
	}
	read := path
	if dir := filepath.Dir(path); fits(dir) {
		read = dir
	}
	return server.Root{Path: read, Note: fmt.Sprintf("%s holds more than %d .sigil files and no %s, so the language server reads %s alone; a sigil.yaml at the root of the policies makes them one project", best, maxUndeclared, config.FileNames[0], read)}
}

// Load reads the project at root as `sigil check` run there would, with
// the open buffers in overlay replacing their files, and diagnoses it.
// What stops the check, such as a configuration file that doesn't parse,
// is the snapshot's error; a project that loaded but whose requirements
// can't be enforced still answers completion.
func (l loader) Load(root string, overlay map[string][]byte) *server.Snapshot {
	dir := root
	if !isDir(root) {
		dir = filepath.Dir(root)
	}
	cfg, err := config.Find(dir)
	if err != nil {
		return &server.Snapshot{Err: err}
	}
	if cfg != "" {
		cfg, _ = filepath.Abs(cfg)
	}
	r, err := diagnose.Load(dir, cfg, project.Sources{Paths: []string{root}, Overlay: overlay}, nil, nil, l.kinds)
	if err != nil {
		return &server.Snapshot{Err: err}
	}
	snap := &server.Snapshot{Project: r.Project}
	if err := r.Diagnose(); err != nil {
		snap.Err = err
		return snap
	}
	snap.Diagnostics = r.Errs
	return snap
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isDir reports whether path is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fits reports whether dir holds at most maxUndeclared `.sigil` files,
// by the rules a walk of a directory reads them: entries whose names
// start with `.` are left out. It stops counting past the bound.
func fits(dir string) bool {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil //nolint:nilerr // an entry that can't be read doesn't count, as a walk leaves it to the loader
		case p != dir && strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case !d.IsDir() && project.IsSigil(p):
			n++
		}
		if n > maxUndeclared {
			return fs.SkipAll
		}
		return nil
	})
	return n <= maxUndeclared
}
