package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The definitions section is report-only: a malformed rendered agent shows up
// in the audit output and does NOT move the exit code, the same as the hooks,
// shadow and pins sections. Both halves are asserted, because "printed but
// gated" and "gated but silent" are each a plausible wiring mistake.
func TestAuditReportsMalformedDefinitionsWithoutChangingTheExitCode(t *testing.T) {
	hr, pr := auditFixture(t, pbxCompliant, "")
	agents := filepath.Join(pr, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "colon.md"), []byte("---\nname: a:b\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, pr)
	// No claude on PATH: the test must not depend on the machine it runs on.
	t.Setenv("PATH", t.TempDir())

	var out, errb bytes.Buffer
	code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (report-only)\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	for _, want := range []string{".claude/agents/colon.md:2", "contains ':'", "not checked (claude CLI not found)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("audit output missing %q:\n%s", want, out.String())
		}
	}
}
