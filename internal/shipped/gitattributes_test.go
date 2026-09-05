package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitattributes"
	"github.com/patrickserrano/lacquer/internal/sync"
)

// The lacquer ships 20 Python files, 207KB, into every project taking the ios
// profile — xcode-build-orchestrator/scripts/*.py, xcode-compilation-analyzer,
// swiftui-expert-skill/scripts/instruments_parser/*.py — and it ships them once
// per enabled tool directory. GitHub Linguist counted every byte toward the
// language bar. Measured before this region existed: ShelfLife read as MORE
// Python than Swift, and rail, flare, Skein, momfriend and dailybread each
// carried 570-650KB of the identical "Python" none of them wrote.
//
// These tests ask REAL GIT (`git check-attr`) rather than reading the rendered
// text, for the same reason the .gitignore tests ask `git check-ignore`: a line
// that reads `.claude/skills linguist-vendored` looks completely correct and
// marks nothing, because attributes match FILE paths and that pattern only
// matches the directory entry itself. A plausible .gitattributes that vendors
// nothing is the failure this is guarding, and only git can tell the difference.

// attr returns the value git resolves for attribute name on path in project, or
// "unspecified" when no pattern claims it.
//
// The VALUE, not a boolean: `linguist-vendored=true` and `-linguist-vendored`
// (which means the opposite, and sits one character away) both count as "an
// attribute is set here" to a naive check, and only one of them does what this
// region exists to do.
func attr(t *testing.T, project, name, path string) string {
	t.Helper()
	cmd := exec.Command("git", "check-attr", name, "--", path)
	cmd.Dir = project
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git check-attr %s %s: %v", name, path, err)
	}
	// `<path>: <attr>: <value>` — the path can itself contain ": ", so cut from
	// the right on the attribute name rather than splitting on every colon.
	line := strings.TrimSpace(string(out))
	i := strings.LastIndex(line, ": ")
	if i < 0 {
		t.Fatalf("unparseable check-attr output for %s: %q", path, line)
	}
	return line[i+2:]
}

// TestGitattributesRegionVendorsEverySkillDir is the whole point: after a sync,
// git itself must report the skill trees as vendored, in every tool directory
// this project actually receives skills in.
func TestGitattributesRegionVendorsEverySkillDir(t *testing.T) {
	project := syncedProject(t, "")

	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dirs := assets.SkillDirs(cfg)
	if len(dirs) == 0 {
		t.Fatal("this project receives skills in no directory at all, so this test asserts nothing")
	}
	for _, dir := range dirs {
		// A file DEEP inside the tree, not the directory entry: Linguist counts
		// bytes in files, and `dir linguist-vendored` (no `/**`) marks the
		// directory and leaves every file under it counted.
		for _, path := range []string{
			dir + "/xcode-build-orchestrator/scripts/benchmark_builds.py",
			dir + "/swiftui-expert-skill/scripts/instruments_parser/xctrace.py",
			dir + "/handoff/SKILL.md",
		} {
			if got := attr(t, project, "linguist-vendored", path); got != "true" {
				t.Errorf("%s: linguist-vendored = %q, want %q — Linguist is still counting these bytes as project source", path, got, "true")
			}
		}
	}

	// The project's OWN source must not be swept up. A pattern broadened to
	// `**` or to the repo root would make the language bar empty rather than
	// accurate, and that failure looks like success from inside this test file.
	for _, path := range []string{
		"ios/Demo/ContentView.swift",
		"scripts/check-secrets.sh",
		"README.md",
	} {
		if got := attr(t, project, "linguist-vendored", path); got == "true" {
			t.Errorf("%s is marked linguist-vendored — the patterns are too broad and the language bar now under-reports the project's own code", path)
		}
	}
}

// TestGitattributesRegionCoversTheRealAssetPlan asserts the rendered patterns
// cover the skill files the lacquer ACTUALLY syncs into this project, read off
// the real asset plan rather than a hand-picked list.
//
// The hand-picked paths above prove the pattern shape works. This proves the
// pattern SET is complete: a skill synced into a directory the region does not
// name is exactly the bug the fleet had, and asserting against the plan means a
// tool directory added later is covered without anyone remembering to come here.
func TestGitattributesRegionCoversTheRealAssetPlan(t *testing.T) {
	project := syncedProject(t, "")

	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := assets.Plan(root(t), cfg)
	if err != nil {
		t.Fatal(err)
	}

	var checked, python int
	for _, a := range plan {
		dest := filepath.ToSlash(a.Dest)
		if !strings.Contains(dest, "/skills/") {
			continue
		}
		checked++
		if strings.HasSuffix(dest, ".py") {
			python++
		}
		if got := attr(t, project, "linguist-vendored", dest); got != "true" {
			t.Errorf("%s is a lacquer-synced skill file and linguist-vendored = %q — its bytes still count toward the language bar", dest, got)
		}
	}
	if checked == 0 {
		t.Fatal("the lacquer synced no skill files, so this test asserted nothing")
	}
	// The Python is the reason this region exists. If a refactor ever stops
	// shipping it, this assertion is the thing that says so out loud rather than
	// letting the test keep passing over an empty set.
	if python == 0 {
		t.Error("no .py files in the synced skill trees — this region was built for exactly those bytes, so either they moved or this fixture stopped covering them")
	}
}

// TestGitattributesRegionKeepsProjectOwnedContent is why this is a region and not
// a whole-file asset, and it is the most important test in this file.
//
// Three fleet repositories already keep real content in .gitattributes and every
// one of them would have been destroyed by a whole-file asset:
//
//   - dailybread: twelve Git LFS filter lines. Clobbering those breaks LFS
//     outright — binaries start committing as raw bytes with no pointer.
//   - kit: line-ending and binary rules plus its OWN linguist overrides
//     (Pods/** vendored, *.xcodeproj/** generated, docs/** documentation).
//   - flare: line-ending normalization and `*.swift text diff=swift`.
//
// The fixture below is those three files' content merged, and the assertions ask
// git whether each rule is still IN FORCE — not whether its text survived.
// Present-in-the-file and still-working come apart easily: a managed block
// landing above a project's own pattern loses, because for .gitattributes the
// LAST matching pattern wins.
func TestGitattributesRegionKeepsProjectOwnedContent(t *testing.T) {
	preexisting := `# Git LFS (dailybread)
*.png filter=lfs diff=lfs merge=lfs -text
*.jpg filter=lfs diff=lfs merge=lfs -text

# Line endings and binaries (flare, kit)
* text=auto
*.swift text diff=swift linguist-language=Swift
*.pbxproj binary

# This project's own linguist overrides (kit)
Pods/** linguist-vendored
*.xcodeproj/** linguist-generated
docs/** linguist-documentation
`
	project := syncedProjectWith(t, map[string]string{gitattributes.Name: preexisting})

	got, err := os.ReadFile(filepath.Join(project, gitattributes.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), preexisting) {
		t.Errorf("the project's own .gitattributes text was not preserved verbatim at the top — a real repo would have lost its LFS config:\n%s", got)
	}

	// Still in force, asked of git. The LFS one is the destructive case: a
	// binary that stops matching its filter is committed as raw bytes.
	for _, tc := range []struct{ path, name, want string }{
		{"Assets/icon.png", "filter", "lfs"},
		{"Assets/icon.png", "diff", "lfs"},
		{"Assets/photo.jpg", "merge", "lfs"},
		{"ios/Demo/ContentView.swift", "diff", "swift"},
		{"ios/Demo/ContentView.swift", "linguist-language", "Swift"},
		{"ios/Demo.xcodeproj/project.pbxproj", "binary", "set"},
		// kit's own linguist rules, including one spelled in the bare boolean
		// form. Ours uses `=true`; both must keep working side by side.
		{"Pods/Alamofire/Source.swift", "linguist-vendored", "set"},
		// Root-level, deliberately. A .gitattributes pattern containing a slash
		// is anchored to the file's own directory and `*` does not cross one, so
		// kit's `*.xcodeproj/**` matches Demo.xcodeproj/… and NOT
		// ios/Demo.xcodeproj/… . That is a property of the project's own rule
		// rather than anything the lacquer does — and it is the same anchoring
		// the managed patterns rely on, which is why it is pinned here.
		{"Demo.xcodeproj/project.pbxproj", "linguist-generated", "set"},
		{"docs/guide.md", "linguist-documentation", "set"},
	} {
		if got := attr(t, project, tc.name, tc.path); got != tc.want {
			t.Errorf("%s: %s = %q, want %q — the project's own rule stopped working after sync", tc.path, tc.name, got, tc.want)
		}
	}

	// And the managed rules work in the same file, so this is a merge rather
	// than a preservation that quietly dropped the lacquer's half.
	if got := attr(t, project, "linguist-vendored", ".claude/skills/handoff/SKILL.md"); got != "true" {
		t.Errorf(".claude/skills/handoff/SKILL.md: linguist-vendored = %q, want \"true\" — the managed block merged in but does not apply", got)
	}
}

// TestGitattributesBodyIsDeterministic is the precondition the idempotence test
// below depends on, asserted directly because that test only catches a violation
// PROBABILISTICALLY.
//
// The body is built from a map (the tool-directory set) and Go randomizes map
// iteration. One `lacquer sync` renders this body three separate times — the
// clobber guard's audit, the write, and the lock — so an unsorted render does
// not merely churn between runs, it writes a lock that disagrees with the file
// it just wrote, in a single run. With only three directories an unsorted render
// still comes out in the same order about a third of the time, so a single
// before/after comparison lets it through. Fifty renders do not.
func TestGitattributesBodyIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	first := gitattributes.Body(cfg)
	for i := range 50 {
		if got := gitattributes.Body(cfg); got != first {
			t.Fatalf("render %d differs from the first — the body is not deterministic, so one sync writes a lock that disagrees with the file it wrote:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
}

// TestSyncingTwiceLeavesTheGitattributesRegionAlone. A managed region that
// rewrites itself on every run is indistinguishable from real drift: `lacquer
// audit` reports it, CI's drift gate wakes for it, and the fleet learns to
// ignore both.
func TestSyncingTwiceLeavesTheGitattributesRegionAlone(t *testing.T) {
	project := syncedProjectWith(t, map[string]string{
		gitattributes.Name: "*.png filter=lfs diff=lfs merge=lfs -text\n",
	})

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
