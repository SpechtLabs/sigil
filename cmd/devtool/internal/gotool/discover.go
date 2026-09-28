package gotool

import (
	"context"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// Target is a test function a devtool command runs: a benchmark or a fuzz
// target.
type Target struct {
	// Package is the import path; Dir is the package directory relative to
	// the module root, in the ./dir form a person passes on the command line.
	Package string
	Dir     string
	// File is the declaring file, relative to the module root.
	File string
	Name string
}

// Checkout describes the working tree a run measures.
type Checkout struct {
	Head  string
	Dirty bool
	Go    string
}

// Discover returns the test functions whose names start with prefix in the
// packages patterns select (./... when there are none), in package and
// file order.
func Discover(ctx context.Context, root string, patterns []string, prefix string) ([]Target, humane.Error) {
	pkgs, err := List(ctx, root, orAll(patterns)...)
	if err != nil {
		return nil, err
	}

	var targets []Target
	for _, p := range pkgs {
		funcs, err := p.Funcs(prefix)
		if err != nil {
			return nil, err
		}
		dir := RelDir(root, p.Dir)
		for _, fn := range funcs {
			file := strings.TrimPrefix(dir+"/"+fn.File, "./")
			targets = append(targets, Target{Package: p.ImportPath, Dir: dir, File: file, Name: fn.Name})
		}
	}
	return targets, nil
}

// Select returns the targets whose names filter matches. It's an error when
// none do; patterns and nouns, e.g. "benchmarks", word it.
func Select(targets []Target, filter *regexp.Regexp, patterns []string, nouns string) ([]Target, humane.Error) {
	var selected []Target
	for _, t := range targets {
		if filter.MatchString(t.Name) {
			selected = append(selected, t)
		}
	}
	if len(selected) > 0 {
		return selected, nil
	}
	where := "in " + strings.Join(orAll(patterns), " ")
	if len(targets) > 0 {
		where = "matching " + filter.String() + " " + where
	}
	return nil, humane.New("no "+nouns+" "+where,
		"check the package patterns and --filter; the list command shows every one")
}

// Packages returns the targets' packages, in order.
func Packages(targets []Target) []string {
	var pkgs []string
	for _, t := range targets {
		if len(pkgs) == 0 || pkgs[len(pkgs)-1] != t.Dir {
			pkgs = append(pkgs, t.Dir)
		}
	}
	return pkgs
}

// Describe returns the commit, the dirty state and the Go version of the
// checkout at root.
func Describe(ctx context.Context, root string) (Checkout, humane.Error) {
	head, err := Output(ctx, root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return Checkout{}, err
	}
	status, err := Output(ctx, root, nil, "git", "status", "--porcelain")
	if err != nil {
		return Checkout{}, err
	}
	version, err := Output(ctx, root, nil, "go", "env", "GOVERSION")
	if err != nil {
		return Checkout{}, err
	}
	goos, err := Output(ctx, root, nil, "go", "env", "GOOS", "GOARCH")
	if err != nil {
		return Checkout{}, err
	}
	return Checkout{
		Head:  strings.TrimSpace(head),
		Dirty: status != "",
		Go:    strings.TrimSpace(version) + " " + strings.Join(strings.Fields(goos), "/"),
	}, nil
}

// Short abbreviates the commit the way git log --oneline does.
func (c Checkout) Short() string {
	return ShortSHA(c.Head)
}

// ShortSHA abbreviates a commit ID the way git log --oneline does.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// CompileFilter compiles a --filter value; an empty one matches everything.
func CompileFilter(expr string) (*regexp.Regexp, humane.Error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, humane.Wrap(err, "--filter "+expr+" isn't a valid regular expression",
			"pass a Go regular expression, e.g. --filter 'Parse|Lexer'")
	}
	return re, nil
}

func orAll(patterns []string) []string {
	if len(patterns) == 0 {
		return []string{"./..."}
	}
	return patterns
}
