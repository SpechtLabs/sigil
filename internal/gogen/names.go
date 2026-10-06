package gogen

import (
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// initialisms are the words GoName spells in capitals, as Go spells
// `ID` and `URL`: the list golint used.
var initialisms = map[string]bool{
	"acl": true, "api": true, "ascii": true, "cpu": true, "css": true, "dns": true,
	"eof": true, "guid": true, "html": true, "http": true, "https": true, "id": true,
	"ip": true, "json": true, "lhs": true, "qps": true, "ram": true, "rhs": true,
	"rpc": true, "sla": true, "smtp": true, "sql": true, "ssh": true, "tcp": true,
	"tls": true, "ttl": true, "udp": true, "ui": true, "uid": true, "uuid": true,
	"uri": true, "url": true, "utf8": true, "vm": true, "xml": true, "xmpp": true,
	"xsrf": true, "xss": true,
}

// namespace hands out Go identifiers in one scope, the package or one
// struct's fields, so no two things the generator declares share a name.
type namespace map[string]bool

// GoName returns the exported Go identifier for a Sigil name: each word
// between underscores starts with a capital, and a word Go spells as an
// initialism is all capitals, so release_manager becomes ReleaseManager
// and user_id UserID. A name with no letter to start with, such as `_1`,
// gets an X in front, since a Go identifier can't start with a digit.
func GoName(name string) string {
	var b strings.Builder
	for word := range strings.SplitSeq(name, "_") {
		switch {
		case word == "":
		case initialisms[word]:
			b.WriteString(strings.ToUpper(word))
		default:
			b.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	s := b.String()
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		s = "X" + s
	}
	return s
}

// PackageName returns the default Go package name for a kind: its name
// in lower case, so DeployApproval becomes deployapproval.
func PackageName(kindName string) string {
	return strings.ToLower(kindName)
}

// CheckPackage returns an error saying why name can't name a Go package,
// with what to do about it, or nil when it can: when it's an identifier,
// and neither a keyword nor the blank identifier.
func CheckPackage(name string) humane.Error {
	why := ""
	switch {
	case token.IsKeyword(name):
		why = "is a Go keyword"
	case !token.IsIdentifier(name) || name == "_":
		why = "isn't a Go identifier"
	default:
		return nil
	}
	return humane.New("package name "+strconv.Quote(name)+" "+why, "name the package with a Go identifier that isn't a keyword, like `deployapproval`")
}

// reserved says why a Go file can't declare a package-level type called
// name, or "" when it can. The generated types keep the kind's names, so
// a keyword or a predeclared identifier, which the generated code needs
// for its own types, can't be one.
func reserved(name string) string {
	switch {
	case name == "_":
		return "_ is Go's blank identifier"
	case name == "init":
		return "init is reserved for Go's package initializers"
	case token.IsKeyword(name):
		return name + " is a Go keyword"
	case types.Universe.Lookup(name) != nil:
		return name + " is predeclared in Go, and the generated code uses Go's own"
	}
	return ""
}

// claim takes the first candidate that's free, or, when all are taken,
// the last candidate with the smallest number from 2 that makes it free.
// A declaration earlier in the generated file keeps its name, and a later
// one that maps to the same identifier gets the number.
func (n namespace) claim(candidates ...string) string {
	for _, c := range candidates {
		if !n[c] {
			n[c] = true
			return c
		}
	}
	last := candidates[len(candidates)-1]
	for i := 2; ; i++ {
		if c := last + strconv.Itoa(i); !n[c] {
			n[c] = true
			return c
		}
	}
}
