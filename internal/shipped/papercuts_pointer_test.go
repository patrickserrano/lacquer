package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The optional machine-local log procedure lives in engineering-workflow.
// Assert on each delivered skill, not a reference stranded in the source tree.
var pointerPhrases = []struct{ phrase, why string }{
	{"~/Developer/papercuts.md", "the path the operator's log lives at"},
	{"date · symptom · fix · project", "the one-line entry format the log uses"},
	{"Global", "entries that apply in any repo go under Global"},
	{"only exists on", "the file is machine-local, not something the repo ships"},
	{"absence is normal", "a session without the file must not treat that as a fault"},
}

func TestRenderedSkillPointsAtThePapercutsLog(t *testing.T) {
	project := syncedProject(t, "")
	for _, dir := range []string{".claude/skills", ".codex/skills"} {
		t.Run(dir, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(project, dir, "engineering-workflow/references/project-rules.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range pointerPhrases {
				if !strings.Contains(string(raw), p.phrase) {
					t.Errorf("%s lacks %q (%s)", dir, p.phrase, p.why)
				}
			}
		})
	}
}

// The pointer must never harden into a requirement anywhere it is written. The
// section is short, so this looks at the whole of it: no MUST-style wording
// about the file. (Detection is on the shipped source, which is what syncs.)
func TestPapercutsPointerIsOptional(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "core/skills/engineering-workflow/references/project-rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "~/Developer/papercuts.md")
	if i < 0 {
		t.Fatal("engineering-workflow does not mention ~/Developer/papercuts.md")
	}
	start := strings.LastIndex(src[:i], "\n## ")
	if start < 0 {
		t.Fatal("the papercuts pointer is not under a `## ` heading")
	}
	end := len(src)
	if j := strings.Index(src[i:], "\n## "); j >= 0 {
		end = i + j
	}
	section := src[start:end]

	if n := len(strings.Split(strings.TrimSpace(section), "\n")); n > 14 {
		t.Errorf("the papercuts section is %d lines; it is meant to be a heading and a few lines", n)
	}
	for _, banned := range []string{"must exist", "is required", "must read", "must append", "fail if"} {
		if strings.Contains(strings.ToLower(section), banned) {
			t.Errorf("the papercuts section says %q — the log is optional and no check may require it", banned)
		}
	}
}
