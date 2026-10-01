package compile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/diagnose"
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/stamp"
)

// executable returns the path of the binary find finds, which compile
// copies, and fails when out is that binary: compile writes a copy and
// leaves the binary it runs as alone.
func executable(find func() (string, error), out string) (string, humane.Error) {
	exe, err := find()
	if err != nil {
		return "", humane.Wrap(err, "the sigil binary to copy can't be found", "run compile from an installed sigil binary, not through a link that no longer exists")
	}
	if sameFile(out, exe) {
		return "", humane.New("--out "+out+" is the sigil binary compile runs as", "name another file for the compiled binary; compile copies this binary and leaves it as it is")
	}
	return exe, nil
}

// notInput fails when out is a file the check read: a policy, kind or
// trusted file, or the configuration file.
func notInput(out string, r *diagnose.Result) humane.Error {
	all := slices.Concat(r.Files.Kinds, r.Files.Paths, r.Files.Trusted)
	read := make([]string, 0, len(all)+1)
	read = append(read, r.Config.File)
	for _, f := range all {
		read = append(read, f.Name)
	}
	for _, name := range read {
		if name != "" && name != stdinName && sameFile(out, name) {
			msg := "--out " + out + " is one of the files compile reads"
			if filepath.ToSlash(filepath.Clean(out)) != name {
				msg = "--out " + out + " is " + name + ", one of the files compile reads"
			}
			return humane.New(msg, "name another file for the compiled binary")
		}
	}
	return nil
}

// sameFile reports whether a and b are the same existing file.
func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// build reads the binary exe, writes data into its reserved area, and
// writes the result to out.
func build(exe, out string, data []byte) humane.Error {
	bin, err := os.ReadFile(exe) //nolint:gosec // the running binary, which compile copies

	if err != nil {
		return humane.Wrap(err, "the sigil binary "+exe+" couldn't be read", "check its permissions; compile copies the binary it runs as")
	}
	if err := stamp.Patch(bin, payload.Marker(), payload.AreaSize, data); err != nil {
		return patchError(exe, err)
	}
	return write(out, bin)
}

// patchError explains why stamp couldn't write the bundle into exe.
func patchError(exe string, err error) humane.Error {
	var large *stamp.TooLargeError
	switch {
	case errors.As(err, &large):
		return humane.Wrap(err,
			fmt.Sprintf("the bundle takes %d bytes compressed, %d more than a binary has room for (%d bytes)", large.Size, large.Size-large.Max, large.Max),
			"compile fewer files: name only the directories that hold the policies to ship and what they use",
		)
	case errors.Is(err, stamp.ErrSigned):
		return humane.Wrap(err,
			exe+" is signed with an identity, and compile can only update an ad-hoc signature",
			"compile with an unsigned or ad-hoc-signed sigil, such as one built with go build, and sign the compiled binary afterwards",
		)
	case errors.Is(err, stamp.ErrNoArea):
		return humane.Wrap(err,
			exe+" has no room for a bundle",
			"compile with a sigil release that has the compile command, or with a host binary built with the pkg/cli package",
		)
	case errors.Is(err, stamp.ErrManyAreas):
		return humane.Wrap(err, exe+" holds more than one reserved area", "this is a bug in sigil, please report it")
	case errors.Is(err, stamp.ErrUnsupported):
		return humane.Wrap(err,
			"compile can't write into "+exe,
			"compile with a sigil binary built for one platform; a universal macOS binary holds several",
		)
	case errors.Is(err, stamp.ErrMalformed):
		return humane.Wrap(err, exe+" is damaged", "install sigil again, or build it again")
	case errors.Is(err, stamp.ErrMarkerInData):
		return humane.Wrap(err, "the encoded bundle happens to hold the marker of the reserved area, so it can't be written into a binary", "compile again; if it keeps happening, report it as a bug in sigil")
	}
	return humane.Wrap(err, "the bundle couldn't be written into a copy of "+exe, "this is a bug in sigil, please report it")
}

// write writes bin to out: to a temporary file in out's directory, made
// executable, then renamed over out, so out is never half-written, and a
// running copy of a previous out keeps its file.
func write(out string, bin []byte) humane.Error {
	advice := "check that the directory of " + out + " exists and is writable"
	tmp, err := os.CreateTemp(filepath.Dir(out), "."+filepath.Base(out)+".*")
	if err != nil {
		return humane.Wrap(err, out+" couldn't be written", advice)
	}
	// Both are no-ops once the file is closed and renamed; they clean up
	// after a failure on the way.
	defer func() { _ = tmp.Close() }()
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.Write(bin)
	if err := errors.Join(werr, tmp.Close(), os.Chmod(tmp.Name(), 0o755)); err != nil { //nolint:gosec // a binary is meant to be run
		return humane.Wrap(err, out+" couldn't be written", advice)
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return humane.Wrap(err, out+" couldn't be written", "check that "+out+" isn't a directory, and that its directory is writable")
	}
	return nil
}
