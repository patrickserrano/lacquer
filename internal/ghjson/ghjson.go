// Package ghjson reads gh's JSON output where gh is known to print nothing for
// an empty list.
//
// Measured with gh 2.101.0: `gh label list -R <repo> --search <no match> --json
// name` exits 0 and writes ZERO BYTES, not `[]`. A bare json.Unmarshal calls
// that "unexpected end of JSON input". Every other gh list lacquer parses
// (issue list, search issues, pr list open and merged) was measured printing
// `[]`, so zero bytes from THOSE is abnormal and must stay an error: reading it
// as "nothing there" would turn a broken read into a clean one. Leniency is
// granted only where it was measured, and only through this package.
package ghjson

import (
	"bytes"
	"encoding/json"
)

// UnmarshalLabelList decodes the output of a SUCCESSFUL `gh label list --json`
// into v. Empty or whitespace-only output is an empty list and leaves v
// untouched; anything else non-empty must be valid JSON. A failed gh call is
// the caller's error to report before it gets here.
func UnmarshalLabelList(out []byte, v any) error {
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	return json.Unmarshal(out, v)
}
