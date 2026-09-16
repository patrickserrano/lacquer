package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// profiles/ios/CLAUDE.ios.md's "Verifying UI in the Simulator" section says to
// read the accessibility tree as text (`rocketsim elements --agent-mode
// nav|act`, or `flowdeck ui simulator screen --tree --json` where RocketSim
// isn't available) and act by element id, and calls that "a much better
// default than a screenshot-and-read loop, which costs orders of magnitude
// more for a less precise answer." Nothing enforced that other shipped iOS
// skills actually followed it: `ios-debugger-agent/SKILL.md` documented only a
// screenshot-session-then-Read-the-image loop for driving the simulator UI,
// with no tree-read step anywhere in the file, and `app-store-screenshots`
// read the tree without ever naming the flag that guarantees no image comes
// back with it. Both were fixed in the same change that added this test; this
// is what stops them drifting apart again.
//
// THE DISCRIMINATOR IS COMMAND TEXT, NOT PROSE. A detector keyed on human
// wording ("verify", "check the screen") is exactly the failure class
// documented in this repo's own CLAUDE.md and in lacquer#333 — it reads as
// working right up until someone phrases the same instruction differently. So
// this matches concrete command fingerprints: a screenshot-to-file signature
// (`ui simulator screen` with `--output`/`--screenshot`, or `simulator
// frames`) and a text-tree-read signature (`--agent-mode`, or `--tree`). A
// skill that ships the first without the second, anywhere in its own files, is
// presumed to be teaching the screenshot-and-read loop the source rule warns
// against.
//
// This deliberately does NOT try to tell "screenshot for a genuinely visual
// question" apart from "screenshot to check what's on screen" within a single
// skill — that distinction lives in prose, which this test does not trust.
// Instead: a skill that also teaches the tree read (ios-debugger-agent, which
// needs screenshots too for animation/layout review) passes on co-occurrence,
// and a skill whose job IS producing an image — where requiring a co-located
// tree-read teaching would be nonsensical — is named on the allow list
// instead. `app-store-screenshots` is the one that exists in the fleet today;
// its native-resolution `simulator frames --images` capture is the deliverable,
// not a stand-in for reading UI state.

// screenshotToFileRE matches commands that write a simulator frame to disk.
var screenshotToFileRE = regexp.MustCompile(`ui simulator screen[^\n]*(--output|--screenshot)|simulator frames\b`)

// treeReadRE matches the text-first alternative: reading the accessibility
// tree as structured text instead of capturing pixels.
var treeReadRE = regexp.MustCompile(`--agent-mode\b|--tree\b`)

// screenshotOnlyAllowList names shipped iOS skills whose screenshot/frame
// capture commands ARE the deliverable, so the skill is not required to also
// teach a text-first tree read anywhere in its own files.
//
// Keep this list named, not just non-empty: TestShippedIOSSkillsTeachTextFirstUIVerification
// refuses to run at all if it is empty (a cleared allow list would otherwise
// silently start requiring every image-producing skill to also read a tree,
// which is not what this test is for).
var screenshotOnlyAllowList = map[string]bool{
	"app-store-screenshots": true,
}

// skillScreenshotViolation is one shipped skill directory that references a
// screenshot-to-file command without also referencing a text-based tree read
// anywhere in its own markdown files, and is not on the allow list.
type skillScreenshotViolation struct {
	Skill string
	File  string // relative to skillsDir
}

// skillScreenshotViolations scans every immediate subdirectory of skillsDir —
// each one a shipped skill — and returns the ones described above, plus the
// number of skill directories it actually looked at. Callers must treat a
// zero count as a failure in its own right: it means the scan found nothing to
// check, not that everything passed.
func skillScreenshotViolations(skillsDir string, allowList map[string]bool) (violations []skillScreenshotViolation, scanned int, err error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, 0, err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		scanned++
		name := e.Name()
		if allowList[name] {
			continue
		}

		dir := filepath.Join(skillsDir, name)
		hasScreenshot := false
		hasTreeRead := false
		var offending string

		walkErr := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			body := string(b)
			if screenshotToFileRE.MatchString(body) {
				if offending == "" {
					offending = p
				}
				hasScreenshot = true
			}
			if treeReadRE.MatchString(body) {
				hasTreeRead = true
			}
			return nil
		})
		if walkErr != nil {
			return nil, scanned, walkErr
		}

		if hasScreenshot && !hasTreeRead {
			rel, relErr := filepath.Rel(skillsDir, offending)
			if relErr != nil {
				rel = offending
			}
			violations = append(violations, skillScreenshotViolation{Skill: name, File: rel})
		}
	}

	sort.Slice(violations, func(i, j int) bool { return violations[i].Skill < violations[j].Skill })
	return violations, scanned, nil
}

// TestShippedIOSSkillsTeachTextFirstUIVerification is the guard: no shipped
// iOS skill may instruct a screenshot-to-file loop for reading or driving the
// simulator UI without also teaching the text-first tree read, unless it is
// named on screenshotOnlyAllowList because the image itself is its deliverable.
func TestShippedIOSSkillsTeachTextFirstUIVerification(t *testing.T) {
	if len(screenshotOnlyAllowList) == 0 {
		t.Fatal("screenshotOnlyAllowList is empty — refusing to run rather than silently require " +
			"every image-producing skill (e.g. app-store-screenshots) to also teach a text tree read, " +
			"which is not what this test checks for")
	}

	r := root(t)
	skillsDir := filepath.Join(r, "profiles", "ios", "skills")

	violations, scanned, err := skillScreenshotViolations(skillsDir, screenshotOnlyAllowList)
	if err != nil {
		t.Fatalf("scanning %s: %v", skillsDir, err)
	}
	if scanned == 0 {
		t.Fatalf("scanned zero skill directories under %s — this check would pass by finding nothing, "+
			"not by everything passing", skillsDir)
	}

	for _, v := range violations {
		t.Errorf("profiles/ios/skills/%s references a screenshot-to-file command (`ui simulator screen` "+
			"with --output/--screenshot, or `simulator frames`) in %s without teaching the text-first "+
			"accessibility-tree read (`rocketsim elements --agent-mode ...` or `flowdeck ui simulator "+
			"screen --tree --json`) anywhere in the skill. profiles/ios/CLAUDE.ios.md's \"Verifying UI in "+
			"the Simulator\" section calls a screenshot-and-read loop worse than that default, orders of "+
			"magnitude more expensive for a less precise answer — either add the tree-read step, or add "+
			"%q to screenshotOnlyAllowList if the screenshot IS this skill's deliverable.",
			v.Skill, v.File, v.Skill)
	}
}

// TestSkillScreenshotViolationsRefusesAnEmptyDirectory pins the "scanned zero
// skills" guard at the level of the function that computes it, independent of
// where profiles/ios/skills happens to live. A scanner pointed at a directory
// with no skill subdirectories must report scanned == 0, which is what the
// real test above turns into a Fatal — not a silent pass.
func TestSkillScreenshotViolationsRefusesAnEmptyDirectory(t *testing.T) {
	empty := t.TempDir()
	violations, scanned, err := skillScreenshotViolations(empty, screenshotOnlyAllowList)
	if err != nil {
		t.Fatalf("scanning an empty directory should not itself error: %v", err)
	}
	if scanned != 0 {
		t.Fatalf("expected 0 skill directories scanned in an empty dir, got %d", scanned)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations from an empty dir, got %v", violations)
	}
}
