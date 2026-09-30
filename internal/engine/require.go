package engine

import (
	"fmt"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// required returns the policies compile's requirements make root, a
// policy of group g, invoke unconditionally, as a host's policy.Require
// does. With trusted files, each is policy.Require(name,
// policy.From(trusted)): the required policy must be one of theirs. Go
// would let a document of the bundle satisfy a requirement its trusted
// source doesn't define; here that's an error, since the trusted files
// are the host's statement of where the policy comes from. A required
// policy of another kind can't be invoked by root, so it doesn't apply.
func required(p *workspace.Project, g *workspace.Group, r *request) ([]string, humane.Error) {
	var names []string
	for i, req := range r.Require {
		switch {
		case req.Policy == "":
			return nil, humane.New(where(i)+" names no policy", `give each requirement the policy it requires, as {"policy": "platform.paging"}`)
		case len(req.Trusted) > 0 || len(req.Roots) > 0:
			return nil, humane.New(where(i)+": compile's requirements take only a policy", "the requirement applies to the policy compiled; send its trusted documents as trusted_files")
		}
		owner := p.Group(req.Policy)
		if len(r.Trusted) > 0 {
			if err := fromTrusted(owner, req.Policy, i); err != nil {
				return nil, err
			}
		}
		if owner == nil || owner == g {
			names = append(names, req.Policy)
		}
	}
	return names, nil
}

// fromTrusted checks that the required policy, which g owns, is defined
// in the trusted files, as policy.From requires.
func fromTrusted(g *workspace.Group, name string, i int) humane.Error {
	if g == nil {
		return humane.New(fmt.Sprintf("%s: %s is required, but the trusted files define no policy %s", where(i), name, name), "send the document that defines it among trusted_files")
	}
	d := g.Bundle.Document(name)
	if d.Trusted {
		return nil
	}
	return stopped(diag.ErrorList{{
		File: d.File, Pos: d.Node.Pos(), End: d.Node.End(), Doc: name,
		Msg:  fmt.Sprintf("%s must come from the trusted files, but it's defined here", name),
		Help: "the host reads a required policy only from its trusted source, as policy.From does; define it there, and remove this definition",
	}}, fmt.Sprintf("%s: %s isn't among the trusted files", where(i), name))
}
