// Package stub replaces host functions with canned results, so `sigil
// test`, package policytest and `sigil eval` can evaluate a policy that
// calls a host function the binary doesn't link, or pin one whose real
// result changes from run to run.
//
// A stub is YAML or JSON, keyed by the host function's name:
//
//	remove_requestor:
//	  calls:                 # first match wins; args compared as values
//	    - args: [{name: kevin, roles: [user]}, [cedric, alice, bob]]
//	      returns: [cedric, alice, bob]
//	  returns: []            # when no call matches
//	lookup_owner:
//	  error: directory unavailable
//
// A call whose args equal an entry's under `calls:` gets that entry's
// result. Any other call gets the stub's `returns:`, or fails with its
// `error:`; a stub with neither, which then needs `calls:`, fails such a
// call with an [*ErrUnmatched] that names its args. Each level gives
// exactly one of `returns:` and `error:`, except that the stub itself may
// give neither when it has calls.
//
// # Parsing and binding
//
// [Parse] reads a stubs object from its YAML node and checks its shape,
// with the line of every problem; [Set] implements [yaml.Unmarshaler]
// with it, so a test file's `stubs:` decodes straight into a Set.
// [ParseDocument] reads a whole stubs file, and [ParseFlag] one
// `NAME=VALUE` flag, whose value is the result of every call.
//
// [Set.Bind] checks a set against the kind, naming an unknown function,
// a call with the wrong number of args and a value that doesn't fit its
// param or result type, and returns a copy of the binding with each
// stubbed function replaced, whether the binding's own was real or
// unbound. Host functions are resolved when a policy is compiled, so a
// policy evaluates with stubs only when it is compiled with the bound
// copy. [Set.Validate] runs the same checks without a binding.
package stub
