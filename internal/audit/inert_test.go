package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func inertRepo(t *testing.T, releaseBody string) string {
	t.Helper()
	dir := t.TempDir()
	if releaseBody != "" {
		p := filepath.Join(dir, ".github", "workflows", "ios-release.yml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(releaseBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func cfgWithSecrets(exclude bool) *config.Config {
	c := &config.Config{}
	c.Product = []config.Product{{
		Name: "Probe",
		Secrets: map[string]string{
			"REVENUECAT_API_KEY": "REVENUECAT_API_KEY",
			"SENTRY_DSN":         "SENTRY_DSN",
		},
	}}
	if exclude {
		c.Project.Exclude = []config.Exclusion{{Path: ".github/workflows/ios-release.yml", Reason: "project-owned"}}
	}
	return c
}

// a-bible-verse-each-day, live. It declares three keys WITH secret_formats shape
// checks and excludes ios-release.yml, so nothing renders the step that writes
// them and every release archives with all three undefined. The declaration is
// the strongest evidence available that somebody meant them to be written, which
// is exactly why the silence is worth breaking.
func TestDeclaredSecretsWithAnExcludedReleaseAreReported(t *testing.T) {
	dir := inertRepo(t, "")
	fs := InertSecretDeclarations(dir, cfgWithSecrets(true))
	if len(fs) != 1 {
		t.Fatalf("inert declaration not reported: %+v", fs)
	}
	if !fs[0].Excluded {
		t.Error("the report does not record that the workflow was EXCLUDED — an exclusion is a " +
			"decision to revisit, an absence is a sync away, and the remedies differ")
	}
	out := FormatInertSecrets(fs)
	for _, want := range []string{"REVENUECAT_API_KEY", "SENTRY_DSN", "EXCLUDED", "UNDEFINED"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// The healthy case: Steps declares secrets and its rendered release workflow
// carries the step that consumes them.
func TestDeclaredSecretsWithAWritingReleaseAreQuiet(t *testing.T) {
	dir := inertRepo(t, "jobs:\n  release:\n    steps:\n      - name: Write release configuration (Steps)\n        run: scripts/write-release-config.sh \"Secrets.xcconfig\"\n")
	if fs := InertSecretDeclarations(dir, cfgWithSecrets(false)); len(fs) != 0 {
		t.Fatalf("a project whose release DOES write its secrets was flagged: %+v", fs)
	}
}

// Declaring nothing is not a finding. Most of the fleet is this, and reporting
// it would bury the one project that has the problem.
func TestNoDeclaredSecretsIsQuiet(t *testing.T) {
	c := &config.Config{}
	c.Product = []config.Product{{Name: "Probe"}}
	if fs := InertSecretDeclarations(inertRepo(t, ""), c); len(fs) != 0 {
		t.Fatalf("a project declaring no secrets was flagged: %+v", fs)
	}
}

// A release workflow that archives but never writes the declared file is the
// same exposure as having no release workflow. Presence is not the question;
// writing the file is.
func TestAReleaseThatNeverWritesTheFileIsReported(t *testing.T) {
	dir := inertRepo(t, "jobs:\n  release:\n    steps:\n      - run: xcodebuild archive\n")
	fs := InertSecretDeclarations(dir, cfgWithSecrets(false))
	if len(fs) != 1 {
		t.Fatalf("a release workflow with no write step was treated as consuming the secrets: %+v", fs)
	}
	if fs[0].Excluded {
		t.Error("reported as excluded when the file is present but simply lacks the step")
	}
}

// a-bible-verse-each-day, and the false positive this check shipped with for
// about an hour. It declares secrets_file = "Config/Monetization.xcconfig",
// EXCLUDES ios-release.yml, and carries a project-owned release with a
// hand-rolled step — "Create protected runtime configuration" — that reads every
// declared secret, fails closed on any unset, and writes exactly that file.
//
// The first version of this check asked whether ios-release.yml contained the
// literal string "Write release configuration". That reported a correct,
// fail-closed setup as broken: a false positive on the one project the check was
// built for. The question is whether ANYTHING writes the declared file, not
// whether the managed step is the thing writing it.
func TestAHandRolledWriterInAnotherWorkflowCounts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".github", "workflows", "ios-release.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `jobs:
  build:
    steps:
      - name: Create protected runtime configuration
        run: |
          : "${REVENUECAT_API_KEY:?Missing REVENUECAT_API_KEY}"
          printf 'REVENUECAT_API_KEY = %s\n' "$REVENUECAT_API_KEY" > "Config/Monetization.xcconfig"
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{}
	c.Product = []config.Product{{
		Name:        "Free",
		SecretsFile: "Config/Monetization.xcconfig",
		Secrets:     map[string]string{"REVENUECAT_API_KEY": "REVENUECAT_API_KEY"},
	}}
	c.Project.Exclude = []config.Exclusion{{Path: ".github/workflows/ios-release.yml", Reason: "project-owned"}}

	if fs := InertSecretDeclarations(dir, c); len(fs) != 0 {
		t.Fatalf("a project whose own workflow writes the declared file was flagged as inert: %+v", fs)
	}
}

// TestHasNonCommentMention is the table-driven pin for issue #363's fix: a
// `#` comment naming the declared secrets file must not count as writing it,
// while every real write shape found in profiles/*/workflows and
// profiles/ios/root/scripts/write-release-config.sh — a quoted script
// argument, a shell redirection, a `cp` target, a heredoc — must still count,
// including when it sits inside a heredoc body or a quoted string.
func TestHasNonCommentMention(t *testing.T) {
	const path = "Secrets.xcconfig"
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "whole_line_comment_only",
			body: "jobs:\n  release:\n    steps:\n      # Secrets.xcconfig is copied by CI\n      - run: xcodebuild archive\n",
			want: false,
		},
		{
			name: "indented_comment_only",
			body: "      #   Secrets.xcconfig used to be written here\n      - run: xcodebuild archive\n",
			want: false,
		},
		{
			name: "no_mention_at_all",
			body: "      - run: xcodebuild archive\n",
			want: false,
		},
		{
			// The hand-rolled writer in TestAHandRolledWriterInAnotherWorkflowCounts
			// above, restated as a redirection shape.
			name: "redirection_write",
			body: "      - run: |\n" +
				"          : \"${API_KEY:?Missing API_KEY}\"\n" +
				"          printf 'API_KEY = %s\\n' \"$API_KEY\" > \"Secrets.xcconfig\"\n",
			want: true,
		},
		{
			name: "heredoc_opening_line_write",
			body: "      - run: |\n" +
				"          cat > \"Secrets.xcconfig\" <<EOF\n" +
				"          API_KEY = ${API_KEY}\n" +
				"          EOF\n",
			want: true,
		},
		{
			name: "cp_write",
			body: "      - run: cp Secrets.xcconfig.example Secrets.xcconfig\n",
			want: true,
		},
		{
			// The shape internal/tokens.ReleaseSecretsSteps actually renders
			// (internal/tokens/tokens.go, ~line 1102): the path as a quoted
			// argument to the shipped writer, not a redirection at all.
			name: "quoted_script_argument_write",
			body: "      - name: Write release configuration (Free)\n" +
				"        run: |\n" +
				"          scripts/write-release-config.sh \"Secrets.xcconfig\" \\\n" +
				"            \"API_KEY\"\n",
			want: true,
		},
		{
			// A write line that happens to sit INSIDE a heredoc body must
			// still count — "skip everything inside a heredoc" would be the
			// blunt mistake, not the fix.
			name: "write_inside_heredoc_body",
			body: "      - run: |\n" +
				"          cat > setup.sh <<'EOF'\n" +
				"          cp template.xcconfig \"Secrets.xcconfig\"\n" +
				"          EOF\n",
			want: true,
		},
		{
			// The historical false positive this check exists to avoid
			// (CLAUDE.md, "Three defects"): a correct hand-rolled step under a
			// name that is NOT "Write release configuration" must still be
			// recognized as a writer.
			name: "hand_rolled_step_unexpected_name",
			body: "jobs:\n" +
				"  build:\n" +
				"    steps:\n" +
				"      - name: Create protected runtime configuration\n" +
				"        run: |\n" +
				"          : \"${REVENUECAT_API_KEY:?Missing REVENUECAT_API_KEY}\"\n" +
				"          printf 'REVENUECAT_API_KEY = %s\\n' \"$REVENUECAT_API_KEY\" > \"Secrets.xcconfig\"\n",
			want: true,
		},
		{
			// A comment above a genuine write must not suppress the write:
			// only lines that are THEMSELVES entirely comments are excluded.
			name: "comment_alongside_real_write",
			body: "      # Secrets.xcconfig is written below by the shipped script.\n" +
				"      - run: scripts/write-release-config.sh \"Secrets.xcconfig\"\n",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasNonCommentMention(tt.body, path); got != tt.want {
				t.Errorf("hasNonCommentMention(body, %q) = %v, want %v\nbody:\n%s", path, got, tt.want, tt.body)
			}
		})
	}
}

// TestCommentOnlyMentionIsReportedAsInert is the end-to-end pin for issue
// #363, wired through InertSecretDeclarations rather than the bare helper: a
// release workflow whose ONLY mention of the declared secrets file is inside
// a `#` comment, with no step that actually writes it, must be reported as
// inert. This is the same fixture shape as
// internal/eval/scenario_comment_match_test.go's TestScenarioCommentMatch,
// which tracked this as a known, expected failure against this issue; that
// scenario is updated alongside this fix.
func TestCommentOnlyMentionIsReportedAsInert(t *testing.T) {
	dir := inertRepo(t, "jobs:\n"+
		"  release:\n"+
		"    steps:\n"+
		"      # TODO: this step used to write Secrets.xcconfig here before the\n"+
		"      # release script was migrated; restore it before shipping secrets again.\n"+
		"      - run: xcodebuild archive\n")
	fs := InertSecretDeclarations(dir, cfgWithSecrets(false))
	if len(fs) != 1 {
		t.Fatalf("a comment-only mention of the declared secrets file was treated as a write: %+v", fs)
	}
}
