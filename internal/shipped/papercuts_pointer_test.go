package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/region"
)

// The operator keeps ~/Developer/papercuts.md, a machine-wide log of what cost
// sessions time. CI and cloud sessions never see it — they see only what the
// repo ships — so the rendered core region carries a pointer to it.
//
// The pointer is OPTIONAL by design. The file exists on one machine; its absence
// is the normal case everywhere else (CI, cloud, a fresh clone). What is pinned
// here is therefore two things that pull against each other: the pointer is
// rendered into every project's CLAUDE.md and its AGENTS.md mirror, and the text
// says outright that nothing depends on the file. A pointer that reads as a
// requirement would send a cloud session hunting for a path that cannot exist.

// pointerPhrases are the load-bearing claims of the section. Each is asserted on
// the RENDERED region rather than on core/CLAUDE.core.md, because the region in a
// project's CLAUDE.md is what an agent reads.
var pointerPhrases = []struct{ phrase, why string }{
	{"~/Developer/papercuts.md", "the path the operator's log lives at"},
	{"date · symptom · fix · project", "the one-line entry format the log uses"},
	{"Global", "entries that apply in any repo go under Global"},
	{"only exists on", "the file is machine-local, not something the repo ships"},
	{"absence is normal", "a session without the file must not treat that as a fault"},
}

func TestRenderedCoreRegionPointsAtThePapercutsLog(t *testing.T) {
	project := syncedProject(t, "")

	// The manifest enables codex, so the AGENTS.md mirror applies. A claude-only
	// project has no AGENTS.md and is covered by the sync tests for the mirror.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(project, name))
			if err != nil {
				t.Fatal(err)
			}
			body, ok := region.ExtractBody(string(raw), "core")
			if !ok {
				t.Fatalf("%s has no core region — this test would assert nothing", name)
			}
			for _, p := range pointerPhrases {
				if !strings.Contains(body, p.phrase) {
					t.Errorf("%s core region lacks %q (%s)", name, p.phrase, p.why)
				}
			}
		})
	}
}

// The pointer must never harden into a requirement anywhere it is written. The
// section is short, so this looks at the whole of it: no MUST-style wording
// about the file. (Detection is on the shipped source, which is what syncs.)
func TestPapercutsPointerIsOptional(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "core", "CLAUDE.core.md"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "~/Developer/papercuts.md")
	if i < 0 {
		t.Fatal("core/CLAUDE.core.md does not mention ~/Developer/papercuts.md")
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
