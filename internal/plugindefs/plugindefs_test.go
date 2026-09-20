package plugindefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every row is a definition Claude Code skips silently, or one it loads. The
// "loads" rows matter as much as the "skips" ones: the first version of this
// check parsed the frontmatter as strict YAML and would have reported six
// shipped agents as broken (an unquoted `including: designing` in a description)
// that Claude Code loads without complaint. A detector that cries wolf gets
// switched off, so the negative rows are the guard against that.
func TestCheckFile(t *testing.T) {
	cases := []struct {
		name    string
		kind    Kind
		body    string
		problem string // substring of the one expected problem; "" = clean
		line    int
	}{
		{"agent ok", Agent, "---\nname: ok\ndescription: d\n---\nbody\n", "", 0},
		{"agent ok crlf", Agent, "---\r\nname: ok\r\ndescription: d\r\n---\r\nbody\r\n", "", 0},
		{"agent ok quoted name", Agent, "---\nname: \"ok\"\n---\n", "", 0},
		// The real shipped shape: a colon-space inside an unquoted description is
		// invalid strict YAML and loads fine.
		{"agent ok loose yaml description", Agent, "---\nname: ok\ndescription: Use it when: a, b including: c\n---\n", "", 0},
		{"agent ok memory field", Agent, "---\nname: ok\ndescription: d\nmemory: user\n---\n", "", 0},

		{"agent no frontmatter", Agent, "You are an agent.\n", "no frontmatter", 1},
		{"agent blank first line", Agent, "\n---\nname: x\ndescription: d\n---\nbody\n", "line 2, not line 1", 2},
		{"agent unterminated", Agent, "---\nname: x\ndescription: d\nbody\n", "never closed", 1},
		{"agent no name", Agent, "---\ndescription: d\n---\nbody\n", "no `name`", 1},
		{"agent empty name", Agent, "---\nname:\ndescription: d\n---\n", "no `name`", 2},
		{"agent colon name", Agent, "---\nname: bad:name\ndescription: d\n---\n", "contains ':'", 2},
		{"agent quoted colon name", Agent, "---\nname: \"bad:name\"\n---\n", "contains ':'", 2},
		// name: only counts at the top level of the frontmatter.
		{"agent nested name is not a name", Agent, "---\ndescription: d\nmeta:\n  name: nested\n---\n", "no `name`", 1},

		// Skills and commands: `name` is optional (the directory / file name is
		// the fallback) and frontmatter may be absent, so neither is a finding.
		{"skill no name ok", Skill, "---\ndescription: d\n---\nbody\n", "", 0},
		{"skill no frontmatter ok", Skill, "just a body\n", "", 0},
		{"command no frontmatter ok", Command, "Do the thing.\n", "", 0},
		{"command horizontal rule in body ok", Command, "Intro\n\n---\n\nMore\n", "", 0},
		// Structural damage is a finding for every kind.
		{"skill unterminated", Skill, "---\nname: s\ndescription: d\nbody\n", "never closed", 1},
		{"command shifted frontmatter", Command, "\n---\ndescription: d\n---\nbody\n", "line 2, not line 1", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, filepath.Join(t.TempDir(), "x.md"), c.body)
			got := CheckFile(p, c.kind)
			if c.problem == "" {
				if len(got) != 0 {
					t.Fatalf("want clean, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want exactly one finding containing %q, got %+v", c.problem, got)
			}
			if !strings.Contains(got[0].Problem, c.problem) {
				t.Errorf("problem = %q, want it to contain %q", got[0].Problem, c.problem)
			}
			if got[0].Line != c.line {
				t.Errorf("line = %d, want %d", got[0].Line, c.line)
			}
		})
	}
}

func TestFilesFindsEachKindAndOnlyDefinitions(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\n---\n")
	write(t, filepath.Join(root, "agents", "README.txt"), "not a definition")
	write(t, filepath.Join(root, "commands", "sub", "c.md"), "body")
	write(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\n---\n")
	// A reference file inside a skill is not itself a definition.
	write(t, filepath.Join(root, "skills", "s1", "references", "notes.md"), "no frontmatter")
	// A directory under skills/ without a SKILL.md is not a skill.
	write(t, filepath.Join(root, "skills", "empty", "other.md"), "x")

	var got []string
	for _, f := range Files(root) {
		rel, _ := filepath.Rel(root, f.Path)
		got = append(got, string(f.Kind)+":"+filepath.ToSlash(rel))
	}
	want := []string{"agent:agents/a.md", "command:commands/sub/c.md", "skill:skills/s1/SKILL.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Files = %v, want %v", got, want)
	}
}

func TestBuildWrapperMakesAPluginTheCLIWillActuallyRead(t *testing.T) {
	root := t.TempDir()
	a := write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\n---\n")
	s := write(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\n---\n")
	c := write(t, filepath.Join(root, "commands", "c.md"), "body")
	// A symlinked definition (rendered trees do this) must be copied by content:
	// the CLI does not follow symlinks and would report nothing.
	if err := os.Symlink(a, filepath.Join(root, "agents", "link.md")); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	m, err := BuildWrapper(root, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 4 {
		t.Fatalf("copied %d definitions, want 4: %v", len(m), m)
	}
	manifest, err := os.ReadFile(filepath.Join(dst, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The manifest's own "no author" warning would otherwise fail every --strict run.
	if !strings.Contains(string(manifest), `"author"`) {
		t.Errorf("manifest has no author; --strict would fail on it: %s", manifest)
	}
	for _, rel := range []string{"agents/a.md", "agents/link.md", "skills/s1/SKILL.md", "commands/c.md"} {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("wrapper missing %s: %v", rel, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s is a symlink; the CLI would not read it", rel)
		}
	}
	if m[filepath.Join(dst, "skills", "s1", "SKILL.md")] != s || m[filepath.Join(dst, "commands", "c.md")] != c {
		t.Errorf("wrapper->original map is wrong: %v", m)
	}
}

func TestBuildWrapperRefusesToBuildAnEmptyPlugin(t *testing.T) {
	// The CLI reports "Validation passed" for a plugin with nothing in it, so an
	// empty wrapper would be a green check over nothing.
	if _, err := BuildWrapper(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("BuildWrapper on a root with no definitions must error, not produce an empty plugin")
	}
}
