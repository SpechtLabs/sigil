// Package testsuite reads the test files `sigil test` and package
// policytest run, and checks an evaluation against what a test case
// expects.
//
// A test file is YAML, named `*_test.yaml`, and holds the cases for one
// policy:
//
//	policy: payments.production
//	cases:
//	  - name: pci deploy needs a review
//	    input_file: testdata/pci.json
//	    expect:
//	      decision: review
//	      reason: service_owner
//	      payload:
//	        approvers: [payments-leads, security-leads]
//	  - name: an unnamed actor fails the assert
//	    input: {actor: {name: ""}}
//	    expect:
//	      asserts: [named_actor]
//
// A case expects one decision and reason, with any payload fields it
// lists; or, for a `collect all` kind, the whole outcome; or the reasons
// of the asserts that fail. It can't expect a conflict.
//
// # Running a suite
//
// Both callers run a file the same way. [Parse] reads the file and
// rejects keys the format doesn't define. [Suite.Validate] checks every decision, reason and payload field
// a case names against the kind before anything runs. [Runner.RunCase]
// reads and decodes one case's input through the kind's binding, calls
// the caller's [Eval], and compares the [Outcome] with the case's
// [Expect], returning a [Result] whose failures say how they differ.
//
// The caller supplies the evaluation, so the CLI can evaluate a compiled
// eval.Policy and package policytest the host's public policy.Policy,
// each reducing its own result to an Outcome.
package testsuite
