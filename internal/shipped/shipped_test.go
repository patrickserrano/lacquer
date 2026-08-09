// Package shipped holds tests that lint the lacquer's OWN shipped content —
// the files under core/ and profiles/ that sync copies into every project.
//
// Everything else in this repo tests the Go that distributes content. Nothing
// tested the content itself, and content is what actually runs in seventeen
// repos' CI. The gap had teeth: profiles/ios/workflows/ci.yml parsed its test
// results with `xcresulttool get --format json`, a form current Xcode refuses
// outright ("--legacy flag is required to use it"). The designed parser had
// therefore never run on any modern toolchain, every run fell through to a
// regex hunting for text Swift Testing does not emit, and that fallback's
// default branch was `exit 0`. A real test failure could report green.
//
// No Go test could have caught it, because no Go test ever read the workflow.
// These do.
package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// root is the lacquer checkout these tests lint.
func root(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r, "VERSION")); err != nil {
		t.Skipf("not running from a lacquer checkout: %v", err)
	}
	return r
}

// banned is a pattern that must not appear in shipped content, and the reason.
//
// Each entry is a real incident, not a style preference. The `why` is printed on
// failure so whoever trips it learns what broke rather than just which regex
// matched.
type banned struct {
	name  string
	re    *regexp.Regexp
	why   string
	allow func(path, line string) bool // optional per-line exemption
}

var bans = []banned{
	{
		name: "deprecated xcresulttool invocation",
		// The legacy object form. `--legacy` makes it explicit and is still
		// accepted, so only the un-flagged call is banned.
		re: regexp.MustCompile(`xcresulttool\s+get\s+--format\s+json`),
		why: "current Xcode refuses `xcresulttool get --format json` " +
			"(\"--legacy flag is required to use it\") and exits non-zero with EMPTY stdout. " +
			"Shipped CI redirected stderr to /dev/null, so the parser silently never ran and the " +
			"job fell through to a log-scraping fallback whose default branch was a PASS. " +
			"Use `xcresulttool get test-results summary` (fields .result / .failedTests) or " +
			"`get test-results tests`, and fail closed when the parse is inconclusive.",
		allow: func(_, line string) bool {
			// A line that explicitly opts into the legacy API is a deliberate
			// choice; a line describing the ban is prose, not a call.
			return strings.Contains(line, "--legacy") || isProse(line)
		},
	},
}

// isProse reports whether a line is commentary rather than an invocation —
// a Markdown bullet, or a shell/YAML comment. These files document their own
// history, and a ban that cannot be explained in a comment is a ban nobody can
// understand when it fires.
func isProse(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") ||
		strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*")
}

// TestShippedContentAvoidsBannedPatterns walks every file sync would distribute.
func TestShippedContentAvoidsBannedPatterns(t *testing.T) {
	r := root(t)
	var checked int

	for _, dir := range []string{"core", "profiles", "archetypes"} {
		base := filepath.Join(r, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !isText(path) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checked++
			rel, _ := filepath.Rel(r, path)
			for _, line := range strings.Split(string(data), "\n") {
				for _, b := range bans {
					if !b.re.MatchString(line) {
						continue
					}
					if b.allow != nil && b.allow(rel, line) {
						continue
					}
					t.Errorf("%s: %s\n  line: %s\n  why: %s", rel, b.name, strings.TrimSpace(line), b.why)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// Guard the guard. A walk that silently matches nothing would pass forever
	// — the same shape of vacuous green this package exists to catch.
	if checked < 50 {
		t.Fatalf("only %d shipped files scanned; the walk is not reaching the content", checked)
	}
	t.Logf("scanned %d shipped files", checked)
}

// isText reports whether a path is worth scanning. Binary assets and lockfiles
// carry no shell.
func isText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yml", ".yaml", ".sh", ".md", ".toml", ".json", ".bash", ".zsh":
		return true
	}
	return false
}

// TestIOSCIFailsClosedOnAnInconclusiveParse pins the property that actually
// matters, rather than the spelling of the command.
//
// A future edit could adopt some third parsing API and still reintroduce the
// bug, because the defect was never the API — it was that "I could not tell"
// took the same branch as "it passed". The last word in that block must be a
// failure.
func TestIOSCIFailsClosedOnAnInconclusiveParse(t *testing.T) {
	r := root(t)
	data, err := os.ReadFile(filepath.Join(r, "profiles", "ios", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	start := -1
	for i, l := range lines {
		if strings.Contains(l, "if [ $EXIT_CODE -eq 65 ]") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("could not find the exit-65 handler in profiles/ios/workflows/ci.yml")
	}

	// Walk to the end of the handler and find its final decision.
	var lastDecision string
	for i := start; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "elif [ $EXIT_CODE -ne 0 ]") {
			break
		}
		if strings.HasPrefix(l, "exit ") {
			lastDecision = l
		}
	}
	if lastDecision == "" {
		t.Fatal("the exit-65 handler reaches no explicit exit")
	}
	if lastDecision == "exit 0" {
		t.Errorf("the exit-65 handler's last decision is %q — an inconclusive parse must never "+
			"exit 0. This exact branch reported a green PR on a run with a real, confirmed "+
			"test failure.", lastDecision)
	}
}
