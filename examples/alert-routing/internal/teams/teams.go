// Package teams is alertrouter's team directory: which teams it routes for,
// and for each the on-call target a page goes to and the channel a
// notification is posted in. An alert names its team in its team label, the
// router looks the team up here, and the policy <team>.alerts reads the
// entry as the kind's team input. A policy never hard-codes a pager or a
// channel, so a rotation change is an edit to this file and not to a policy.
//
// The directory is a small YAML file. [Default] is the copy embedded in the
// binary; [LoadFile] reads the one --teams-file names. Decoding is strict,
// because a misspelled key would otherwise leave a team with an empty
// on-call target that only shows up when a critical alert can't page anyone:
// an unknown key is an error with a did-you-mean hint, and every team is
// validated before the directory is returned.
package teams //nolint:pkgnaming // a directory of teams; team.Directory would read as one team's directory

import (
	"bytes"
	_ "embed" // for the default directory
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// DefaultSource is what [Directory.Source] reports for [Default].
const DefaultSource = "embedded teams.yaml"

// formatAdvice is the shape of a directory file, for every error about it.
const formatAdvice = `write the directory as "teams:" and a list of {name, oncall, channel} entries, like internal/teams/teams.yaml`

// The keys a directory file takes, at its top level and in each team.
var (
	fileKeys = []string{"teams"}
	teamKeys = []string{"name", "oncall", "channel"}
)

// namePattern is what a team name must look like. The name becomes the
// policy name <team>.alerts and the bundle directory teams/<team>/, so it
// has to be a Sigil identifier, and lower case keeps the directory and the
// label value the same everywhere.
var namePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

//go:embed teams.yaml
var defaultFile []byte

// Directory is a loaded team directory. It is immutable once loaded, so a
// server shares one between requests without locking.
type Directory struct {
	teams  map[string]routing.Team
	source string
	names  []string
}

// Load reads a directory from r. source names where r comes from, a path or
// [DefaultSource], and prefixes every error position. The error says which
// key or team is wrong, where, and how to fix it.
func Load(r io.Reader, source string) (*Directory, humane.Error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, humane.Wrap(err, "cannot read the team directory "+source,
			"check that the file is readable, or leave --teams-file empty for the embedded directory")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, humane.Wrap(err, "the team directory "+source+" isn't valid YAML", formatAdvice)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, humane.New("the team directory "+source+" is empty", formatAdvice)
	}

	root := doc.Content[0]
	if herr := checkKeys(source, root, fileKeys, "the directory"); herr != nil {
		return nil, herr
	}
	list := value(root, "teams")
	if list == nil || list.Kind != yaml.SequenceNode || len(list.Content) == 0 {
		return nil, humane.New("the team directory "+source+" lists no teams", formatAdvice)
	}

	d := &Directory{teams: make(map[string]routing.Team, len(list.Content)), source: source}
	for _, node := range list.Content {
		if herr := checkKeys(source, node, teamKeys, "a team"); herr != nil {
			return nil, herr
		}

		var entry struct {
			Name    string `yaml:"name"`
			Oncall  string `yaml:"oncall"`
			Channel string `yaml:"channel"`
		}
		if err := node.Decode(&entry); err != nil {
			return nil, humane.Wrap(err, fmt.Sprintf("%s: the team isn't a map of strings", at(source, node)), formatAdvice)
		}

		team := routing.Team{Name: entry.Name, Oncall: entry.Oncall, Channel: entry.Channel}
		if herr := d.add(source, node, team); herr != nil {
			return nil, herr
		}
	}

	slices.Sort(d.names)
	return d, nil
}

// LoadFile reads the directory at path, which is also its [Directory.Source].
func LoadFile(path string) (*Directory, humane.Error) {
	f, err := os.Open(path) //nolint:gosec // the path is the operator's --teams-file
	if err != nil {
		return nil, humane.Wrap(err, "cannot open the team directory "+path,
			"check that --teams-file names a readable YAML file, or leave it empty for the embedded directory")
	}
	defer func() { _ = f.Close() }()

	return Load(f, path)
}

// Default returns the directory embedded in the binary, internal/teams/
// teams.yaml. It panics if that file doesn't load, which the package's
// tests rule out: the file is compiled in, so no input can make it fail.
func Default() *Directory {
	d, err := Load(bytes.NewReader(defaultFile), DefaultSource)
	if err != nil {
		panic("the embedded team directory doesn't load: " + err.Display()) //nolint:nopanic // the file is compiled in and TestDefault loads it, so no input reaches this
	}
	return d
}

// Lookup returns the team called name, and false when the directory has no
// such team. Names match exactly: a team label must be written as the
// directory writes it.
func (d *Directory) Lookup(name string) (routing.Team, bool) {
	t, ok := d.teams[name]
	return t, ok
}

// Names returns every team's name, sorted. The slice is the caller's.
func (d *Directory) Names() []string {
	return slices.Clone(d.names)
}

// Teams returns every team, sorted by name. The slice is the caller's.
func (d *Directory) Teams() []routing.Team {
	teams := make([]routing.Team, 0, len(d.names))
	for _, name := range d.names {
		teams = append(teams, d.teams[name])
	}
	return teams
}

// Source is where the directory was loaded from: the --teams-file path, or
// [DefaultSource].
func (d *Directory) Source() string {
	return d.source
}

// add validates team, read from node, and adds it to the directory.
func (d *Directory) add(source string, node *yaml.Node, team routing.Team) humane.Error {
	pos := at(source, node)
	switch {
	case team.Name == "":
		return humane.New(pos+": a team has no name", "set name to the value alerts carry in their team label")
	case !namePattern.MatchString(team.Name):
		return humane.New(fmt.Sprintf("%s: the team name %q isn't a lower-case identifier", pos, team.Name),
			"use letters a-z, digits and underscores, starting with a letter: the name becomes the policy <team>.alerts")
	case team.Oncall == "":
		return humane.New(fmt.Sprintf("%s: team %s has no oncall", pos, team.Name),
			"set oncall to the paging target of the team's rotation; without it a critical alert pages no one")
	case !strings.HasPrefix(team.Channel, "#"):
		return humane.New(fmt.Sprintf("%s: team %s has the channel %q, which doesn't start with #", pos, team.Name, team.Channel),
			`set channel to the team's alert channel, such as "#checkout-alerts", quoted so YAML doesn't read the # as a comment`)
	}

	if _, dup := d.teams[team.Name]; dup {
		return humane.New(fmt.Sprintf("%s: team %s is listed twice", pos, team.Name),
			"merge the two entries: a team has one on-call target and one channel")
	}

	d.teams[team.Name] = team
	d.names = append(d.names, team.Name)
	return nil
}

// checkKeys fails when node isn't a mapping, or holds a key outside keys or
// the same key twice. what names the node in the message, such as "a team".
func checkKeys(source string, node *yaml.Node, keys []string, what string) humane.Error {
	if node == nil {
		return humane.New(fmt.Sprintf("%s: %s is missing", source, what), formatAdvice)
	}
	if node.Kind != yaml.MappingNode {
		return humane.New(fmt.Sprintf("%s: %s must be a map with the keys %s", at(source, node), what, strings.Join(keys, ", ")), formatAdvice)
	}

	seen := make(map[string]bool, len(keys))
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if !slices.Contains(keys, key.Value) {
			return humane.New(fmt.Sprintf("%s: unknown key %q in %s", at(source, key), key.Value, what), keyAdvice(key.Value, keys))
		}
		if seen[key.Value] {
			return humane.New(fmt.Sprintf("%s: the key %q is set twice in %s", at(source, key), key.Value, what),
				"keep one of them; YAML would silently use the last")
		}
		seen[key.Value] = true
	}
	return nil
}

// keyAdvice suggests the known key closest to key, if any is close enough to
// be a typo, and lists the keys either way.
func keyAdvice(key string, keys []string) string {
	known := "the keys are " + strings.Join(keys, ", ")
	best, bestDist := "", 3 // more than two edits away is not a typo
	for _, k := range keys {
		if d := distance(strings.ToLower(key), k); d < bestDist {
			best, bestDist = k, d
		}
	}
	if best == "" {
		return known
	}
	return fmt.Sprintf("did you mean %q? %s", best, known)
}

// value returns the value of key in the mapping node, or nil.
func value(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// at is node's position in source, as file:line:column.
func at(source string, node *yaml.Node) string {
	if node == nil {
		return source
	}
	return fmt.Sprintf("%s:%d:%d", source, node.Line, node.Column)
}

// distance is the Levenshtein distance between a and b, in bytes: key names
// are ASCII.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
