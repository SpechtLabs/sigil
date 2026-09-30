// Package policies embeds the example's policy trees into the binaries that
// need them. The platform's documents are the trusted source of the required
// guardrails, so the service only ever reads them from here and never from a
// directory an operator can swap. The team and access documents are embedded
// too, so the service runs with working bundles when nothing is mounted.
//
// A bundle holds documents of one kind, and the platform tree keeps both
// kinds' documents side by side under deploy/ and access/. [PlatformDeploy]
// and [PlatformAccess] are the per-kind views a host passes to
// [github.com/spechtlabs/sigil/pkg/policy.From]; [Only] builds them without
// stripping the directory, so positions in diagnostics and traces keep
// naming the file as it sits in the repository.
package policies //nolint:pkgnaming // named after the policies/ directory it embeds, which the docs and the tooling refer to

import (
	"embed"
	"io/fs"
	"strings"
)

var (
	//go:embed platform
	platformFiles embed.FS

	//go:embed teams
	teamFiles embed.FS

	//go:embed access
	accessFiles embed.FS
)

var (
	// Platform holds the platform team's documents for every kind: deploy/
	// for DeployApproval and access/ for AccessGrant. A bundle holds
	// documents of one kind only, so a host passes PlatformDeploy or
	// PlatformAccess to policy.From, not this.
	Platform = sub(platformFiles, "platform")

	// PlatformDeploy holds the platform's DeployApproval documents
	// (deploy.common, deploy.guardrails, deploy.production). The service
	// passes it to policy.From, which is what keeps a team bundle from
	// redefining the guardrails.
	PlatformDeploy = Only(Platform, "deploy")

	// PlatformAccess holds the platform's AccessGrant documents
	// (access.common, access.guardrails).
	PlatformAccess = Only(Platform, "access")

	// Teams holds the team policies shipped with the binary. It holds the
	// policy tests and their fixtures too; the loader only reads `.sigil`
	// files, so they do no harm.
	Teams = sub(teamFiles, "teams")

	// Access holds the AccessGrant bundle shipped with the binary, whose
	// root is access.main. Like Teams, it holds its policy tests too.
	Access = sub(accessFiles, "access")
)

// Only returns a view of fsys that shows the directory dir and nothing else
// at its root. Unlike fs.Sub, it keeps dir in every path, so positions in
// compile errors and traces still read `deploy/production.sigil:16:5`.
// That is how one platform tree with a directory per kind serves each kind
// its own documents: a bundle must not hold another kind's.
func Only(fsys fs.FS, dir string) fs.FS { //nolint:returninterface // a view is only useful as an fs.FS
	return onlyFS{fsys: fsys, dir: dir}
}

// sub roots fsys at dir. fs.Sub only fails on a path that isn't valid, and
// every path is a literal matching an embed directive above, which the
// package's test checks. The result is an fs.FS because fs.Sub's concrete
// type is unexported.
func sub(fsys embed.FS, dir string) fs.FS { //nolint:returninterface // fs.Sub returns an unexported type; fs.FS is the only way to name it
	root, _ := fs.Sub(fsys, dir)
	return root
}

// onlyFS is the view Only returns. It is a comparable struct, so it can be
// passed to policy.From, which uses the source as a map key.
type onlyFS struct {
	fsys fs.FS
	dir  string
}

// Open opens name when it is the root or lies under dir.
func (o onlyFS) Open(name string) (fs.File, error) { //nolint:humaneerror // fs.FS fixes the signature, and callers match its *fs.PathError
	if name != "." && !o.shows(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return o.fsys.Open(name)
}

// ReadDir lists name, with only dir left at the root.
func (o onlyFS) ReadDir(name string) ([]fs.DirEntry, error) { //nolint:humaneerror // fs.ReadDirFS fixes the signature, and callers match its *fs.PathError
	if name != "." && !o.shows(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(o.fsys, name)
	if err != nil || name != "." {
		return entries, err //nolint:errorwrap // passed through unchanged, so fs.ErrNotExist and friends still match
	}
	for _, e := range entries {
		if e.Name() == o.dir {
			return []fs.DirEntry{e}, nil
		}
	}
	return []fs.DirEntry{}, nil
}

// shows reports whether name is dir or lies under it.
func (o onlyFS) shows(name string) bool {
	return name == o.dir || strings.HasPrefix(name, o.dir+"/")
}
