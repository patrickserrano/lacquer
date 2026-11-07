package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
)

// A dated relaxation may admit existing violations, but must still run lint,
// reject expired/malformed debt, and keep an unrelated relaxation isolated.
func TestLintRelaxationRunsAndExpires(t *testing.T) {
	for _, linked := range []bool{false, true} {
		for _, tc := range []struct {
			name, date, key string
			relaxed         bool
			clean           bool
		}{
			{"active", "2099-01-01", "lint", true, false},
			{"expired", "2000-01-01", "lint", false, false},
			{"other key", "2099-01-01", "documentation", false, false},
			{"clean active", "2099-01-01", "lint", true, true},
			{"clean expired", "2000-01-01", "lint", false, true},
			{"expiry day", "2026-11-07", "lint", true, false},
			{"malformed", "invalid", "lint", false, false},
		} {
			t.Run(tc.name+map[bool]string{true: "/worktree", false: "/checkout"}[linked], func(t *testing.T) {
				cfg := soloConfig()
				cfg.Baseline.Relax = map[string]baseline.Relax{tc.key: {Until: tc.date, Reason: "existing style debt"}}
				doc := parseIOSCI(t, cfg)
				repo := t.TempDir()
				tree(t, repo, ".swiftlint.yml", ".swiftformat", "App.swift")
				writeSPMFile(t, repo, ".lacquer.toml", "[baseline.relax]\n"+tc.key+" = { until = \""+tc.date+"\", reason = \"existing style debt\" }\n")
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
					stubTool(t, bin, tool, "echo lint-ran\nexit "+exitCode+"\n")
				}
				for _, name := range []string{"Run SwiftLint", "Check SwiftFormat"} {
					body := stepRun(t, doc, "lint", name)
					out, code := runStepIn(t, repo, body, bin, "SWIFTFORMAT_BIN="+filepath.Join(bin, "swiftformat"))
					if (code == 0) != tc.relaxed {
						t.Fatalf("%s exit %d, relaxed=%v\n%s", name, code, tc.relaxed, out)
					}
					if tc.relaxed && (!strings.Contains(out, "lint-ran") || !strings.Contains(out, "::warning::")) {
						t.Fatalf("%s did not run and report relaxed lint: %s", name, out)
					}
				}
			})
		}
	}
}
