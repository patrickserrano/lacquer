// Package inboxtest guards the operator's real inbox from test binaries.
//
// inbox.Path falls back to ~/.local/state/lacquer/inbox.jsonl, and code that
// now writes the inbox on its own (internal/producers) makes it easy for a test
// that forgot to name a temp file to append to the real one. A test never sees
// that as a failure, and the operator finds fabricated entries on their phone.
//
// The guard is on the write, not a before/after comparison of the file: the
// real inbox is live (other sessions append to it while a suite runs), so a
// comparison would blame the tests for someone else's entry.
package inboxtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Run is a TestMain body. It refuses every write to the real default inbox for
// the life of the test binary, and fails the binary if one was attempted, even
// when the code under test swallowed the error. Use it as
//
//	os.Exit(inboxtest.Run(m, func() int { return m.Run() }))
func Run(_ *testing.M, run func() int) int {
	real, _, err := inbox.Path("", os.Getenv)
	if err != nil {
		return run()
	}
	real = filepath.Clean(real)
	var tripped atomic.Bool
	inbox.WriteGuard = func(path string) error {
		if filepath.Clean(path) != real {
			return nil
		}
		tripped.Store(true)
		return fmt.Errorf("a test tried to write the real inbox %s; point it at a temp file (--inbox, LACQUER_INBOX or XDG_STATE_HOME)", real)
	}
	code := run()
	inbox.WriteGuard = nil
	if tripped.Load() {
		fmt.Fprintf(os.Stderr, "FAIL: a test tried to write the real inbox %s\n", real)
		return 1
	}
	return code
}
