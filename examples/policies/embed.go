// Package policies embeds the example's policy trees into the binaries that
// need them. The platform's documents are the trusted source of the required
// guardrails, so the service only ever reads them from here and never from a
// directory an operator can swap. The team documents are embedded too, so the
// service runs with a working bundle when no policies directory is mounted.
package policies //nolint:pkgnaming // named after the policies/ directory it embeds, which the docs and the tooling refer to

import (
	"embed"
	"io/fs"
)

var (
	//go:embed platform
	platformFiles embed.FS

	//go:embed teams
	teamFiles embed.FS
)

var (
	// Platform holds the platform team's documents (deploy.common,
	// deploy.guardrails, deploy.production), rooted the way the loader expects
	// them. The service passes it to policy.From, which is what keeps a team
	// bundle from redefining the guardrails.
	Platform = sub(platformFiles, "platform")

	// Teams holds the team policies shipped with the binary. It holds the
	// policy tests and their fixtures too; the loader only reads `.sigil`
	// files, so they do no harm.
	Teams = sub(teamFiles, "teams")
)

// sub roots fsys at dir. fs.Sub only fails on a path that isn't valid, and
// both paths are literals matching the embed directives above, which the
// package's test checks. The result is an fs.FS because fs.Sub's concrete
// type is unexported.
func sub(fsys embed.FS, dir string) fs.FS { //nolint:returninterface // fs.Sub returns an unexported type; fs.FS is the only way to name it
	root, _ := fs.Sub(fsys, dir)
	return root
}
