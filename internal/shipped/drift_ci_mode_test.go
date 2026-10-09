package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CI audits at the version .lacquer.lock names, which is the one place a Behind
// row cannot mean "the lacquer advanced" (lacquer#522, U11; internal/audit/ci.go).
// `audit --ci` turns it into exit 8. Two ways the shipped workflows could lose
// that, both silent: a drift step that runs `audit` without the flag reports
// Behind as routine again, and a `case` that does not fail on 8 passes it.

// auditInvocation matches a workflow line that runs the downloaded lacquer's
// audit subcommand.
var auditInvocation = regexp.MustCompile(`lacquer"?\s+audit\b`)

// TestEveryShippedDriftAuditRunsInCIMode scans every workflow under profiles/
// rather than a list, so a profile that gains a drift audit later is held to
// the same flag without anyone remembering to add it here.
func TestEveryShippedDriftAuditRunsInCIMode(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "profiles", "*", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "echo ") {
				continue
			}
			if !strings.Contains(trimmed, "LACQUER_ROOT=") || !auditInvocation.MatchString(trimmed) {
				continue
			}
			found++
			if !strings.Contains(trimmed, "audit --ci") {
				rel, _ := filepath.Rel(root(t), f)
				t.Errorf("%s:%d runs the drift audit without --ci, so a lock the named version "+
					"never wrote passes as Behind:\n  %s", rel, i+1, trimmed)
			}
		}
	}
	// One per profile that ships the drift step. Fewer means the scan matched
	// nothing it should have, and every assertion above was vacuous.
	if found < len(ciProfiles) {
		t.Fatalf("found %d drift audit invocations under profiles/, want at least %d (%v)",
			found, len(ciProfiles), ciProfiles)
	}
}

// TestDriftStepFailsOnLockMismatch executes the rendered drift step's own exit
// code handling with each code the audit can return, and reads back the
// verdict ci-ok enforces. Exit 8 has to leave `result` at fail AND say why.
func TestDriftStepFailsOnLockMismatch(t *testing.T) {
	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			var run string
			for _, st := range parseCI(t, profile).Jobs["changes"].Steps {
				if st.ID == "drift" {
					run = st.Run
				}
			}
			if run == "" {
				t.Fatal("no `drift` step in the changes job")
			}
			_, tail, ok := strings.Cut(run, "\ncode=$?\n")
			if !ok {
				t.Fatal("the drift step has no `code=$?` line after the audit — the exit code is never read")
			}
			for _, tc := range []struct {
				code       string
				wantResult string
				wantError  string
			}{
				{"0", "pass", ""},
				{"3", "fail", "::error::"},
				{"8", "fail", "::error::The managed files match .lacquer.lock's recorded hashes"},
			} {
				t.Run("exit "+tc.code, func(t *testing.T) {
					script := "set +e\nresult=fail\ntrap 'echo RESULT=$result' EXIT\ncode=" + tc.code + "\n" + tail
					cmd := exec.Command("bash", "-c", script)
					cmd.Env = append(os.Environ(), "LACQUER_TAG=v9.9.9")
					out, _ := cmd.CombinedOutput()
					if !strings.Contains(string(out), "RESULT="+tc.wantResult) {
						t.Errorf("exit %s left result != %s:\n%s", tc.code, tc.wantResult, out)
					}
					if tc.wantError != "" && !strings.Contains(string(out), tc.wantError) {
						t.Errorf("exit %s did not print %q:\n%s", tc.code, tc.wantError, out)
					}
					if tc.code == "8" && !strings.Contains(string(out), "v9.9.9") {
						t.Errorf("exit 8's message does not name the lacquer version:\n%s", out)
					}
				})
			}
		})
	}
}
