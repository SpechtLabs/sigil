// Package input reads bounded request files and HTTP bodies, so a stray
// file or a wrong --url can't make demo-cli buffer without limit.
package input

import (
	"fmt"
	"io"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// ReadLimited reads r to the end and returns what it read, or an error when
// r holds more than limit bytes or fails.
func ReadLimited(r io.Reader, limit int64) ([]byte, humane.Error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, humane.Wrap(err, "cannot read the request or response", "check the input file or the connection to deploygate")
	}
	if int64(len(body)) > limit {
		return nil, humane.New(fmt.Sprintf("body exceeds the %d-byte limit", limit), "use a smaller request or check that --url points to deploygate")
	}
	return body, nil
}
