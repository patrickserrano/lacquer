package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every shipped Claude Code hook command must be syntactically valid shell.
//
// This is a real incident, not a style preference. The ios profile's
// PreToolUse Edit|Write hook embedded a quoted command inside its own
// single-quoted `bash -c '…'` wrapper:
//
//	bash -c 'if …; then echo "… run 'xcodegen generate' and commit …"; fi'
//
// The apostrophe before `xcodegen` terminates the wrapper, so bash received an
// unterminated string and every invocation died with:
//
//	-c: line 1: unexpected EOF while looking for matching `"'
//
// A PreToolUse hook that exits non-zero BLOCKS the tool call, so this did not
// degrade to "the guard is skipped" — it made Claude Code unable to write or
// edit ANY file in every project on the ios profile, whether or not the path
// had anything to do with Xcode. It shipped because nothing ever executed the
// hook: the JSON stayed valid, so no parser complained, and the string was only
// ever read by bash on a developer's machine.
//
// `bash -n` parses without executing, which is exactly the check that was
// missing — it catches the unterminated quote while running none of the guard's
// side effects.
func TestShippedClaudeHookCommandsParse(t *testing.T) {
	r := root(t)

	type hookEntry struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	type settings struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}

	var files []string
	err := filepath.WalkDir(filepath.Join(r, "profiles"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "settings.json" && strings.Contains(path, ".claude") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("found no shipped .claude/settings.json under profiles/ — this test would silently assert nothing")
	}

	// A path that matches no shipped guard, so every hook no-ops.
	probe := filepath.Join(t.TempDir(), "hook-probe.txt")

	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var s settings
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: invalid JSON: %v", f, err)
		}
		rel, _ := filepath.Rel(r, f)
		for event, entries := range s.Hooks {
			for _, entry := range entries {
				for _, h := range entry.Hooks {
					cmd := strings.TrimSpace(h.Command)
					if cmd == "" {
						continue
					}
					checked++
					// EXECUTE the hook with a benign, non-matching path and
					// look for a shell SYNTAX error.
					//
					// Static parsing cannot catch this bug, and both cheaper
					// attempts were tried and rejected against the known-broken
					// hook before landing on this one:
					//
					//  1. `bash -n` on the inner script of `bash -c '…'` — the
					//     wrong layer. The inner script's quotes balance on
					//     their own; it is the outer line the apostrophe breaks.
					//  2. `bash -n` on the whole command line — still passes.
					//     The stray apostrophes come in PAIRS, so the line is
					//     syntactically VALID; it merely splits into different
					//     arguments than intended, handing `bash -c` a truncated
					//     script. The damage is at runtime, not parse time.
					//
					// So the hook must actually run. That is safe here: with a
					// path matching none of the guards, every shipped hook is a
					// no-op that exits 0 without touching a file. Whatever the
					// exit code, a shell syntax error in stderr means the
					// command could never work for ANY input.
					proc := exec.Command("bash", "-c", cmd)
					proc.Stdin = strings.NewReader(`{"tool_input":{"file_path":"` + probe + `"}}`)
					out, _ := proc.CombinedOutput()
					for _, sig := range []string{"unexpected EOF", "syntax error", "unterminated"} {
						if strings.Contains(string(out), sig) {
							t.Errorf("%s: hook %s (matcher %q) is not runnable shell (%s):\n%s\ncommand: %s",
								rel, event, entry.Matcher, sig, out, cmd)
							break
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("parsed shipped settings.json files but found no hook commands — this test would silently assert nothing")
	}
	t.Logf("checked %d hook command(s) across %d file(s)", checked, len(files))
}
