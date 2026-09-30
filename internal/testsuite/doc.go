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
// of the asserts that fail; or, with `error:`, text the message of the
// runtime error it fails with contains, such as a stub's error. It can't
// expect a conflict.
//
// A file's `stubs:` replace host functions for every case, and a case's
// own `stubs:` replace those, per function, in the format package stub
// reads:
//
//	stubs:
//	  owner: {returns: payments-leads}
//	cases:
//	  - name: the directory is down
//	    stubs:
//	      owner: {error: directory unavailable}
//
// [Runner.Bind] returns the binding a case evaluates with. Host functions
// are resolved when a policy compiles, so the caller compiles the policy
// with the suite's binding, and again for each case with stubs of its own.
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
