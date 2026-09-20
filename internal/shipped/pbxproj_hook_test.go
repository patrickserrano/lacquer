package shipped

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The iOS profile's PreToolUse hook blocks Edit/Write on Xcode project files.
// Its non-XcodeGen message used to say "Create Swift files and add them to the
// target in Xcode." That sent the operator into Xcode for every version bump
// (flare 1.0.1, dailybread 1.9), and Xcode then rewrote unrelated project lines
// (lacquer#410). The BLOCK is right — a hand-edit of a .pbxproj is how a project
// stops building — so these tests pin both halves: the hook still blocks, and
// what it says now names the two sanctioned paths.
//
// The hook is one line of shell inside a JSON string inside `bash -c '…'`, so it
// is tested the only way that means anything: the rendered settings.json is
// parsed, and the command is EXECUTED with a real hook payload on stdin.

// bumpScript is the shipped script the hook message points at, relative to the
// component root — the same place it is synced to.
const bumpScript = "scripts/bump-marketing-version.sh"

// renderedPbxprojHook syncs an iOS project and returns the command of the
// PreToolUse hook that guards Xcode project files, read from the RENDERED
// .claude/settings.json rather than from the profile source.
func renderedPbxprojHook(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		// The hook itself shells out to jq; without it every path reads as empty
		// and the guard passes everything, which would make the blocking
		// assertions below meaningless rather than failing.
		t.Fatal("jq is required: the hook under test calls it")
	}
	p := fromFixture(t, "rootapp")
	p.sync()
	raw := p.read(".claude/settings.json")

	// The file must be valid JSON as a tool would read it, not just as encoding/json does.
	jq := exec.Command("jq", ".")
	jq.Stdin = strings.NewReader(raw)
	if out, err := jq.CombinedOutput(); err != nil {
		t.Fatalf("rendered .claude/settings.json does not parse with jq: %v\n%s", err, out)
	}

	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("rendered .claude/settings.json is not valid JSON: %v", err)
	}
	var found []string
	for _, e := range s.Hooks["PreToolUse"] {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, "pbxproj") {
				found = append(found, h.Command)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one PreToolUse hook guarding pbxproj files, found %d", len(found))
	}
	return found[0]
}

// runHook runs a hook command with a PreToolUse payload naming filePath.
func runHook(t *testing.T, cmd, filePath string) (stderr string, code int) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"tool_input": map[string]any{"file_path": filePath}})
	if err != nil {
		t.Fatal(err)
	}
	proc := exec.Command("bash", "-c", cmd)
	proc.Stdin = bytes.NewReader(payload)
	var errBuf bytes.Buffer
	proc.Stderr = &errBuf
	err = proc.Run()
	switch e := err.(type) {
	case nil:
		return errBuf.String(), 0
	case *exec.ExitError:
		return errBuf.String(), e.ExitCode()
	default:
		t.Fatalf("could not run hook: %v", err)
		return "", -1
	}
}

// mustContain fails naming each missing fragment, so a message that lost one of
// the paths says which.
func mustContain(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s does not mention %q:\n%s", what, w, got)
		}
	}
}

// THE DEFECT. Without a project.yml the hook said "add them to the target in
// Xcode". It must still block (exit 2), and now name the version-bump path, the
// XcodeGen path, the operator fallback, and the script by its path.
func TestPbxprojHookNamesTheSanctionedPathsWithoutXcodeGen(t *testing.T) {
	t.Parallel()
	hook := renderedPbxprojHook(t)
	dir := t.TempDir()

	msg, code := runHook(t, hook, filepath.Join(dir, "App.xcodeproj", "project.pbxproj"))
	if code != 2 {
		t.Fatalf("the hook must still BLOCK an edit of project.pbxproj: exit %d, want 2\n%s", code, msg)
	}
	mustContain(t, "the non-XcodeGen block message", msg,
		"BLOCKED",
		"MARKETING_VERSION", // the version-bump path
		"Bash",              // ...done through Bash, not Edit/Write
		bumpScript,          // ...and the script that does it safely
		"XcodeGen",          // the structural path...
		"project.yml",       // ...named by the file that proves a repo has it
		"operator",          // ...and what to do when it does not
	)
	for _, stale := range []string{"add them to the target in Xcode", "Create Swift files"} {
		if strings.Contains(msg, stale) {
			t.Errorf("the message still carries the instruction that sent the operator into Xcode (%q):\n%s", stale, msg)
		}
	}
}

// The XcodeGen branch is kept; it gains the version-bump sentence.
func TestPbxprojHookKeepsTheXcodeGenMessage(t *testing.T) {
	t.Parallel()
	hook := renderedPbxprojHook(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.yml"), []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, code := runHook(t, hook, filepath.Join(dir, "App.xcodeproj", "project.pbxproj"))
	if code != 2 {
		t.Fatalf("the hook must still BLOCK on an XcodeGen project: exit %d, want 2\n%s", code, msg)
	}
	mustContain(t, "the XcodeGen block message", msg,
		"BLOCKED",
		"generated by XcodeGen",
		"xcodegen generate",
		"MARKETING_VERSION",
	)
	if strings.Contains(msg, "add them to the target in Xcode") {
		t.Errorf("the XcodeGen message regressed to the Xcode instruction:\n%s", msg)
	}
}

// Blocking stays narrow: the other Xcode-owned files still block, and an
// ordinary source file does not.
func TestPbxprojHookBlocksXcodeFilesAndNothingElse(t *testing.T) {
	t.Parallel()
	hook := renderedPbxprojHook(t)
	dir := t.TempDir()

	for _, blocked := range []string{
		"App.xcodeproj/project.pbxproj",
		"App.xcworkspace/contents.xcworkspacedata",
		"Base.lproj/Main.storyboard",
		"View.xib",
	} {
		if msg, code := runHook(t, hook, filepath.Join(dir, blocked)); code != 2 {
			t.Errorf("%s: exit %d, want 2 (blocked)\n%s", blocked, code, msg)
		}
	}
	for _, allowed := range []string{"Sources/App.swift", "project.yml", "scripts/bump-marketing-version.sh"} {
		if msg, code := runHook(t, hook, filepath.Join(dir, allowed)); code != 0 {
			t.Errorf("%s: exit %d, want 0 (not an Xcode file)\n%s", allowed, code, msg)
		}
	}
}

// The script the message points at ships, and ships executable — the message
// names it, so a synced repo that lacks it (or has it without the bit) would be
// told to run something that is not there.
func TestBumpMarketingVersionScriptShips(t *testing.T) {
	t.Parallel()
	src := filepath.Join(root(t), "profiles", "ios", "root", filepath.FromSlash(bumpScript))
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("the ios profile does not ship %s: %v", bumpScript, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable in the profile (mode %v)", bumpScript, info.Mode())
	}

	p := fromFixture(t, "rootapp")
	p.sync()
	dst, err := os.Stat(filepath.Join(p.root, filepath.FromSlash(bumpScript)))
	if err != nil {
		t.Fatalf("a synced iOS project does not receive %s: %v", bumpScript, err)
	}
	if dst.Mode()&0o111 == 0 {
		t.Errorf("the synced %s lost its executable bit (mode %v)", bumpScript, dst.Mode())
	}
}
