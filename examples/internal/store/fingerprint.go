package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"path"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// Fingerprint hashes the names and contents of every `.sigil` file in fsys,
// the files the loader reads, walking fsys the way the loader does: entries
// whose names start with `.` are skipped, and what is a directory is decided
// with fs.Stat, so symbolic links to directories are followed. That matters
// for a mounted ConfigMap, whose keys and projected directories are links
// into kubelet's ..data directory: the hash follows the ..data swap even
// though the links themselves never change. Hashing contents rather than
// modification times also ignores a touch or a checkout that rewrote files
// with the same bytes.
func Fingerprint(fsys fs.FS) (string, humane.Error) {
	h := sha256.New()
	if herr := hashDir(h, fsys, "."); herr != nil {
		return "", humane.Wrap(herr, "fingerprinting the team policies failed",
			"check that the policies directory exists and is readable")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDir adds every `.sigil` file below dir to h, in the order fs.ReadDir
// returns them, which is sorted by name.
func hashDir(h hash.Hash, fsys fs.FS, dir string) humane.Error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return humane.Wrap(err, "listing "+dir+" failed", "check the directory's permissions")
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := path.Join(dir, e.Name())
		info, err := fs.Stat(fsys, name)
		if err != nil {
			return humane.Wrap(err, name+" can't be read", "a symbolic link there may be dangling")
		}

		switch {
		case info.IsDir():
			if herr := hashDir(h, fsys, name); herr != nil {
				return herr
			}
		case path.Ext(name) == ".sigil":
			data, err := fs.ReadFile(fsys, name)
			if err != nil {
				return humane.Wrap(err, "reading "+name+" failed", "check the file's permissions")
			}
			// Length-prefixed, so moving bytes between a name and a file
			// can't produce the same hash.
			_, _ = fmt.Fprintf(h, "%d:%s%d:", len(name), name, len(data))
			_, _ = h.Write(data)
		}
	}
	return nil
}
