// Package inboxtest guards the operator's real inbox from test binaries.
//
// inbox.Path falls back to ~/.local/state/lacquer/inbox.jsonl, and code that
// now writes the inbox on its own (internal/producers) makes it easy for a test
// that forgot to name a temp file to append to the real one. A test never sees
// that as a failure, and the operator finds fabricated entries on their phone.
package inboxtest

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Run is a TestMain body: it snapshots the real default inbox before the
// tests, runs them, and fails the binary if the file changed. Use it as
//
//	os.Exit(inboxtest.Run(m, func() int { return m.Run() }))
func Run(_ *testing.M, run func() int) int {
	path, _, err := inbox.Path("", os.Getenv)
	if err != nil {
		return run()
	}
	before, beforeErr := os.ReadFile(path)
	code := run()
	after, afterErr := os.ReadFile(path)
	if os.IsNotExist(beforeErr) != os.IsNotExist(afterErr) || !bytes.Equal(before, after) {
		fmt.Fprintf(os.Stderr, "FAIL: a test wrote to the real inbox %s; point the test at a temp file (--inbox, LACQUER_INBOX or XDG_STATE_HOME)\n", path)
		return 1
	}
	return code
}
