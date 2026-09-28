package fixture

import (
	"encoding/json"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// ExamplesDir is the examples module's root seen from a suite's package
// directory, test/<suite>, which is where `go test` runs it.
const ExamplesDir = "../.."

// OwnerFixture is the deploy input the payments policy tests use, relative
// to the examples module. OwnerRequest builds the same deploy as an API
// request; RequestFromInput relates the two so they can't drift.
const OwnerFixture = "policies/teams/payments/testdata/owner.json"

// The teams the example serves, and the environments the specs use.
const (
	TeamPayments = "payments"
	TeamCheckout = "checkout"

	EnvProduction = "production"
	EnvStaging    = "staging"
)

// The decisions and the reasons the specs assert most often.
const (
	DecisionApprove    = "approve"
	DecisionReview     = "review"
	DecisionDeny       = "deny"
	ReasonServiceOwner = "service_owner"
)

// Ada is the actor of every request the fixtures build.
const Ada = "ada"

// The access policy the service evaluates before every deploy.
const AccessPolicy = "access.main"

// Mutator changes one aspect of a request built by OwnerRequest.
type Mutator func(*DeployRequest)

// OwnerRequest returns the payments owner's PCI deploy with each mutator
// applied in turn. The actor is in the payments group, so the access policy
// makes them a deployer, and the deploy policy sees them as a payments team
// member who owns the service. Every call builds fresh slices and maps, so a
// mutator never leaks into the next spec.
func OwnerRequest(mutators ...Mutator) DeployRequest {
	r := DeployRequest{
		Release: Release{Soak: "6h", Hotfix: false},
		Service: Service{
			Name:   "ledger",
			Tier:   "standard",
			Owners: []string{TeamPayments},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "argocd",
				"platform.example.com/lifecycle": "ga",
				"regions":                        "eu,us",
				"compliance":                     "pci",
			},
		},
		Actor: Actor{
			Name:    Ada,
			Groups:  []string{TeamPayments},
			Regions: []string{"eu", "us"},
		},
		Environment: EnvProduction,
	}

	for _, mutate := range mutators {
		mutate(&r)
	}

	return r
}

// NoLabel removes a service label.
func NoLabel(key string) Mutator {
	return func(r *DeployRequest) { delete(r.Service.Labels, key) }
}

// Tier sets the service's tier.
func Tier(tier string) Mutator {
	return func(r *DeployRequest) { r.Service.Tier = tier }
}

// Owners replaces the service's owning teams.
func Owners(owners ...string) Mutator {
	return func(r *DeployRequest) { r.Service.Owners = append([]string{}, owners...) }
}

// Groups replaces the actor's groups. They decide the roles the access
// policy grants, and the deploy policy reads them as the actor's teams.
func Groups(groups ...string) Mutator {
	return func(r *DeployRequest) { r.Actor.Groups = append([]string{}, groups...) }
}

// Clearance sets the actor's clearance level.
func Clearance(level string) Mutator {
	return func(r *DeployRequest) { r.Actor.Clearance = level }
}

// ActorName sets the actor's name.
func ActorName(name string) Mutator {
	return func(r *DeployRequest) { r.Actor.Name = name }
}

// Soak sets the release's soak, a duration string.
func Soak(soak string) Mutator {
	return func(r *DeployRequest) { r.Release.Soak = soak }
}

// Hotfix marks the release as a hotfix.
func Hotfix() Mutator {
	return func(r *DeployRequest) { r.Release.Hotfix = true }
}

// JSON renders the request. The request holds only strings, bools, slices
// and string maps, which always marshal, so there is no error to return, and
// the suites can build their tables while Ginkgo constructs the spec tree.
func (r DeployRequest) JSON() string {
	return mustMarshal(r)
}

// JSONWithField renders the request with one more top-level field, which the
// service must reject rather than ignore: a misspelled or misplaced field
// would otherwise evaluate a policy against a zero value.
func (r DeployRequest) JSONWithField(key, value string) string {
	return mustMarshal(map[string]any{
		"release":     r.Release,
		"service":     r.Service,
		"actor":       r.Actor,
		"environment": r.Environment,
		key:           value,
	})
}

// RequestFromInput turns a deploy policy test input, which has the deploy
// kind's actor with teams and roles, into the API request a client sends for
// the same deploy: the teams become the groups, and the roles are left out
// because the access policy grants them.
func RequestFromInput(input []byte) (DeployRequest, humane.Error) {
	var in struct {
		Release     Release `json:"release"`
		Service     Service `json:"service"`
		Environment string  `json:"environment"`
		Actor       struct {
			Name    string   `json:"name"`
			Teams   []string `json:"teams"`
			Regions []string `json:"regions"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return DeployRequest{}, humane.Wrap(err, "the deploy input isn't valid JSON",
			"check the testdata file against the DeployApproval kind")
	}

	return DeployRequest{
		Release:     in.Release,
		Service:     in.Service,
		Environment: in.Environment,
		Actor: Actor{
			Name:    in.Actor.Name,
			Groups:  in.Actor.Teams,
			Regions: in.Actor.Regions,
		},
	}, nil
}

// AccessFor returns ada's access request for the payments team in
// production, as a member of groups.
func AccessFor(groups ...string) AccessRequest {
	return AccessRequest{
		Actor:       AccessActor{Name: Ada, Groups: append([]string{}, groups...)},
		Team:        TeamPayments,
		Environment: EnvProduction,
	}
}

// In returns the request for another environment.
func (r AccessRequest) In(environment string) AccessRequest {
	r.Environment = environment
	return r
}

// Cleared returns the request with the actor holding clearance level.
func (r AccessRequest) Cleared(level string) AccessRequest {
	r.Actor.Clearance = level
	return r
}

// Named returns the request with the actor called name.
func (r AccessRequest) Named(name string) AccessRequest {
	r.Actor.Name = name
	return r
}

// mustMarshal renders v, which the callers build from strings, bools, string
// slices and string maps only. Those always marshal, so there is no error to
// return.
func mustMarshal(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
