package fixture

import "net/http"

// DecisionCase is one request and the decision the served policies must
// answer it with. The integration and end-to-end suites run the same cases,
// so the in-process server and the container can't disagree.
type DecisionCase struct {
	Request  DeployRequest
	Name     string
	Team     string
	Decision string
	Reason   string
	// Payload is the exact payload JSON, durations as strings.
	Payload string
	Status  int
}

// BadRequestCase is a body the service must refuse with 400.
type BadRequestCase struct {
	Name string
	Body string
}

// DecisionCases returns every decision and reason the payments and checkout
// policies can reach through the API, with the status it maps to.
func DecisionCases() []DecisionCase {
	const (
		bothApprovers = `{"approvers": ["payments-leads", "security-leads"]}`
		noPayload     = `{}`
	)
	checkoutOwner := func(more ...Mutator) DeployRequest {
		return OwnerRequest(append([]Mutator{Owners(TeamCheckout), Teams(TeamCheckout), Soak("24h")}, more...)...)
	}

	return []DecisionCase{
		{
			Name: "a payments owner's PCI service needs both approver groups",
			Team: TeamPayments, Request: OwnerRequest(),
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: bothApprovers,
		},
		{
			Name: "a non-PCI service needs only the payments leads",
			Team: TeamPayments, Request: OwnerRequest(NoLabel("compliance")),
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: `{"approvers": ["payments-leads"]}`,
		},
		{
			Name: "a release manager approves a critical service with the default bake",
			Team: TeamPayments, Request: OwnerRequest(Tier("critical"), Roles("deployer", "release_manager")),
			Status: http.StatusOK, Decision: DecisionApprove, Reason: "release_manager",
			Payload: `{"bake": "1h"}`,
		},
		{
			Name: "a payments SRE approves with the team's short bake",
			Team: TeamPayments, Request: OwnerRequest(Teams("payments-sre")),
			Status: http.StatusOK, Decision: DecisionApprove, Reason: "payments_sre",
			Payload: `{"bake": "15m"}`,
		},
		{
			Name: "a two hour soak is shorter than the team's four hours",
			Team: TeamPayments, Request: OwnerRequest(Soak("2h")),
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "soak_too_short",
			Payload: noPayload,
		},
		{
			Name: "a hotfix with a two hour soak skips the soak guardrail",
			Team: TeamPayments, Request: OwnerRequest(Soak("2h"), Hotfix()),
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: bothApprovers,
		},
		{
			Name: "an actor without the deployer role is not eligible",
			Team: TeamPayments, Request: OwnerRequest(Roles()),
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "not_eligible",
			Payload: noPayload,
		},
		{
			// Checkout keeps the platform's 24h minimum soak, so its owner
			// ships a release that soaked for a day.
			Name: "the checkout team reviews its own standard service",
			Team: TeamCheckout, Request: checkoutOwner(),
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: `{"approvers": ["checkout-leads"]}`,
		},
		{
			// Checkout narrows the tiers to standard, so nothing matches an
			// internal service and the kind's default applies.
			Name: "checkout's owner of an internal service matches no rule",
			Team: TeamCheckout, Request: checkoutOwner(Tier("internal")),
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "no_rule_matched",
			Payload: noPayload,
		},
	}
}

// BadRequestCases returns bodies the deployments endpoint must refuse before
// evaluating anything.
func BadRequestCases() []BadRequestCase {
	return []BadRequestCase{
		{Name: "malformed JSON", Body: `{"release": {"soak": "6h"`},
		{Name: "a field at the wrong level", Body: OwnerRequest().JSONWithField("tier", "critical")},
		{Name: "a duration that doesn't parse", Body: OwnerRequest(Soak("six hours")).JSON()},
		{Name: "a number where a duration belongs", Body: `{"release": {"soak": 21600000000000}}`},
		{Name: "a lone minus sign as a duration", Body: OwnerRequest(Soak("-")).JSON()},
		{Name: "a day count longer than a duration holds", Body: OwnerRequest(Soak("9999999999d")).JSON()},
		{Name: "a negative soak", Body: OwnerRequest(Soak("-1h")).JSON()},
	}
}
