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
// That fix is real progress — cfgWithSecrets/TestDeclaredSecretsWithAWritingReleaseAreQuiet
// pins it — but "appears anywhere in the text" is still a substring match over
// the WHOLE workflow body, comments included. A `#` comment that merely
// mentions the secrets file name (e.g. left behind when the writing step was
// deleted, or added as a TODO before the step existed) satisfies
// strings.Contains(body, p.SecretsPath()) exactly as well as a real step that
// writes it.
//
// Known-correct verdict (machine-checkable): a workflow whose ONLY mention of
// the declared secrets file is inside a `#` comment, with no step that
// actually writes it, must still be reported as inert. A detector keyed on
// prose — even prose one level more specific than a step's display name — is
// invalid.
func TestScenarioCommentMatch(t *testing.T) {
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

	// Marked, strict expected-failure: issue #363 (whether/how to change the
	// documented over-acceptance trade-off in internal/audit/inert.go is an
	// open maintainer decision, not something this suite adjudicates). The
	// verdict condition is `len(findings) != 0` (true = the bug is fixed,
	// the comment-only mention now gets reported as inert). See expect.go's
	// expectKnownFailure — the setup above still calls t.Fatalf directly and
	// can never be absorbed by this.
	expectKnownFailure(t, issueCommentMatch, len(findings) != 0,
		"a declared secret whose only mention anywhere in "+
			"the release workflow is inside a `#` comment — with the writing step deleted — "+
			"was NOT reported as inert. InertSecretDeclarations (internal/audit/inert.go) "+
			"keys its 'written' check on strings.Contains(body, p.SecretsPath()) over the "+
			"WHOLE workflow text, which a comment satisfies exactly as well as a real step. "+
			"The detector is keyed on prose and is invalid for this input.")
}

// Mutation-tested: replacing `len(findings) == 0` with `len(findings) >= 0`
// (i.e. an assertion that can never fail) makes this test pass unconditionally
// — confirmed, then reverted. The real assertion currently reproduces a
// genuine, live gap in internal/audit/inert.go as shipped (found while this
// scenario was being built: the comment-fooled state is not hypothetical
// here, it reproduces today). It is marked as a strict expected-failure
// against issue #363 (see expect.go's expectKnownFailure) — whether and how
// to close that gap is an open maintainer decision (the substring-match
// over-acceptance is documented as deliberate in inert.go, to avoid the
// step-name false positive CLAUDE.md's "Three defects" describes), so this
// known, tracked trade-off does not turn the eval-suite job red.
// TestScenarioCommentMatchRealStepIsQuiet below is the paired negative
// control: the same fixture shape with an ACTUAL writing step must NOT be
// flagged, so the grader is not simply "always report inert."
func TestScenarioCommentMatchRealStepIsQuiet(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
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
