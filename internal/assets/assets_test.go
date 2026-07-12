package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlan(t *testing.T) {
	h := t.TempDir()
	// core assets
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	write(t, filepath.Join(h, "core", "commands", "sync-worktree.md"), "x")
	// ios profile assets
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "config", ".swiftlint.yml"), "x")

	cfg := &config.Config{
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
		},
	}

	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Map dest -> src for assertion.
	dests := map[string]string{}
	for _, a := range got {
		dests[a.Dest] = a.Src
	}
	want := []string{
		filepath.Join(".claude", "skills", "git.md"),
		filepath.Join(".claude", "commands", "sync-worktree.md"),
		filepath.Join(".claude", "skills", "build.md"),
		filepath.Join(".github", "workflows", "ios-ci.yml"),
		filepath.Join("ios", ".swiftlint.yml"),
	}
	var gotDests []string
	for d := range dests {
		gotDests = append(gotDests, d)
	}
	sort.Strings(want)
	sort.Strings(gotDests)
	if len(gotDests) != len(want) {
		t.Fatalf("got %d assets %v, want %d %v", len(gotDests), gotDests, len(want), want)
	}
	for i := range want {
		if gotDests[i] != want[i] {
			t.Errorf("dest[%d] = %q, want %q", i, gotDests[i], want[i])
		}
	}
	// Source paths must be absolute and exist.
	for _, a := range got {
		if !filepath.IsAbs(a.Src) {
			t.Errorf("src not absolute: %q", a.Src)
		}
	}
}

func TestPlanFansSkillsAcrossTools(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	write(t, filepath.Join(h, "core", "commands", "sync-worktree.md"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")

	cfg := &config.Config{
		Project:    config.Project{Tools: []string{"claude", "codex", "antigravity"}},
		Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}},
	}

	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]bool{}
	for _, a := range got {
		dests[a.Dest] = true
	}

	// Each skill must be provisioned into all three tool dirs.
	for _, skill := range []string{"git.md", "build.md"} {
		for _, dir := range []string{".claude/skills", ".codex/skills", ".agents/skills"} {
			want := filepath.Join(filepath.FromSlash(dir), skill)
			if !dests[want] {
				t.Errorf("missing skill fan-out: %q", want)
			}
		}
	}
	// Commands stay Claude-only — never fanned to other tools.
	if dests[filepath.Join(".codex", "skills", "sync-worktree.md")] ||
		dests[filepath.Join(".agents", "skills", "sync-worktree.md")] {
		t.Error("commands must not fan out to non-Claude tools")
	}
	if !dests[filepath.Join(".claude", "commands", "sync-worktree.md")] {
		t.Error("command missing from .claude/commands")
	}
}

func TestPlanAgentsCategory(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "agents", "software-architect.md"), "core agent")
	write(t, filepath.Join(h, "profiles", "ios", "agents", "ios-swift-engineer.md"), "profile agent")

	cfg := &config.Config{Components: []config.Component{
		{Path: "ios", Profiles: []string{"ios"}},
	}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]string{}
	for _, a := range got {
		dests[a.Dest] = a.Src
	}

	// A core agent lands flat at .claude/agents/<name>.md.
	coreDest := filepath.Join(".claude", "agents", "software-architect.md")
	if _, ok := dests[coreDest]; !ok {
		t.Errorf("core agent missing at %q; got %v", coreDest, dests)
	}
	// A profile agent lands at the same flat destination shape, not nested
	// under the profile name.
	profileDest := filepath.Join(".claude", "agents", "ios-swift-engineer.md")
	if _, ok := dests[profileDest]; !ok {
		t.Errorf("profile agent missing at %q; got %v", profileDest, dests)
	}
}

// TestPlanAgentsStayClaudeOnly proves the key behavioral difference from
// skills: unlike SKILL.md, custom agent definitions are a Claude-Code-specific
// mechanism, so they must never fan out to other enabled tools' dirs.
func TestPlanAgentsStayClaudeOnly(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "agents", "software-architect.md"), "core agent")
	write(t, filepath.Join(h, "profiles", "ios", "agents", "ios-swift-engineer.md"), "profile agent")

	cfg := &config.Config{
		Project:    config.Project{Tools: []string{"claude", "codex", "antigravity"}},
		Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}},
	}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]bool{}
	for _, a := range got {
		dests[a.Dest] = true
	}

	if !dests[filepath.Join(".claude", "agents", "software-architect.md")] {
		t.Error("core agent missing from .claude/agents")
	}
	if !dests[filepath.Join(".claude", "agents", "ios-swift-engineer.md")] {
		t.Error("profile agent missing from .claude/agents")
	}
	for _, bad := range []string{
		filepath.Join(".codex", "agents", "software-architect.md"),
		filepath.Join(".agents", "agents", "software-architect.md"),
		filepath.Join(".codex", "agents", "ios-swift-engineer.md"),
		filepath.Join(".agents", "agents", "ios-swift-engineer.md"),
	} {
		if dests[bad] {
			t.Errorf("agents must not fan out to non-Claude tools, but found %q", bad)
		}
	}
}

// TestPlanAgentCoreWinsOverProfile proves the same "first writer wins"
// collision rule that applies to skills/commands also applies to agents: a
// core agent takes precedence over a same-named profile agent.
func TestPlanAgentCoreWinsOverProfile(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "agents", "dup.md"), "CORE VERSION")
	write(t, filepath.Join(h, "profiles", "ios", "agents", "dup.md"), "PROFILE VERSION")

	cfg := &config.Config{Components: []config.Component{
		{Path: "ios", Profiles: []string{"ios"}},
	}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var src string
	for _, a := range got {
		if a.Dest == filepath.Join(".claude", "agents", "dup.md") {
			src = a.Src
		}
	}
	if src == "" {
		t.Fatal("dup.md agent not planned")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "CORE VERSION" {
		t.Errorf("core agent should win a same-named collision, got src content %q", data)
	}
}

func TestPlanSkipsCruft(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	// Build/tool junk that may sit on the lacquer disk must never be planned.
	write(t, filepath.Join(h, "core", "skills", "__pycache__", "git.cpython-314.pyc"), "x")
	write(t, filepath.Join(h, "core", "skills", "helper.pyc"), "x")
	write(t, filepath.Join(h, "core", "skills", ".DS_Store"), "x")

	got, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, a := range got {
		if strings.Contains(a.Dest, "__pycache__") || strings.HasSuffix(a.Dest, ".pyc") ||
			strings.HasSuffix(a.Dest, ".DS_Store") {
			t.Errorf("cruft was planned: %s", a.Dest)
		}
	}
	// The real skill is still planned.
	var sawGit bool
	for _, a := range got {
		if a.Dest == filepath.Join(".claude", "skills", "git.md") {
			sawGit = true
		}
	}
	if !sawGit {
		t.Error("real skill git.md should still be planned")
	}
}

func TestPlanHonorsExclude(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")

	cfg := &config.Config{
		Project:    config.Project{Exclude: []string{".github/workflows/"}},
		Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}},
	}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, a := range got {
		if strings.HasPrefix(a.Dest, ".github/workflows/") {
			t.Errorf("excluded path was planned: %s", a.Dest)
		}
	}
	// The non-excluded skill is still planned.
	var sawSkill bool
	for _, a := range got {
		if a.Dest == filepath.Join(".claude", "skills", "build.md") {
			sawSkill = true
		}
	}
	if !sawSkill {
		t.Error("non-excluded skill should still be planned")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCopyWritesAssets(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "core", "skills", "git.md"), "SKILL BODY")

	cfg := &config.Config{}
	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan, config.Project{}); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "git.md"))
	if err != nil {
		t.Fatalf("asset not written: %v", err)
	}
	if string(got) != "SKILL BODY" {
		t.Errorf("content = %q", got)
	}
}

func TestCopyRefusesDirtyTarget(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "NEW")

	// Pre-create the destination with a local edit and commit, then dirty it.
	dest := filepath.Join(project, ".claude", "commands", "build.md")
	write(t, dest, "committed\n")
	gitInit(t, project)
	cmd := exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = project
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	write(t, dest, "LOCAL UNSAVED EDIT\n") // now dirty

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = Copy(project, plan, config.Project{})
	if err == nil {
		t.Fatal("expected Copy to refuse dirty target, got nil")
	}
	if !strings.Contains(err.Error(), "build.md") {
		t.Errorf("error should name the dirty file: %v", err)
	}
	// The local edit must be preserved.
	got, _ := os.ReadFile(dest)
	if string(got) != "LOCAL UNSAVED EDIT\n" {
		t.Errorf("dirty target was overwritten: %q", got)
	}
}

func TestCopyRefusesNonGitProject(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir() // deliberately NOT git init'd
	write(t, filepath.Join(h, "core", "skills", "git.md"), "BODY")
	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan, config.Project{}); err == nil {
		t.Fatal("expected Copy to refuse a non-git project (fail-closed), got nil")
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "git.md")); err == nil {
		t.Error("asset was written to a non-git project despite guard")
	}
}

func TestCopyRefusesSymlinkedDestDir(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "core", "skills", "git.md"), "BODY")
	// .claude is a symlink pointing outside the project root.
	if err := os.Symlink(outside, filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan, config.Project{}); err == nil {
		t.Fatal("expected Copy to refuse writing through symlinked .claude dir")
	}
	if _, err := os.Stat(filepath.Join(outside, "skills", "git.md")); err == nil {
		t.Error("asset escaped the project root via symlinked .claude")
	}
}

// TestCopyAllOrNothingOnConfinementViolation proves the preflight is all-or-
// nothing for deterministic safety checks: a confinement violation on a LATER
// asset must abort before any EARLIER asset is written.
func TestCopyAllOrNothingOnConfinementViolation(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "good.md"), "GOOD")
	// A symlinked dir escaping the project root, used by the second asset.
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	plan := []Asset{
		{Src: filepath.Join(h, "good.md"), Dest: filepath.Join(".claude", "skills", "a.md")},
		{Src: filepath.Join(h, "good.md"), Dest: filepath.Join("escape", "CLAUDE.md")},
	}
	if err := Copy(project, plan, config.Project{}); err == nil {
		t.Fatal("expected a confinement violation error, got nil")
	}
	// The earlier, safe asset must NOT have been written.
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "a.md")); err == nil {
		t.Error("earlier asset was written despite a later confinement violation (partial sync)")
	}
}

func TestPlanRootCategory(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "root", "scripts", "check-secrets.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(h, "profiles", "ios", "root", "Brewfile"), "brew 'x'\n")
	write(t, filepath.Join(h, "profiles", "ios", "root", ".claude", "scripts", "allow_mcp.js"), "//x\n")

	cfg := &config.Config{Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]bool{}
	for _, a := range got {
		dests[a.Dest] = true
	}
	for _, want := range []string{
		filepath.Join("scripts", "check-secrets.sh"),
		"Brewfile",
		filepath.Join(".claude", "scripts", "allow_mcp.js"),
	} {
		if !dests[want] {
			t.Errorf("missing root asset dest %q; got %v", want, dests)
		}
	}
}

func TestCopyPreservesExecutableBit(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	gitInit(t, project)
	exe := filepath.Join(h, "core", "root", "scripts", "hook.sh")
	write(t, exe, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(h, "core", "root", "Brewfile"), "brew 'x'\n")

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan, config.Project{}); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	fi, err := os.Stat(filepath.Join(project, "scripts", "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o100 == 0 {
		t.Errorf("script lost its executable bit: mode=%v", fi.Mode())
	}
	bf, err := os.Stat(filepath.Join(project, "Brewfile"))
	if err != nil {
		t.Fatal(err)
	}
	if bf.Mode()&0o111 != 0 {
		t.Errorf("non-exec file gained an executable bit: mode=%v", bf.Mode())
	}
}

func TestCopyRestoresExecBitOnOverwrite(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()

	exe := filepath.Join(h, "core", "root", "scripts", "hook.sh")
	write(t, exe, "#!/bin/sh\necho new\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing, committed (clean), NON-executable destination.
	dest := filepath.Join(project, "scripts", "hook.sh")
	write(t, dest, "old\n")
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, project)
	commit := exec.Command("git", "commit", "-qm", "init")
	commit.Dir = project
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan, config.Project{}); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o100 == 0 {
		t.Errorf("exec bit not restored on overwrite: mode=%v", fi.Mode())
	}
}

func TestPlanRecordsComponentPrefix(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "web", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "core", "skills", "g.md"), "x")
	cfg := &config.Config{Components: []config.Component{
		{Path: ".", Profiles: []string{"ios"}},
		{Path: "dashboard", Profiles: []string{"web"}},
	}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pre := map[string]string{}
	for _, a := range got {
		pre[a.Dest] = a.Prefix
	}
	if pre[filepath.Join(".github", "workflows", "ios-ci.yml")] != "" {
		t.Errorf("ios (root) prefix should be empty, got %q", pre[filepath.Join(".github", "workflows", "ios-ci.yml")])
	}
	if pre[filepath.Join(".github", "workflows", "web-ci.yml")] != "dashboard/" {
		t.Errorf("web prefix = %q, want dashboard/", pre[filepath.Join(".github", "workflows", "web-ci.yml")])
	}
	if pre[filepath.Join(".claude", "skills", "g.md")] != "" {
		t.Errorf("core asset prefix must be empty, got %q", pre[filepath.Join(".claude", "skills", "g.md")])
	}
}
