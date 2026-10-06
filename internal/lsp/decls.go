package lsp

import "github.com/spechtlabs/sigil/internal/ast"

// The finders below pick a name out of one declaration of a kind
// document, for go-to-definition: each returns the name declaring what it
// looks for, or nil when d doesn't declare it.

// inputDecl finds `input name`.
func inputDecl(d ast.Decl, name string) *ast.Ident {
	if in, ok := d.(*ast.InputDecl); ok && in.Name.Name == name {
		return in.Name
	}
	return nil
}

// fnDecl finds `fn name`.
func fnDecl(d ast.Decl, name string) *ast.Ident {
	if fn, ok := d.(*ast.FnDecl); ok && fn.Name.Name == name {
		return fn.Name
	}
	return nil
}

// decisionDecl finds `decision name`.
func decisionDecl(d ast.Decl, name string) *ast.Ident {
	if dec, ok := d.(*ast.DecisionDecl); ok && dec.Name.Name == name {
		return dec.Name
	}
	return nil
}

// decisionField finds the payload field of decision.
func decisionField(d ast.Decl, decision, field string) *ast.Ident {
	if dec, ok := d.(*ast.DecisionDecl); ok && dec.Name.Name == decision {
		for _, f := range dec.Fields {
			if f.Name.Name == field {
				return f.Name
			}
		}
	}
	return nil
}

// decisionReason finds one reason of decision.
func decisionReason(d ast.Decl, decision, reason string) *ast.Ident {
	if dec, ok := d.(*ast.DecisionDecl); ok && dec.Name.Name == decision {
		for _, r := range dec.Reasons {
			if r.Name == reason {
				return r
			}
		}
	}
	return nil
}

// reasonField finds the `reason:` of decision, or its name in the legacy
// syntax, which has none.
func reasonField(d ast.Decl, decision string) *ast.Ident {
	if dec, ok := d.(*ast.DecisionDecl); ok && dec.Name.Name == decision {
		if dec.ReasonName != nil {
			return dec.ReasonName
		}
		return dec.Name
	}
	return nil
}

// typeDecl finds `type name`.
func typeDecl(d ast.Decl, name string) *ast.Ident {
	if t, ok := d.(*ast.TypeDecl); ok && t.Name.Name == name {
		return t.Name
	}
	return nil
}

// typeField finds a field of the struct type name.
func typeField(d ast.Decl, name, field string) *ast.Ident {
	if t, ok := d.(*ast.TypeDecl); ok && t.Name.Name == name {
		for _, f := range t.Fields {
			if f.Name.Name == field {
				return f.Name
			}
		}
	}
	return nil
}

// enumDecl finds `enum name`.
func enumDecl(d ast.Decl, name string) *ast.Ident {
	if e, ok := d.(*ast.EnumDecl); ok && e.Name.Name == name {
		return e.Name
	}
	return nil
}

// enumValue finds one value of the enum name.
func enumValue(d ast.Decl, name, value string) *ast.Ident {
	if e, ok := d.(*ast.EnumDecl); ok && e.Name.Name == name {
		for _, v := range e.Values {
			if v.Name == value {
				return v
			}
		}
	}
	return nil
}
