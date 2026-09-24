package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/plugindefs/plugindefstest"
)

func putDef(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goodAgent = "---\nname: good\ndescription: d\n---\nbody\n"

func TestPluginDefinitionsCleanSetWithCLI(t *testing.T) {
	proj := t.TempDir()
	putDef(t, proj, ".claude/agents/good.md", goodAgent)
	putDef(t, proj, ".claude/skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\n")
	putDef(t, proj, ".claude/commands/c.md", "Do it.\n")
	putDef(t, proj, ".agents/skills/s1/SKILL.md", "---\nname: s1\ndescription: d\n---\n")

	r := PluginDefinitionsWith(proj, plugindefstest.FakeClaude(t))
	out := FormatPluginDefs(r)
	if len(r.Findings) != 0 || len(r.CLIIssues) != 0 || len(r.CLIErrors) != 0 {
		t.Fatalf("valid set reported problems: %+v", r)
	}
	if r.Checked != 4 {
		t.Errorf("checked %d, want 4 (.claude agent+skill+command, .agents skill)", r.Checked)
	}
	for _, want := range []string{"(4 in .claude, .agents)", "lacquer check: ok", "claude plugin validate --strict (claude 9.9.9): ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPluginDefinitionsReportsBothLayersForAMalformedSet(t *testing.T) {
	proj := t.TempDir()
	putDef(t, proj, ".claude/agents/good.md", goodAgent)
	// Broken in the way only the lacquer check sees.
	putDef(t, proj, ".claude/agents/colon.md", "---\nname: bad:name\ndescription: d\n---\n")
	putDef(t, proj, ".claude/agents/noname.md", "---\ndescription: d\n---\n")
	// Broken in the way the (fake) CLI reports.
	putDef(t, proj, ".codex/agents/cli.md", "---\nname: cli\ndescription: d\n---\nBROKEN-BY-CLI\n")

	r := PluginDefinitionsWith(proj, plugindefstest.FakeClaude(t))
	out := FormatPluginDefs(r)

	// Paths are repo-relative, with the line the problem is on.
	for _, want := range []string{
		".claude/agents/colon.md:2  agent `name` \"bad:name\" contains ':'",
		".claude/agents/noname.md:1  agent has no `name`",
		"lacquer check: 2 problem(s)",
		".codex/agents/cli.md  frontmatter: No frontmatter block found.",
		"claude plugin validate --strict (claude 9.9.9): 1 problem(s)",
		// Both outputs say what each layer does and does not prove.
		"it does NOT catch a missing `name` or a `name` with ':'",
		"the exit code does not change",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "good.md") {
		t.Errorf("the valid agent was reported:\n%s", out)
	}
	if strings.Contains(out, proj) {
		t.Errorf("output leaks an absolute path:\n%s", out)
	}
}

func TestPluginDefinitionsWithoutTheCLISaysNotChecked(t *testing.T) {
	proj := t.TempDir()
	putDef(t, proj, ".claude/agents/good.md", goodAgent)
	putDef(t, proj, ".claude/agents/colon.md", "---\nname: a:b\n---\n")

	// PATH with no claude on it: the real entry point, not the injected one.
	t.Setenv("PATH", t.TempDir())
	r := PluginDefinitions(proj)
	out := FormatPluginDefs(r)
	if r.CLIFound {
		t.Fatal("CLI reported found on an empty PATH")
	}
	if !strings.Contains(out, "claude plugin validate: not checked (claude CLI not found)") {
		t.Errorf("missing the not-checked line:\n%s", out)
	}
	// The layer that needs no binary still runs, and its finding is still shown.
	if !strings.Contains(out, ".claude/agents/colon.md:2") {
		t.Errorf("the lacquer check did not run without the CLI:\n%s", out)
	}
	if strings.Contains(out, "plugin validate --strict (claude") {
		t.Errorf("claimed a CLI result with no CLI:\n%s", out)
	}
}

func TestPluginDefinitionsCLIFailureIsNotAPass(t *testing.T) {
	proj := t.TempDir()
	putDef(t, proj, ".claude/agents/good.md", goodAgent)
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := FormatPluginDefs(PluginDefinitionsWith(proj, bin))
	if !strings.Contains(out, "NOT checked, the run failed") || strings.Contains(out, "--strict (claude") {
		t.Errorf("a CLI that could not run read as a pass or was omitted:\n%s", out)
	}
}

func TestPluginDefinitionsNothingRenderedPrintsNothing(t *testing.T) {
	if out := FormatPluginDefs(PluginDefinitionsWith(t.TempDir(), "")); out != "" {
		t.Errorf("a project with no definitions printed %q", out)
	}
}
