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

// TestWritesPath pins what counts as writing the declared secrets file.
//
// Issue #363 (PR #378) established the first half: a `#` comment naming the
// file never executes, so it cannot write it, while every real write shape —
// a quoted argument to the managed writer, a redirection, a `cp` target, a
// write inside a heredoc body — must still count.
//
// The second half is what that version could not see. It asked whether the
// NAME appeared on a non-comment line, and the managed ios-ci.yml names
// Secrets.xcconfig on dozens: in its step name, in `find -name
// 'Secrets.xcconfig.example'`, in an existence test, and in the placeholder
// seed itself. Any one of them satisfied the check, on every iOS repository in
// the fleet, so the audit could never fire where the default path was in use.
// A mention is not a write, a file that merely contains the name is not the
// file, and copying the committed example into place is a placeholder seed,
// not the release writing values.
func TestWritesPath(t *testing.T) {
	const path = "Secrets.xcconfig"
	tests := []struct {
		name string
		body string
		want bool
	}{
		// --- not a write ---
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
			// Issue #363 under the new rule: a commented-out writer is still a
			// comment. The #363 fixtures name the file in prose, which is not a
			// write shape at all; this one is, and must still not count.
			name: "commented_out_writer",
			body: "          # scripts/write-release-config.sh \"Secrets.xcconfig\" \"API_KEY\"\n" +
				"          #cp template.xcconfig Secrets.xcconfig\n",
			want: false,
		},
		{
			// A trailing shell comment is as inert as a whole-line one.
			name: "write_inside_a_trailing_comment",
			body: "      - run: xcodebuild archive # then cp template.xcconfig Secrets.xcconfig\n",
			want: false,
		},
		{
			name: "step_name_only",
			body: "      - name: Create Secrets.xcconfig (build-time placeholder)\n        run: xcodebuild build\n",
			want: false,
		},
		{
			name: "the_example_is_named",
			body: "          find . -name 'Secrets.xcconfig.example' -print0\n",
			want: false,
		},
		{
			name: "existence_test_only",
			body: "          if [ ! -f \"$scheme_dir/Secrets.xcconfig\" ]; then echo missing; fi\n",
			want: false,
		},
		{
			name: "read_back_only",
			body: "          grep -nE 'your[-_]' xcconfig/Secrets.xcconfig\n",
			want: false,
		},
		{
			// PR #378 listed this shape as a write, citing CI's seed. It is the
			// defect: CI seeding placeholders is not the release writing values.
			name: "cp_from_the_example_is_a_seed",
			body: "      - run: cp Secrets.xcconfig.example Secrets.xcconfig\n",
			want: false,
		},
		{
			// Byte-for-byte the managed ci.yml seed, with a component prefix.
			name: "ci_placeholder_seed",
			body: "            cp \"Flare/Secrets.xcconfig.example\" \"$scheme_dir/Secrets.xcconfig\"\n",
			want: false,
		},
		{
			name: "mv_from_the_example_is_a_seed",
			body: "          mv -f Secrets.xcconfig.example Secrets.xcconfig\n",
			want: false,
		},
		{
			name: "the_file_is_only_a_cp_source",
			body: "          cp Secrets.xcconfig backup.xcconfig\n",
			want: false,
		},
		{
			name: "redirect_into_a_tmp_sibling",
			body: "          awk '{ print }' \"Secrets.xcconfig\" > \"Secrets.xcconfig.tmp\"\n",
			want: false,
		},
		{
			name: "redirect_into_a_name_that_contains_it",
			body: "          printf 'A = %s\\n' \"$A\" > OldSecrets.xcconfig\n" +
				"          printf 'A = %s\\n' \"$A\" > Secrets.xcconfig.bak\n",
			want: false,
		},

		// --- a write ---
		{
			// The hand-written redirection in TestAHandRolledWriterInAnotherWorkflowCounts.
			name: "redirection_write",
			body: "      - run: |\n" +
				"          : \"${API_KEY:?Missing API_KEY}\"\n" +
				"          printf 'API_KEY = %s\\n' \"$API_KEY\" > \"Secrets.xcconfig\"\n",
			want: true,
		},
		{
			name: "append_redirection_write",
			body: "          echo \"API_KEY = $API_KEY\" >> Secrets.xcconfig\n",
			want: true,
		},
		{
			name: "unspaced_redirection_write",
			body: "          echo \"API_KEY = $API_KEY\" >\"Secrets.xcconfig\"\n",
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
			// The 2 in 2>/dev/null is a file descriptor, not cp's destination.
			name: "cp_with_stderr_redirected",
			body: "          cp template.xcconfig Secrets.xcconfig 2>/dev/null\n",
			want: true,
		},
		{
			// The destination is the last operand of cp, not of the line.
			name: "cp_followed_by_another_command",
			body: "          cp template.xcconfig Secrets.xcconfig && echo seeded\n",
			want: true,
		},
		{
			name: "single_quoted_destination",
			body: "          cp template.xcconfig 'Secrets.xcconfig'\n",
			want: true,
		},
		{
			// A # inside a word is parameter expansion, not a comment.
			name: "hash_inside_a_word_is_not_a_comment",
			body: "          cp ${TEMPLATE#./} Secrets.xcconfig\n",
			want: true,
		},
		{
			name: "cp_from_something_other_than_the_example",
			body: "      - run: cp template.xcconfig Secrets.xcconfig\n",
			want: true,
		},
		{
			// The shape internal/tokens.ProductSecrets renders: the path as the
			// managed writer's first, quoted argument.
			name: "quoted_script_argument_write",
			body: "      - name: Write release configuration (Free)\n" +
				"        run: |\n" +
				"          scripts/write-release-config.sh \"Secrets.xcconfig\" \\\n" +
				"            \"API_KEY\"\n",
			want: true,
		},
		{
			// The declared path is relative to the component; the workflow runs
			// from the repository root, so the writer names it with a prefix.
			name: "component_prefixed_script_argument_write",
			body: "          scripts/write-release-config.sh \"Flare/Secrets.xcconfig\" \\\n" +
				"            \"REVENUECAT_PUBLIC_SDK_KEY=appl_*\"\n",
			want: true,
		},
		{
			name: "write_inside_heredoc_body",
			body: "      - run: |\n" +
				"          cat > setup.sh <<'EOF'\n" +
				"          cp template.xcconfig \"Secrets.xcconfig\"\n" +
				"          EOF\n",
			want: true,
		},
		{
			// CLAUDE.md, "Three defects": a correct hand-rolled step under a
			// name that is NOT "Write release configuration" is a writer.
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
			name: "comment_alongside_real_write",
			body: "      # Secrets.xcconfig is written below by the shipped script.\n" +
				"      - run: scripts/write-release-config.sh \"Secrets.xcconfig\"\n",
			want: true,
		},
		{
			// rail: a sed over the example, continued across lines, redirected
			// into place. Sourced from the example, but substituted — a write.
			name: "continued_sed_redirected_into_place",
			body: "          sed \\\n" +
				"            -e \"s|your_api_key_here|${API_KEY}|g\" \\\n" +
				"            -e \"s|https:/\\$()/your_dsn_here|${DSN_ESC}|g\" \\\n" +
				"            xcconfig/Secrets.xcconfig.example > xcconfig/Secrets.xcconfig\n",
			want: true,
		},
		{
			// momfriend: seed, rewrite into a .tmp, mv the .tmp into place.
			name: "seed_then_rewrite_then_mv",
			body: "          cp \"ios/Secrets.xcconfig.example\" \"ios/MomFriend/Secrets.xcconfig\"\n" +
				"          awk -v k=\"$KEY\" '\n" +
				"            /^KEY = / { print \"KEY = \" k; next }\n" +
				"            { print }\n" +
				"          ' \"ios/MomFriend/Secrets.xcconfig\" > \"ios/MomFriend/Secrets.xcconfig.tmp\"\n" +
				"          mv \"ios/MomFriend/Secrets.xcconfig.tmp\" \"ios/MomFriend/Secrets.xcconfig\"\n",
			want: true,
		},
		{
			name: "cp_continued_across_lines",
			body: "          cp \\\n" +
				"            \"template.xcconfig\" \\\n" +
				"            \"Secrets.xcconfig\"\n",
			want: true,
		},
		{
			name: "tee_write",
			body: "          printf 'KEY = %s\\n' \"$KEY\" | tee -a \"Secrets.xcconfig\" >/dev/null\n",
			want: true,
		},
		{
			// Seed, then substitute in place: the sed -i is the write.
			name: "seed_then_sed_in_place",
			body: "          cp Secrets.xcconfig.example Secrets.xcconfig\n" +
				"          sed -i '' -e \"s|REPLACE_ME|${KEY}|\" Secrets.xcconfig\n",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := writesPath(tt.body, path); got != tt.want {
				t.Errorf("writesPath(body, %q) = %v, want %v\nbody:\n%s", path, got, tt.want, tt.body)
			}
		})
	}
}

// A non-default secrets_file is matched as a whole path: the example beside
// it is a seed, and the same basename in another directory is another file.
func TestWritesPathForADeclaredSecretsFile(t *testing.T) {
	const path = "Config/Monetization.xcconfig"
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"redirection_write", "            } > \"Config/Monetization.xcconfig\"\n", true},
		{"example_seed", "            cp \"Config/Monetization.xcconfig.example\" \"Config/Monetization.xcconfig\"\n", false},
		{"another_directory", "            } > \"Other/Monetization.xcconfig\"\n", false},
		{"dot_slash_prefix", "            } > ./Config/Monetization.xcconfig\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := writesPath(tt.body, path); got != tt.want {
				t.Errorf("writesPath(body, %q) = %v, want %v\nbody:\n%s", path, got, tt.want, tt.body)
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
