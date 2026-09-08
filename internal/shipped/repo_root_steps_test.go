package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestComponentJobsPinStepsThatReadRepoRootFiles guards a bug class that is
// INVISIBLE to single-component projects, which is why it ships easily.
//
// Some jobs declare `defaults.run.working-directory: "{{COMPONENT_PREFIX}}."`
// so the stack's own commands run in the component. Files that live at the
// REPOSITORY root — .lacquer.toml, .lacquer.lock — are then not where a
// relative path finds them, and the step reports the project as unmanaged or
// never-synced instead of failing in a way that names the cause.
//
// This happened. The version-resolve step added beside "Prove the checks can
// fail" did not carry that step's pin, and every multi-component project went
// red at once:
//
//	##[error].lacquer.lock is missing, so there is no synced version to prove
//	the checks against. Run `lacquer sync` to establish the baseline.
//
// Single-component projects stayed green throughout — momfriend (admin/,
// server/) and multimeter were the only casualties. A comment cannot carry
// that, because the person adding the next step reads the step above it, and
// the step above it was already correct.
func TestComponentJobsPinStepsThatReadRepoRootFiles(t *testing.T) {
	rootFiles := []string{".lacquer.lock", ".lacquer.toml"}

	for _, profile := range []string{"ios", "web", "supabase"} {
		path := filepath.Join(root(t), "profiles", profile, "workflows", "ci.yml")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		// Split on real job boundaries — a line at exactly two spaces of indent
		// ending in a colon. Splitting on "\n  " instead shatters the file into
		// hundreds of fragments (553 for the web profile), and the fragment
		// holding the component default then does not contain the steps, so the
		// test passes while the bug is present. That draft existed; this is why
		// the boundary is anchored.
		jobStart := regexp.MustCompile(`(?m)^  ([a-z0-9-]+):$`)
		locs := jobStart.FindAllStringSubmatchIndex(string(raw), -1)
		for i, loc := range locs {
			end := len(raw)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			jobChunk := string(raw[loc[0]:end])
			name := string(raw[loc[2]:loc[3]])
			if !strings.Contains(jobChunk, `working-directory: "{{COMPONENT_PREFIX}}."`) {
				continue
			}
			for _, step := range strings.Split(jobChunk, "      - name: ")[1:] {
				body := step
				if i := strings.Index(step, "\n      - "); i >= 0 {
					body = step[:i]
				}
				title := strings.SplitN(body, "\n", 2)[0]

				var reads string
				for _, f := range rootFiles {
					for _, line := range strings.Split(body, "\n") {
						trimmed := strings.TrimSpace(line)
						if strings.HasPrefix(trimmed, "#") {
							continue
						}
						// A step that NAMES the file in a diagnostic is not a step
						// that READS it. "Run pgTAP tests" mentions .lacquer.toml in
						// an ::error:: telling the reader where to add a relaxation,
						// while reading only supabase/tests/*.sql — correctly relative
						// to the component. Flagging it would demand a pin that would
						// break the glob it actually depends on.
						if strings.HasPrefix(trimmed, "echo ") ||
							strings.Contains(trimmed, "::error::") ||
							strings.Contains(trimmed, "::warning::") ||
							strings.Contains(trimmed, "::notice::") {
							continue
						}
						if strings.Contains(trimmed, f) {
							reads = f
						}
					}
				}
				if reads == "" {
					continue
				}
				// Both spellings appear in these files.
				if !strings.Contains(body, `working-directory: "."`) && !strings.Contains(body, "working-directory: .") {
					t.Errorf("profiles/%s/workflows/ci.yml: job %q defaults into the component, and its "+
						"step %q reads %s, which lives at the REPOSITORY root — but the step does not pin "+
						"`working-directory: \".\"`. On a multi-component project this reads a path that "+
						"does not exist and reports the project as unmanaged instead of failing usefully.",
						profile, name, title, reads)
				}
			}
		}
	}
}
