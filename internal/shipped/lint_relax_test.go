package shipped

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
)

// Each tool must read only its own debt and expiry, while still executing.
func TestLintRelaxationRunsAndExpires(t *testing.T) {
	for _, linked := range []bool{false, true} {
		for _, tc := range []struct {
			name                    string
			dates                   map[string]string
			lintOK, formatOK, clean bool
		}{
			{"no debt", nil, false, false, false},
			{"lint only", map[string]string{"lint": "2099-01-01"}, true, false, false},
			{"format only", map[string]string{"format": "2099-01-01"}, false, true, false},
			{"both active", map[string]string{"lint": "2099-01-01", "format": "2099-02-01"}, true, true, false},
			{"lint expired format active", map[string]string{"lint": "2000-01-01", "format": "2099-01-01"}, false, true, false},
			{"format expired lint active", map[string]string{"lint": "2099-01-01", "format": "2000-01-01"}, true, false, false},
			{"both expired", map[string]string{"lint": "2000-01-01", "format": "2000-02-01"}, false, false, false},
			{"lint expiry day", map[string]string{"lint": "2026-11-07"}, true, false, false},
			{"format expiry day", map[string]string{"format": "2026-11-07"}, false, true, false},
			{"other key", map[string]string{"documentation": "2099-01-01"}, false, false, false},
			{"malformed lint", map[string]string{"lint": "invalid"}, false, false, false},
			{"malformed format", map[string]string{"format": "invalid"}, false, false, false},
			{"clean active", map[string]string{"lint": "2099-01-01", "format": "2099-02-01"}, true, true, true},
			{"clean lint expired", map[string]string{"lint": "2000-01-01", "format": "2099-01-01"}, false, true, true},
			{"clean format expired", map[string]string{"lint": "2099-01-01", "format": "2000-01-01"}, true, false, true},
		} {
			t.Run(fmt.Sprintf("%s/linked=%v", tc.name, linked), func(t *testing.T) {
				cfg := soloConfig()
				cfg.Baseline.Relax = map[string]baseline.Relax{}
				manifest := "[baseline.relax]\n"
				for _, key := range []string{"lint", "format", "documentation"} {
					if until, ok := tc.dates[key]; ok {
						cfg.Baseline.Relax[key] = baseline.Relax{Until: until, Reason: "existing tool-specific debt"}
						manifest += fmt.Sprintf("%s = { until = %q, reason = \"existing tool-specific debt\" }\n", key, until)
					}
				}
				doc := parseIOSCI(t, cfg)
				repo := t.TempDir()
				tree(t, repo, ".swiftlint.yml", ".swiftformat", "App.swift")
				writeSPMFile(t, repo, ".lacquer.toml", manifest)
				helper, err := os.ReadFile(filepath.Join(root(t), "core/root/scripts/docs-relaxation.sh"))
				if err != nil {
					t.Fatal(err)
				}
				writeSPMFile(t, repo, "scripts/docs-relaxation.sh", string(helper))
				if err := os.Chmod(filepath.Join(repo, "scripts/docs-relaxation.sh"), 0755); err != nil {
					t.Fatal(err)
				}
				initRepo(t, repo)
				if linked {
					wt := filepath.Join(t.TempDir(), "linked")
					gitIn(t, repo, "worktree", "add", "--detach", wt, "HEAD")
					repo = wt
				}
				bin := t.TempDir()
				stubTool(t, bin, "date", "echo 2026-11-07\n")
				exitCode := "2"
				if tc.clean {
					exitCode = "0"
				}
				for _, tool := range []string{"swiftlint", "swiftformat"} {
					stubTool(t, bin, tool, "echo "+tool+"-ran\nexit "+exitCode+"\n")
				}
				for _, step := range []struct {
					name, tool string
					ok         bool
				}{{"Run SwiftLint", "swiftlint", tc.lintOK}, {"Check SwiftFormat", "swiftformat", tc.formatOK}} {
					out, code := runStepIn(t, repo, stepRun(t, doc, "lint", step.name), bin, "SWIFTFORMAT_BIN="+filepath.Join(bin, "swiftformat"))
					if (code == 0) != step.ok {
						t.Errorf("%s exit %d, want success=%v\n%s", step.name, code, step.ok, out)
					}
					if !strings.Contains(out, step.tool+"-ran") {
						t.Errorf("%s did not execute: %s", step.name, out)
					}
					key := map[string]string{"swiftlint": "lint", "swiftformat": "format"}[step.tool]
					if step.ok && tc.dates[key] != "" && !strings.Contains(out, "::warning::") {
						t.Errorf("%s admitted debt without reporting it: %s", step.name, out)
					}
				}
			})
		}
	}
}
