package stamp

import "fmt"

// The errors Patch and Verify return. They're matched with errors.Is, and
// most come back wrapped in a message that says what exactly was found, so
// the text reads as a whole sentence either way. They're constants, so
// nothing can reassign them.
const (
	// ErrNoArea is returned when the marker isn't in the binary: it wasn't
	// built with a reserved area, or the area was stripped.
	ErrNoArea sentinel = "the binary has no reserved area: it wasn't built with one, or it was stripped"

	// ErrManyAreas is returned when the marker occurs more than once in the
	// binary, so there's no telling which one is the area.
	ErrManyAreas sentinel = "the binary has more than one reserved area"

	// ErrMarkerInData is returned when the data would put a second marker
	// into the area, which the next Patch couldn't tell from the real one.
	ErrMarkerInData sentinel = "the payload contains the reserved area's marker"

	// ErrSigned is returned for a binary whose signature can't be redone
	// after a change: a Mach-O signed with an identity (its CMS blob isn't
	// empty) or a PE with an Authenticode signature. Only an ad-hoc
	// signature is a pure function of the contents.
	ErrSigned sentinel = "the binary is signed with an identity, and only an ad-hoc signature can be updated after a change"

	// ErrUnsupported is returned for a format Patch doesn't handle: a
	// universal (fat) Mach-O, a format it doesn't know, or a code signature
	// it can't recompute.
	ErrUnsupported sentinel = "unsupported binary"

	// ErrMalformed is returned when the binary's headers or code signature
	// contradict themselves or point outside the file.
	ErrMalformed sentinel = "the binary is damaged"

	// ErrMismatch is returned by Verify when a page doesn't match its hash
	// in the code signature.
	ErrMismatch sentinel = "the code signature doesn't match the binary"
)

// sentinel is the type of the constant errors.
type sentinel string

// TooLargeError is returned when the data doesn't fit into the area after
// the marker.
type TooLargeError struct {
	// Size is the size of the data in bytes.
	Size int
	// Max is the most the area holds after the marker.
	Max int
}

// Error implements the error interface.
func (e *TooLargeError) Error() string {
	return fmt.Sprintf("the payload is %d bytes, %d more than the reserved area holds (%d bytes)", e.Size, e.Size-e.Max, e.Max)
}

// Error implements the error interface.
func (e sentinel) Error() string {
	return string(e)
}

// malformed returns an ErrMalformed that says what's wrong.
func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrMalformed}, args...)...)
}

// unsupported returns an ErrUnsupported that says what isn't supported.
func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrUnsupported}, args...)...)
}
