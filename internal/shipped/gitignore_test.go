package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitignore"
	"github.com/patrickserrano/lacquer/internal/sync"
)

// The lacquer shipped 552 assets and not one .gitignore, so every project wrote
// its own and they disagreed. Measured across five repositories before this
// existed: two ignored `*.p8`, three did not; four ignored `Secrets.xcconfig`,
// one did not; two ignored `.env`, three did not; one was protected by nothing
// at all. The ios profile has shipped Secrets.xcconfig.example the whole time,
// so the lacquer always knew the real file sat beside it.
//
// These tests assert against REAL GIT (`git check-ignore`) rather than against
// the rendered text, because the thing that matters is not whether a line reads
// `*.p8` — it is whether git actually refuses to stage AuthKey_ABC.p8. Those two
// come apart easily: a pattern containing a slash is anchored to the repo root
// (so `Config/x.xcconfig` silently misses `ios/Config/x.xcconfig`), a trailing
// `#` comment is part of the pattern rather than a comment, and a directory-only
// pattern does not match the symlinks `skills add` creates. Every one of those
// renders a plausible-looking .gitignore that protects nothing.

// manifest is a project complete enough to sync the whole ios profile, with the
// two derived cases wired up: a product redirecting its release keys away from
// the default Secrets.xcconfig, and two declared skills — one third-party, one
// whose name the lacquer ALSO ships (`handoff`, a core skill).
const manifest = `[project]
name = "demo"
project_name = "Demo"
scheme = "Demo"
bundle_id = "com.x.demo"
asc_app_id = "1"
xcodeproj = "Demo.xcodeproj"
swift_version = "6"
github_org = "acme"
tools = ["claude", "codex"]
skills = ["vercel-labs/skills@nextjs-guide", "acme/pack@handoff"]

[[component]]
path = "ios"
profiles = ["ios"]

[[product]]
name = "Paid"
scheme = "Paid"
bundle_id = "com.x.paid"
asc_app_id = "111"
tag_prefix = "paid-v"

[[product]]
name = "Free"
scheme = "Free"
bundle_id = "com.x.free"
asc_app_id = "222"
tag_prefix = "free-v"
secrets_file = "Config/Monetization.xcconfig"
secrets = { ADMOB_APPLICATION_ID = "ABV_ADMOB_APP_ID" }
`

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// syncedProject creates a git repo carrying preexisting as its .gitignore (skipped
// when empty), syncs the real lacquer into it, and returns its path.
func syncedProject(t *testing.T, preexisting string) string {
	t.Helper()
	r := root(t)
	project := t.TempDir()

	git(t, project, "init", "-q")
	// Commits happen below (idempotence needs a clean tree); a runner with no
	// global identity would otherwise fail there rather than here.
	git(t, project, "config", "user.email", "test@example.com")
	git(t, project, "config", "user.name", "Test")
	// Answer from THIS repository's .gitignore and nothing else. A developer's
	// ~/.gitignore_global is part of git's answer by default, and this author's
	// contains `.env` — so the assertions below passed on this machine with the
	// `.env` rule deleted from the shipped block, and would have shipped a hole
	// straight into every repo that has no such global file. Repo-local config
	// overrides the global excludesFile.
	git(t, project, "config", "core.excludesFile", os.DevNull)
	if err := os.WriteFile(filepath.Join(project, ".git", "info", "exclude"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(project, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if preexisting != "" {
		if err := os.WriteFile(filepath.Join(project, gitignore.Name), []byte(preexisting), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sync.Run(r, project, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return project
}

// ignoredBy reports whether git itself would ignore path in project, and which
// file the winning pattern came from. The path need not exist: check-ignore
// answers on the patterns alone, which is what a developer's `git add -A`
// consults.
//
// The SOURCE is returned, not just a yes/no, because "git ignores it" and "this
// project's .gitignore ignores it" are different claims and only the second one
// is what the lacquer ships.
func ignoredBy(t *testing.T, project, path string) (bool, string) {
	t.Helper()
	// -q, exit status only, is the authoritative yes/no. `-v` is NOT: it also
	// reports — and exits 0 for — a path matched by a NEGATIVE pattern, so
	// `!.env.example` would read as "ignored" and every re-include assertion
	// below would invert.
	q := exec.Command("git", "check-ignore", "-q", "--", path)
	q.Dir = project
	if err := q.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 {
			// Exit 128 is check-ignore failing to run at all. Reporting that as
			// "not ignored" would turn every assertion below into a false alarm,
			// and reporting it as "ignored" would make them all vacuously pass.
			t.Fatalf("git check-ignore %s: %v\n%s", path, err, ee.Stderr)
		}
		return false, ""
	}
	// Ignored — now ask which file the winning pattern lives in.
	v := exec.Command("git", "check-ignore", "-v", "--", path)
	v.Dir = project
	out, err := v.Output()
	if err != nil {
		t.Fatalf("git check-ignore -v %s: %v", path, err)
	}
	// `<source>:<line>:<pattern>\t<path>`
	source, _, _ := strings.Cut(string(out), ":")
	return true, source
}

// ignored is ignoredBy narrowed to "and the rule came from this project's
// .gitignore".
func ignored(t *testing.T, project, path string) bool {
	t.Helper()
	yes, source := ignoredBy(t, project, path)
	if yes && source != gitignore.Name {
		t.Errorf("%s is ignored by %s, not by the project's %s — that rule does not ship with the lacquer",
			path, source, gitignore.Name)
	}
	return yes
}

// TestGitignoreRegionIgnoresCredentials is the whole point: the files that grant
// real access must be unstageable in a freshly synced project, and the committed
// templates beside them must stay stageable.
func TestGitignoreRegionIgnoresCredentials(t *testing.T) {
	project := syncedProject(t, "")

	for _, path := range []string{
		// App Store Connect API private key. This is the one three of five
		// projects were missing, and it grants build-upload and app-data
		// access for every app on the account.
		"AuthKey_ABC123.p8",
		"ios/AuthKey_ABC123.p8",
		"ios/Fastlane/keys/AuthKey_ABC123.p8",
		// Signing material.
		"certs/distribution.p12",
		"ios/Demo_AppStore.mobileprovision",
		// Real service keys. One project keeps a Secrets.xcconfig at two
		// depths, which is why the rule is unanchored.
		"Secrets.xcconfig",
		"ios/Secrets.xcconfig",
		"ios/Demo/Demo/Secrets.xcconfig",
		// Environment files, including the dotted variants.
		".env",
		".env.local",
		".env.production",
		"ios/.env",
		// [[product]].secrets_file, at the root (where the release workflow
		// writes it) and under a component (where a developer reproducing a
		// release locally would).
		"Config/Monetization.xcconfig",
		"ios/Config/Monetization.xcconfig",
	} {
		if !ignored(t, project, path) {
			t.Errorf("%s is NOT ignored — a `git add -A` would stage a live credential", path)
		}
	}

	// The committed templates. Ignoring these breaks the profile outright: CI
	// copies Secrets.xcconfig.example verbatim to build, and the web profile's
	// env-validation workflow reads .env.example and .env.schema.
	for _, path := range []string{
		"Secrets.xcconfig.example",
		"ios/Secrets.xcconfig.example",
		".env.example",
		".env.schema",
	} {
		if ignored(t, project, path) {
			t.Errorf("%s IS ignored — it is a committed template the profile and CI depend on", path)
		}
	}
}

// TestGitignoreRegionKeepsProjectOwnedEntries is why this is a region and not a
// whole-file asset. A .gitignore is genuinely co-owned: DerivedData paths and
// build outputs are the project's business. Replacing the file would delete them
// and show up as permanent conflict in every audit.
func TestGitignoreRegionKeepsProjectOwnedEntries(t *testing.T) {
	preexisting := "# things only this project has\nDerivedData/\n*.xcuserstate\n!keep-me.xcuserstate\n"
	project := syncedProject(t, preexisting)

	got, err := os.ReadFile(filepath.Join(project, gitignore.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), preexisting) {
		t.Errorf("the project's own .gitignore text was not preserved verbatim at the top:\n%s", got)
	}

	// Present in the text is not the same as still in force: a managed block
	// appended in the wrong place, or a `!` line of ours landing after theirs,
	// would leave the text intact and the behaviour changed.
	for _, path := range []string{"DerivedData/x", "Foo.xcuserstate"} {
		if !ignored(t, project, path) {
			t.Errorf("the project's own rule for %s stopped working after sync", path)
		}
	}
	if ignored(t, project, "keep-me.xcuserstate") {
		t.Error("the project's own negation was overridden by the managed block")
	}
}

// TestGitignoreSkillRulesFollowTheManifest covers the second half of the fleet
// finding: third-party skill artifacts had no convention either. One project
// ignores .agents/skills/ WHOLESALE, which also swallows every skill the lacquer
// syncs — and a lacquer-managed file that git cannot see is a file `lacquer
// audit` goes blind to, so drift in it stops being detectable.
//
// The rules therefore name the third-party skills one by one, out of
// [project].skills.
func TestGitignoreSkillRulesFollowTheManifest(t *testing.T) {
	project := syncedProject(t, "")

	// `skills add` writes the canonical .agents/skills tree and a Claude Code
	// symlink whatever the manifest declares; .codex/skills is bridged because
	// the manifest declares codex.
	for _, dir := range []string{".agents/skills", ".claude/skills", ".codex/skills"} {
		if !ignored(t, project, dir+"/nextjs-guide") {
			t.Errorf("%s/nextjs-guide is not ignored — a declared third-party skill's vendored tree would land in every diff", dir)
		}
		if !ignored(t, project, dir+"/nextjs-guide/SKILL.md") {
			t.Errorf("%s/nextjs-guide/SKILL.md is not ignored — the rule matches the directory but not its contents", dir)
		}
	}

	// A skill the manifest does NOT declare is not ignored, or the rule is
	// really just "ignore the directory" wearing a different spelling.
	if ignored(t, project, ".agents/skills/some-other-skill") {
		t.Error(".agents/skills/some-other-skill is ignored — the rules are not actually derived from [project].skills")
	}

	// Every skill the lacquer itself syncs must stay tracked, in every tool dir
	// it was synced into. This is the windsock failure the design exists to
	// avoid, and it is asserted against the REAL asset plan rather than a
	// hand-picked name, so a skill added to the lacquer is covered automatically.
	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := assets.Plan(root(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	for _, a := range plan {
		dest := filepath.ToSlash(a.Dest)
		if !strings.Contains(dest, "/skills/") {
			continue
		}
		checked++
		if ignored(t, project, dest) {
			t.Errorf("%s is a lacquer-MANAGED skill file and it is ignored — audit goes blind to it", dest)
		}
	}
	if checked == 0 {
		t.Fatal("the lacquer synced no skill files, so this test asserted nothing")
	}

	// `handoff` is declared in [project].skills AND shipped by the lacquer. The
	// synced copy wins and stays tracked; the block says so rather than dropping
	// the entry silently.
	body, err := os.ReadFile(filepath.Join(project, gitignore.Name))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/.agents/skills/handoff\n") {
		t.Error("a skill name the lacquer also ships got an ignore rule, untracking the managed copy")
	}
	if !strings.Contains(string(body), `"handoff" is declared here AND shipped by the lacquer`) {
		t.Errorf("the collision is not explained in the block, so it reads as a renderer bug:\n%s", body)
	}

	// A lockfile is tracked. skills-lock.json pins the resolved source of every
	// installed skill: committing it is what makes an install reproducible and
	// what lets a reviewer see a skill's source change. One project currently
	// leaves it untracked-but-unignored, which is the worst of both.
	if ignored(t, project, "skills-lock.json") {
		t.Error("skills-lock.json is ignored — a lockfile that is not committed pins nothing")
	}
}

// TestGitignoreBodyIsDeterministic is the precondition TestSyncingTwiceChangesNothing
// depends on, asserted directly because that test only catches a violation
// PROBABILISTICALLY.
//
// The body is built from maps (the tool-directory set, the managed-skill set),
// and Go randomizes map iteration. One `lacquer sync` renders this body three
// separate times — once in the clobber guard's audit, once for the write, once
// for the lock — so an unsorted render does not merely churn between runs, it
// writes a lock that disagrees with the file it just wrote, in a single run.
// With only three tool directories an unsorted render still happens to come out
// in the same order about a third of the time, so a single before/after
// comparison lets it through. Fifty renders do not.
func TestGitignoreBodyIsDeterministic(t *testing.T) {
	r := root(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := assets.Plan(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := gitignore.Body(cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		got, err := gitignore.Body(cfg, plan)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("render %d differs from the first — the body is not deterministic, so one sync writes a lock that disagrees with the file it wrote:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
}

// TestSyncingTwiceChangesNothing. A managed region that rewrites itself on every
// run is indistinguishable from real drift: `lacquer audit` reports it, CI's
// drift gate wakes for it, and the fleet learns to ignore both. The .gitignore
// body is rendered from maps (products, skills, tool dirs), and an unsorted
// render is exactly how that happens.
func TestSyncingTwiceChangesNothing(t *testing.T) {
	project := syncedProject(t, "# project-owned\nDerivedData/\n")

	// The second sync's clobber guard refuses to overwrite uncommitted work, so
	// commit what the first one wrote — which is what a real project does.
	git(t, project, "add", "-A")
	git(t, project, "commit", "-qm", "sync")

	if _, err := sync.Run(root(t), project, false); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	if out := git(t, project, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("a second sync changed the tree — every one of these shows as drift forever:\n%s", out)
	}
}
