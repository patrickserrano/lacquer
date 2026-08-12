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
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
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
	{
		name: "npx invocation of a project dependency",
		// `npx <tool>` prefers a local install but SILENTLY DOWNLOADS one when
		// there is none. A project that never added the dependency therefore
		// gets a green step, run by whatever version npm served that minute,
		// against a config nobody chose.
		re: regexp.MustCompile(`npx\s+(biome|typedoc|vitest|tsc)\b`),
		why: "npx silently downloads a missing tool instead of failing, so a project without the " +
			"dependency gets a green check run by an unpinned version. sleevetap had biome.json synced, " +
			"@biomejs/biome in no package.json, and a PASSING Biome step — only `lacquer doctor` noticed " +
			"the check could not be running at all. Call ./node_modules/.bin/<tool> so a missing " +
			"dependency fails loudly, which is also what every doctor probe already does.",
		allow: func(_, line string) bool { return isProse(line) },
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

// TestRenderedWorkflowsAreValidYAML parses what SHIPS, not what is authored.
//
// Templates used to be parseable YAML on their own, which was a convenient
// property and is how this repo extracted every `run:` block to syntax-check it.
// {{WEB_BUILD_ENV}} gives that up: it must render a whole job-level `env:` block
// or nothing at all, so it stands alone at column 0 and the template no longer
// parses.
//
// Losing an authoring convenience is only acceptable if it is replaced by
// something stronger, so this checks the rendered result instead — which is what
// actually runs. Both cases matter: a project WITH build_env must get a
// well-formed env block, and one WITHOUT must get no stray key, no orphaned
// `env:`, and no broken indentation where the token used to be.
func TestRenderedWorkflowsAreValidYAML(t *testing.T) {
	r := root(t)

	cases := []struct {
		name     string
		proj     config.Project
		wantEnv  bool
		wantKeys []string
	}{
		{
			name: "no build_env (the common case)",
			proj: config.Project{ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
				AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme"},
			wantEnv: false,
		},
		{
			name: "build_env set (pixelfoxstudio's case)",
			proj: config.Project{ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
				AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
				BuildEnv: []string{"NEXT_PUBLIC_SANITY_PROJECT_ID", "SANITY_API_READ_TOKEN"}},
			wantEnv:  true,
			wantKeys: []string{"NEXT_PUBLIC_SANITY_PROJECT_ID", "SANITY_API_READ_TOKEN"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(r, "profiles", "web", "workflows", "ci.yml"))
			if err != nil {
				t.Fatal(err)
			}
			out, missing := tokens.Substitute(string(raw), tokens.Values(tc.proj, ""))
			if len(missing) > 0 {
				t.Fatalf("unsubstituted tokens: %v", missing)
			}
			// Match lacquer tokens only. GitHub's own ${{ ... }} expressions
			// legitimately contain "{{", so a bare Contains check reports every
			// workflow as unrendered.
			if m := lacquerToken.FindString(out); m != "" {
				t.Errorf("rendered output still contains the lacquer token %s", m)
			}

			var doc struct {
				Jobs map[string]struct {
					Env map[string]string `yaml:"env"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("rendered workflow is not valid YAML: %v", err)
			}
			check, ok := doc.Jobs["check"]
			if !ok {
				t.Fatal("rendered workflow has no `check` job")
			}
			if !tc.wantEnv {
				if len(check.Env) != 0 {
					t.Errorf("expected no job env, got %v — an empty build_env must leave no stray key", check.Env)
				}
				// A bare `env:` with nothing under it unmarshals to a nil map, so
				// the length check above would not notice it. Actions tolerates
				// that, but it is a dangling artefact of the token and would
				// quietly become the place someone hand-adds a value.
				for _, l := range strings.Split(out, "\n") {
					if strings.TrimRight(l, " ") == "    env:" {
						t.Error("empty build_env still rendered a dangling `env:` key")
					}
				}
				return
			}
			for _, k := range tc.wantKeys {
				v, ok := check.Env[k]
				if !ok {
					t.Errorf("env is missing %q (got %v)", k, check.Env)
					continue
				}
				if want := "${{ secrets." + k + " }}"; v != want {
					t.Errorf("env[%s] = %q, want %q — the value must come from secrets, never the manifest", k, v, want)
				}
			}
		})
	}
}

// lacquerToken matches an unrendered {{TOKEN}} while ignoring GitHub Actions'
// ${{ ... }} expressions, which are not ours to substitute.
var lacquerToken = regexp.MustCompile(`(^|[^$])(\{\{[A-Z_]+\}\})`)

// TestOperatorPackagesNameNoProject enforces the boundary that makes `lacquer
// fleet` safe to ship in a PUBLIC repository while every project it sweeps is
// private.
//
// The roster belongs to the operator, not to this tool. A project name reaching
// this package would be a privacy leak with no upside — and the leak would be
// permanent, because this repo's history is public. The names below are the
// ones this fleet actually uses; the check is a tripwire for the habit, not an
// exhaustive filter.
func TestOperatorPackagesNameNoProject(t *testing.T) {
	r := root(t)
	// Distinctive names only. Short or dictionary-word names ("rail", "kit",
	// "steps") would false-positive on ordinary prose like "guardrail" or
	// "toolkit", and a guard that cries wolf gets deleted.
	names := []string{
		"windsock", "throughline", "queueify", "pixelfoxstudio",
		"needledrop", "sleevetap", "shelflife", "darndest", "mindmint",
	}
	var scanned int
	for _, pkg := range []string{"fleet", "console"} {
		dir := filepath.Join(r, "internal", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			scanned++
			low := strings.ToLower(string(data))
			for _, n := range names {
				if strings.Contains(low, n) {
					t.Errorf("internal/%s/%s names the project %q. "+
						"This package ships in a PUBLIC repo and operates on PRIVATE projects; "+
						"the roster is the operator's, and a name here leaks permanently into public history.",
						pkg, e.Name(), n)
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no operator-facing source files; the guard is not reaching the packages")
	}
}

// TestRetentionDaysAreExplainable catches a retention value that promises more
// than the platform will deliver.
//
// A shipped release workflow asked for 90-day dSYM retention and carried a
// comment explaining why: dSYMs are not reproducible from a later build and
// crash reports arrive for weeks. The reasoning was right and the number was
// fiction — an org or repo artifact-retention limit silently truncates any
// larger value, and this fleet's is 2 days. The file promised three months of
// symbolication that never existed, and the only way to discover that was to go
// looking for a dSYM on the day you needed one.
//
// The limit is the operator's, not this tool's, so this cannot check the number
// against it. What it can require is that any value above a conservative
// threshold is DISCUSSED — because the failure was never the number, it was a
// number nobody had reconciled with the platform.
func TestRetentionDaysAreExplainable(t *testing.T) {
	r := root(t)
	// Anything at or under this ships without comment: it is below every
	// default limit, so it cannot be silently truncated.
	const unremarkable = 7

	re := regexp.MustCompile(`retention-days:\s*(\d+)`)
	var checked int
	for _, dir := range []string{"core", "profiles"} {
		err := filepath.WalkDir(filepath.Join(r, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isText(path) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Split(string(data), "\n")
			rel, _ := filepath.Rel(r, path)
			for i, line := range lines {
				m := re.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				checked++
				n, _ := strconv.Atoi(m[1])
				if n <= unremarkable {
					continue
				}
				// Look back for a comment block mentioning the limit.
				var explained bool
				for j := i - 1; j >= 0 && j > i-12; j-- {
					t := strings.TrimSpace(lines[j])
					if !strings.HasPrefix(t, "#") {
						break
					}
					low := strings.ToLower(t)
					if strings.Contains(low, "limit") || strings.Contains(low, "retention") || strings.Contains(low, "cap") {
						explained = true
						break
					}
				}
				if !explained {
					t.Errorf("%s:%d requests %d-day retention with no note about the platform limit. "+
						"An org or repo artifact-retention limit silently truncates a larger value, so this "+
						"may promise retention that never happens. Either lower it to the real limit, or "+
						"state in a comment that the limit has been checked and raised to match.",
						rel, i+1, n)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if checked == 0 {
		t.Fatal("found no retention-days at all; the guard is not reaching the workflows")
	}
	t.Logf("checked %d retention-days value(s)", checked)
}

// TestSecretsExampleIsNonEmptyAndEscaped guards the two ways this file breaks a
// project silently.
//
// It is copied verbatim by CI to build with, and the profile's accessor
// fatalErrors at launch on a missing or blank key — so an omission here crashes
// every test run before a single test executes. One project sat in exactly that
// state: SENTRY_DSN was absent while its code required it, and the old exit-65
// handler read the resulting crash as "all tests passed". Months of green CI
// over a suite that never ran.
//
// The second trap is subtler. xcconfig treats `//` as a comment, so an
// unescaped URL truncates to `https:` — NON-empty, so the accessor does not
// fire, and the app silently reports crashes nowhere. A blank value fails loudly;
// a truncated one does not, which makes it the worse bug.
func TestSecretsExampleIsNonEmptyAndEscaped(t *testing.T) {
	r := root(t)
	path := filepath.Join(r, "profiles", "ios", "config", "Secrets.xcconfig.example")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no secrets example: %v", err)
	}
	var checked int
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || !strings.Contains(line, "=") {
			continue
		}
		key, val, _ := strings.Cut(line, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		checked++
		if val == "" {
			t.Errorf("%s:%d %s has a blank placeholder. CI copies this file verbatim, and the "+
				"accessor fatalErrors at launch on a blank value — every test would crash before running.",
				filepath.Base(path), i+1, key)
		}
		// A URL value must use the `/$()/` escape or xcconfig truncates it at
		// the `//`, leaving a non-empty value that passes every check and works
		// for nothing.
		if strings.Contains(val, "://") {
			t.Errorf("%s:%d %s contains an unescaped `://`. xcconfig treats `//` as a comment, so this "+
				"truncates to %q — non-empty, so nothing fails, and the service is silently misconfigured. "+
				"Use the `/$()/` form documented at the top of the file.",
				filepath.Base(path), i+1, key, strings.SplitN(val, "//", 2)[0])
		}
	}
	if checked == 0 {
		t.Fatal("no keys found; the guard is not reading the file")
	}
	t.Logf("checked %d placeholder key(s)", checked)
}
