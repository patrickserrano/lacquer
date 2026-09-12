//go:build eval

package eval

import (
	"fmt"
	"os"
	"testing"
)

// TestMain prints one summary line after every test in this package has run:
// pass / expected-fail / fail (see expect.go). Its whole purpose is that a
// GREEN `go test -tags eval ./internal/eval/...` still visibly names the
// scenarios reproducing known, tracked bugs, rather than looking identical to
// "nothing is wrong" — see doc.go's rationale and this repo's own CLAUDE.md
// on a check that cannot distinguish "verified and passing" from "never ran"
// (here: from "silently swallowed").
func TestMain(m *testing.M) {
	code := m.Run()
	fmt.Println(results.report())
	os.Exit(code)
}
