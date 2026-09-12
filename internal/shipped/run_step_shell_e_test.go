package shipped

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// This file guards a 2026-09 defect: a `run:` step written as
// `set -uo pipefail` -- deliberately WITHOUT `e`, per the comment sitting
// right above it -- actually executes under `bash -e {0}`, because that is
// GitHub's default shell for a `run:` step that declares no `shell:` key, and
// `set -uo pipefail` does not clear a `-e` the shell already had. Three
// shipped steps depend on continuing past a command's non-zero exit to reach
// their own diagnostics (an EXIT trap publishing a verdict, or a `case`
// mapping exit codes to `::error::` annotations), and under the real default
// shell they never did: the script died mid-script, before any of that ran,
// with no annotation explaining why. It failed SAFE -- the trap still ran --
// but every diagnostic bolted on top of that safety net was unreachable.
//
// The fix pairs each such step with an explicit `set +e`, which is the only
// thing that actually clears an inherited `-e`. This test must never key on
// the COMMENT claiming `-e` is absent -- that comment is exactly what was
// wrong, on every step this defect touched. It keys on the command text
// alone: hasCommand below only recognises a real, uncommented shell command.

// runStepDoc captures just enough of a rendered GitHub Actions workflow to
// find every run: step's script and its shell: override, across every job.
type runStepDoc struct {
	Jobs map[string]struct {
		Steps []struct {
			Name  string `yaml:"name"`
			Run   string `yaml:"run"`
			Shell string `yaml:"shell"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// shippedWorkflowFiles finds every workflow file lacquer ships under
// <dir>/profiles/*/workflows/*.yml, sorted for stable failure output. It
// takes a directory rather than always reading `root(t)` so a test can point
// it at an empty directory and prove the "found nothing" path actually fails
// instead of passing vacuously.
func shippedWorkflowFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "profiles", "*", "workflows", "*.yml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// hasCommand reports whether `script` runs `want` as an actual shell command,
// rather than merely mentioning it in a comment. A line only counts once any
// trailing `# ...` comment is stripped from it, and a line that is entirely a
// comment (or blank) is skipped outright. This is the whole mechanism that
// keeps the detector off comment wording: this repo shipped a defect where
// the comment said the right thing and the command did not, so a check that
// trusts the comment cannot be the fix for it.
func hasCommand(script, want string) bool {
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// shellAddsE reports whether the given `shell:` value runs the script with
// errexit active. The empty string is GitHub's default on a Linux or macOS
// runner for a `run:` step with no `shell:` key -- `bash -e {0}` -- which is
// the exact fact this whole defect turned on, so the empty string must be
// treated as adding `-e`, not as "unspecified, assume safe".
func shellAddsE(shell string) bool {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return true
	}
	fields := strings.Fields(shell)
	for _, f := range fields[1:] { // skip the interpreter name itself (e.g. "bash")
		if strings.HasPrefix(f, "-") && strings.Contains(f, "e") {
			return true
		}
	}
	return false
}

// renderShippedWorkflow renders one shipped workflow file the way sync would,
// using the same generic manifest drift_gate_test.go's renderCI already
// proves substitutes cleanly for every ci.yml the fleet ships. It is reused
// here, unmodified, for every OTHER shipped workflow file too, so this test
// does not need a bespoke manifest per file.
func renderShippedWorkflow(t *testing.T, path string) (string, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(ciConfig(), ""))
	if len(missing) > 0 {
		return "", fmt.Errorf("unsubstituted tokens: %v", missing)
	}
	return out, nil
}

// TestNoUOPipefailStepRunsUnderImplicitDashE parses every shipped workflow
// and asserts: any `run:` script containing the literal command
// `set -uo pipefail` (i.e. `-u` and `-o pipefail`, deliberately without `-e`)
// must also either run `set +e` as an actual command, or declare a `shell:`
// whose own value does not add `-e`. Absent both, the step's `-e`-absence is
// fictional under GitHub's real default shell -- which is precisely the
// defect this guards.
//
// If this parses zero workflow files, or finds zero `set -uo pipefail`
// steps, it fails outright rather than reporting a vacuous pass -- a
// selector that silently matches nothing is its own defect class in this
// repo (see CLAUDE.md).
func TestNoUOPipefailStepRunsUnderImplicitDashE(t *testing.T) {
	paths, err := shippedWorkflowFiles(root(t))
	if err != nil {
		t.Fatalf("shippedWorkflowFiles: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("shippedWorkflowFiles found zero workflow files under profiles/*/workflows -- the glob is broken, not the fleet; this test must not pass vacuously")
	}

	var checkedSteps int
	var flaggedSteps int
	var violations []string

	for _, path := range paths {
		rel, err := filepath.Rel(root(t), path)
		if err != nil {
			rel = path
		}

		out, err := renderShippedWorkflow(t, path)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}

		var doc runStepDoc
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: rendered workflow is not valid YAML: %v", rel, err)
		}
		if len(doc.Jobs) == 0 {
			t.Fatalf("%s: parsed zero jobs -- the renderer or the parser is broken, not the fleet", rel)
		}

		// Sort job names for stable failure output across runs.
		jobNames := make([]string, 0, len(doc.Jobs))
		for name := range doc.Jobs {
			jobNames = append(jobNames, name)
		}
		sort.Strings(jobNames)

		for _, jobName := range jobNames {
			job := doc.Jobs[jobName]
			for _, step := range job.Steps {
				if step.Run == "" {
					continue
				}
				checkedSteps++

				if !hasCommand(step.Run, "set -uo pipefail") {
					continue
				}
				flaggedSteps++

				if hasCommand(step.Run, "set +e") {
					continue // -e is actually cleared before it can bite
				}
				if !shellAddsE(step.Shell) {
					continue // declared shell doesn't add -e in the first place
				}

				stepName := step.Name
				if stepName == "" {
					stepName = "(unnamed step)"
				}
				violations = append(violations, fmt.Sprintf(
					"%s job %q step %q: runs `set -uo pipefail` (no `e`) with no `set +e` command, under a shell that adds `-e` (shell=%q) -- this step's -e-absence is fictional under GitHub's real default shell (`bash -e {0}`)",
					rel, jobName, stepName, step.Shell,
				))
			}
		}
	}

	if checkedSteps == 0 {
		t.Fatal("found zero run: steps across every shipped workflow -- the parser is broken, not the fleet; this test must not pass vacuously")
	}
	if flaggedSteps == 0 {
		t.Fatal("found zero `set -uo pipefail` steps across every shipped workflow -- either the pattern moved or the detector is broken; this test must not pass vacuously")
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("%d step(s) claim -e is absent via `set -uo pipefail` but actually run under bash's real default -e:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
