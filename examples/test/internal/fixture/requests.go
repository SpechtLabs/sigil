package fixture

import "encoding/json"

// ExamplesDir is the examples module's root seen from a suite's package
// directory, test/<suite>, which is where `go test` runs it.
const ExamplesDir = "../.."

// OwnerFixture is the input the payments policy tests use, relative to the
// examples module. OwnerRequest builds the same input in Go, and both suites
// compare the two so they can't drift.
const OwnerFixture = "policies/teams/payments/testdata/owner.json"

// The teams the example serves.
const (
	TeamPayments = "payments"
	TeamCheckout = "checkout"
)

// The decisions and the reasons the specs assert most often.
const (
	DecisionApprove    = "approve"
	DecisionReview     = "review"
	DecisionDeny       = "deny"
	ReasonServiceOwner = "service_owner"
)

// Mutator changes one aspect of a request built by OwnerRequest.
type Mutator func(*DeployRequest)

// OwnerRequest returns the payments owner's PCI deploy, field for field the
// same as OwnerFixture, with each mutator applied in turn. Every call builds
// fresh slices and maps, so a mutator never leaks into the next spec.
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
			Name:    "ada",
			Teams:   []string{TeamPayments},
			Roles:   []string{"deployer"},
			Regions: []string{"eu", "us"},
		},
		Environment: "production",
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

// Roles replaces the actor's roles. With no roles the list is empty, not
// null, the way a client that knows the field would send it.
func Roles(roles ...string) Mutator {
	return func(r *DeployRequest) { r.Actor.Roles = append([]string{}, roles...) }
}

// Teams replaces the actor's teams.
func Teams(teams ...string) Mutator {
	return func(r *DeployRequest) { r.Actor.Teams = append([]string{}, teams...) }
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
	data, _ := json.Marshal(r)
	return string(data)
}

// JSONWithField renders the request with one more top-level field, which the
// service must reject rather than ignore: a misspelled or misplaced field
// would otherwise evaluate a policy against a zero value.
func (r DeployRequest) JSONWithField(key, value string) string {
	data, _ := json.Marshal(map[string]any{
		"release":     r.Release,
		"service":     r.Service,
		"actor":       r.Actor,
		"environment": r.Environment,
		key:           value,
	})
	return string(data)
}
