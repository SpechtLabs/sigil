// Command gokinds prints, as one JSON object keyed by kind name, what Go's
// Kind.Schema writes for the kinds test/kinds/*.ts define with the
// TypeScript builder. The TypeScript tests run it and compare byte for
// byte, so the two builders can't drift. It lives under testdata so
// `go build ./...`, `go test ./...` and the linters leave it alone; run it
// with `go run ./bindings/typescript/test/testdata/gokinds` from the
// repository root.
package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

func main() {
	kinds := map[string]string{
		"AlertRouting": alertRouting().Schema(),
		"Everything":   everything().Schema(),
		"Collecting":   collecting().Schema(),
		"Minimal":      minimal().Schema(),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(kinds); err != nil {
		panic(err)
	}
}

// alertRouting is examples/alert-routing's kind, as the Go service
// defined it in internal/routing/kind.go.
func alertRouting() *policy.Kind[arInput] {
	page := policy.NewDecision[arPage]("page", "critical_alert", "sustained")
	drop := policy.NewDecision[policy.None]("drop", "muted", "not_production")
	notify := policy.NewDecision[arNotify]("notify", "routine", "unrouted")
	return policy.NewKind[arInput]("AlertRouting",
		policy.WithVersion(1),
		policy.WithEnum(arCritical, arWarning, arInfo),
		policy.WithDecisions(page, drop, notify),
		policy.WithReasonPrecedence(page.Reason("critical_alert"), page.Reason("sustained")),
		policy.WithReasonPrecedence(drop.Reason("muted"), drop.Reason("not_production")),
		policy.WithReasonPrecedence(notify.Reason("routine"), notify.Reason("unrouted")),
		policy.WithDefault(notify.Reason("unrouted")),
	)
}

type Severity string

const (
	arCritical Severity = "critical"
	arWarning  Severity = "warning"
	arInfo     Severity = "info"
)

type arInput struct {
	Alert Alert `policy:"alert"`
	Team  Team  `policy:"team"`
}

type Alert struct {
	Name      string            `policy:"name"`
	Severity  Severity          `policy:"severity"`
	Labels    map[string]string `policy:"labels"`
	FiringFor time.Duration     `policy:"firing_for"`
}

type Team struct {
	Name    string `policy:"name"`
	Oncall  string `policy:"oncall"`
	Channel string `policy:"channel"`
}

type arPage struct {
	Target string `policy:"target"`
}

type arNotify struct {
	Channel string `policy:"channel,default=\"#alerts\""`
}

// everything is a collect one kind with every type, every kind of payload
// default, a function reaching a struct and an enum nothing else does,
// an enum only registered, accepts, exclusive and conflict.
func everything() *policy.Kind[evInput] {
	deny := policy.NewDecision[policy.None]("deny", "bad", "worse", "fallback")
	allow := policy.NewDecision[evAllow]("allow", "fine", "great")
	hold := policy.NewDecision[evHold]("hold", "wait")
	return policy.NewKind[evInput]("Everything",
		policy.WithVersion(3),
		policy.WithAccepts(2),
		policy.WithEnum(UnusedA, UnusedB),
		policy.WithEnum(MoodCalm, MoodTense),
		policy.WithEnum(RegionEU, RegionUS),
		policy.WithEnum(LevelLow, LevelHigh),
		policy.WithFunc("lookup", func(string, map[Region]int64) (User, error) { return User{}, nil }),
		policy.WithFunc("count", func([]string) int { return 0 }),
		policy.WithFunc("locate", func(Site) Region { return RegionEU }),
		policy.WithDecisions(deny, allow, hold),
		policy.WithReasonPrecedence(deny.Reason("worse"), deny.Reason("bad"), deny.Reason("fallback")),
		policy.WithExclusive(allow.Reason("great"), hold),
		policy.WithDefault(deny.Reason("fallback")),
		policy.WithConflict(deny.Reason("worse")),
	)
}

type (
	Level  string
	Region string
	Unused string
	Mood   string
)

const (
	LevelLow  Level  = "low"
	LevelHigh Level  = "high"
	RegionEU  Region = "eu"
	RegionUS  Region = "us"
	UnusedA   Unused = "a"
	UnusedB   Unused = "b"
	MoodCalm  Mood   = "calm"
	MoodTense Mood   = "tense"
)

type evInput struct {
	Request Request            `policy:"request"`
	Now     time.Time          `policy:"now"`
	Limit   *int64             `policy:"limit"`
	Weights map[string]float64 `policy:"weights"`
}

type Request struct {
	User     User             `policy:"user"`
	Manager  *User            `policy:"manager"`
	Tags     []string         `policy:"tags"`
	Meta     map[string]int64 `policy:"meta"`
	Deadline time.Time        `policy:"deadline"`
	Score    float64          `policy:"score"`
	Empty    Empty            `policy:"empty"`
}

type User struct {
	Name   string  `policy:"name"`
	Groups []Group `policy:"groups"`
	Level  Level   `policy:"level"`
}

type Group struct {
	Name  string `policy:"name"`
	Admin bool   `policy:"admin"`
}

type Empty struct{}

type Site struct {
	Name string `policy:"name"`
}

type evAllow struct {
	TTL    time.Duration    `policy:"ttl,default=90m"`
	Days   time.Duration    `policy:"days,default=2d3h"`
	Note   string           `policy:"note,default=\"tab\\t \\\"quoted\\\" \\\\ ✓ é 😀 \\x01 \\u2028 \\x7f\""`
	Limit  int64            `policy:"limit,default=-3"`
	Ratio  float64          `policy:"ratio,default=0.5"`
	Big    float64          `policy:"big,default=1000000000000000000000.0"`
	Tiny   float64          `policy:"tiny,default=0.0000001"`
	Whole  float64          `policy:"whole,default=2.0"`
	On     bool             `policy:"on,default=true"`
	Tags   []string         `policy:"tags,default=[\"b\", \"a\"]"`
	Level  Level            `policy:"level,default=high"`
	Limits map[string]int64 `policy:"limits,default={\"b\": 2, \"a\": 1, \"ä\": 3, \"Z\": 4}"`
	Maybe2 *int64           `policy:"maybe2,default=4"`
	Empty  []string         `policy:"empty,default=[]"`
}

type evHold struct {
	Until string `policy:"until"`
	Mood  Mood   `policy:"mood"`
}

// collecting is a collect all kind with precedence, a reason ranking,
// two exclusive sets and a default.
func collecting() *policy.Kind[colInput] {
	a := policy.NewDecision[policy.None]("a", "r1", "r2")
	b := policy.NewDecision[colB]("b", "x", "y")
	c := policy.NewDecision[policy.None]("c", "z")
	return policy.NewKind[colInput]("Collecting",
		policy.WithVersion(1),
		policy.WithCollect(a, b, c),
		policy.WithPrecedence(c, a, b),
		policy.WithReasonPrecedence(a.Reason("r2"), a.Reason("r1")),
		policy.WithExclusive(a, b.Reason("y")),
		policy.WithExclusive(b, c),
		policy.WithDefault(c.Reason("z")),
	)
}

type colInput struct {
	Items []string `policy:"items"`
}

type colB struct {
	Weight float64 `policy:"weight,default=1.0"`
}

// minimal is the smallest kind: one input, one decision, its default.
func minimal() *policy.Kind[minInput] {
	ok := policy.NewDecision[policy.None]("ok", "yes")
	return policy.NewKind[minInput]("Minimal",
		policy.WithVersion(1),
		policy.WithDecisions(ok),
		policy.WithDefault(ok.Reason("yes")),
		policy.WithFunc("upper", strings.ToUpper),
	)
}

type minInput struct {
	Who string `policy:"who"`
}
