//go:build eval

package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Scenario: comment-match.
//
// The generalized shape of a real incident (CLAUDE.md, "Three defects"): a
// detector keyed on a managed step's NAME rather than on whether a declared
// file was actually written reported a correctly-configured project as
// exposing App Store credentials, because the step's name string also matched
// elsewhere. internal/audit/inert.go's own doc comment names this exact bug and
// its fix: InertSecretDeclarations no longer keys on the step's name
// ("Write release configuration"), it keys on whether p.SecretsPath() appears
// ANYWHERE in a release workflow's text.
//
// That fix was real progress — cfgWithSecrets/TestDeclaredSecretsWithAWritingReleaseAreQuiet
// pins it — but "appears anywhere in the text" was still a substring match
// over the WHOLE workflow body, comments included, which issue #363 tracked:
// a `#` comment that merely mentions the secrets file name (e.g. left behind
// when the writing step was deleted, or added as a TODO before the step
// existed) satisfied strings.Contains(body, p.SecretsPath()) exactly as well
// as a real step that writes it.
//
// FIXED (issue #363, closed): InertSecretDeclarations now excludes a mention
// confined to a whole-line `#` comment (internal/audit/inert.go's
// hasNonCommentMention, since replaced by writesPath, which recognises writes
// rather than mentions and keeps excluding comments) while accepting every
// real write — a redirection, a heredoc body, a quoted script argument.
// This scenario is now an ordinary, unmarked pin of that fixed behavior
// rather than a strict expected-failure: a workflow whose ONLY mention of the
// declared secrets file is inside a `#` comment, with no step that actually
// writes it, is reported as inert.
func TestScenarioCommentMatch(t *testing.T) {
	recordScenario(t) // unmarked: the bug this scenario tracked is fixed.
	dir := t.TempDir()
	workflow := filepath.Join(dir, ".github", "workflows", "ios-release.yml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0o755); err != nil {
		t.Fatalf("setup failed: mkdir: %v", err)
	}
	// The real implementation was deleted; only an explanatory comment
	// mentioning the file remains — the exact fixture shape the brief
	// specifies: "delete the real implementation but leave the comment."
	body := "jobs:\n" +
		"  release:\n" +
		"    steps:\n" +
		"      # TODO: this step used to write Secrets.xcconfig here before the\n" +
		"      # release script was migrated; restore it before shipping secrets again.\n" +
		"      - run: xcodebuild archive\n"
	if err := os.WriteFile(workflow, []byte(body), 0o644); err != nil {
		t.Fatalf("setup failed: write workflow: %v", err)
	}
	if strings.Contains(body, "run: xcodebuild archive -exportOptions") {
		t.Fatal("setup failed: fixture body got corrupted in transcription")
	}

	cfg := &config.Config{Product: []config.Product{{
		Name: "Probe",
		Secrets: map[string]string{
			"REVENUECAT_API_KEY": "REVENUECAT_API_KEY",
		},
	}}}
	// Sanity: SecretsPath defaults to "Secrets.xcconfig", which is exactly the
	// string planted inside the comment above (not as a path, as prose).
	if got := cfg.Product[0].SecretsPath(); got != "Secrets.xcconfig" {
		t.Fatalf("setup failed: unexpected default SecretsPath %q", got)
	}

	findings := audit.InertSecretDeclarations(dir, cfg)

	// Issue #363, fixed: a declared secret whose only mention anywhere in the
	// release workflow is inside a `#` comment — with the writing step
	// deleted — must be reported as inert. InertSecretDeclarations
	// (internal/audit/inert.go) now excludes a mention confined to a
	// whole-line comment via writesPath instead of a bare
	// strings.Contains(body, p.SecretsPath()) over the WHOLE workflow text.
	if len(findings) == 0 {
		t.Errorf("a declared secret whose only mention anywhere in " +
			"the release workflow is inside a `#` comment — with the writing step deleted — " +
			"was NOT reported as inert. InertSecretDeclarations (internal/audit/inert.go) " +
			"should exclude a mention confined to a whole-line comment (writesPath) " +
			"while still catching a real write anywhere else in the text.")
	}
}

// Mutation-tested: replacing `len(findings) == 0` with `len(findings) >= 0`
// (i.e. an assertion that can never fail) makes TestScenarioCommentMatch
// above pass unconditionally — confirmed, then reverted. TestScenarioCommentMatchRealStepIsQuiet
// is the paired negative control for that same assertion: the same fixture
// shape with an ACTUAL writing step must NOT be flagged, so the grader is not
// simply "always report inert."
func TestScenarioCommentMatchRealStepIsQuiet(t *testing.T) {
	recordScenario(t) // unmarked: tallied into the package summary as-is.
	dir := t.TempDir()
	workflow := filepath.Join(dir, ".github", "workflows", "ios-release.yml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0o755); err != nil {
		t.Fatalf("setup failed: mkdir: %v", err)
	}
	body := "jobs:\n" +
		"  release:\n" +
		"    steps:\n" +
		"      - name: Write release configuration (Probe)\n" +
		"        run: scripts/write-release-config.sh \"Secrets.xcconfig\"\n"
	if err := os.WriteFile(workflow, []byte(body), 0o644); err != nil {
		t.Fatalf("setup failed: write workflow: %v", err)
	}
	cfg := &config.Config{Product: []config.Product{{
		Name:    "Probe",
		Secrets: map[string]string{"REVENUECAT_API_KEY": "REVENUECAT_API_KEY"},
	}}}
	if findings := audit.InertSecretDeclarations(dir, cfg); len(findings) != 0 {
		t.Errorf("a project whose release genuinely writes its secrets was flagged inert: %+v", findings)
	}
}
