package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeGH puts a `gh` on PATH that replays one response per call (call N prints
// N.json, or fails with N.err's text; past the last it repeats the last) and
// records every invocation's arguments and the number of calls. Real subprocess,
// real PATH lookup, real exit codes: the same route production takes.
func fakeGH(t *testing.T, steps ...string) (calls func() int, args func() []string) {
	t.Helper()
	dir := t.TempDir()
	for i, s := range steps {
		name := fmt.Sprintf("%d.json", i+1)
		if msg, ok := strings.CutPrefix(s, "ERR:"); ok {
			name, s = fmt.Sprintf("%d.err", i+1), msg
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
d="$FAKE_GH_DIR"
n=$(cat "$d/n" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$d/n"
echo "$*" >> "$d/args"
last=$(ls "$d" | grep -E '^[0-9]+\.(json|err)$' | sed 's/\..*//' | sort -n | tail -1)
[ "$n" -gt "$last" ] && n="$last"
if [ -f "$d/$n.err" ]; then cat "$d/$n.err" >&2; exit 1; fi
cat "$d/$n.json"
`
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GH_DIR", dir)
	calls = func() int {
		b, _ := os.ReadFile(filepath.Join(dir, "n"))
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}
	args = func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "args"))
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return calls, args
}

func checkRun(name, status, conclusion string) string {
	return fmt.Sprintf(`{"__typename":"CheckRun","name":%q,"status":%q,"conclusion":%q,"startedAt":"2026-09-20T03:00:00Z","completedAt":"2026-09-20T03:01:00Z"}`, name, status, conclusion)
}

func runIn(id int, name, status, conclusion string) string {
	return fmt.Sprintf(`{"__typename":"CheckRun","workflowName":"CI","name":%q,"status":%q,"conclusion":%q,"startedAt":"2026-09-20T03:00:00Z","completedAt":"2026-09-20T03:01:00Z","detailsUrl":"https://github.com/o/r/actions/runs/%d/job/9"}`, name, status, conclusion, id)
}

func statusCtx(name, state string) string {
	return fmt.Sprintf(`{"__typename":"StatusContext","context":%q,"state":%q,"startedAt":"2026-09-20T03:00:00Z"}`, name, state)
}

func rollup(nodes ...string) string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":"0123456789abcdef","statusCheckRollup":[%s]}`, strings.Join(nodes, ","))
}

// fast keeps a real-time wait to milliseconds.
var fast = []string{"--interval", "5ms", "--timeout", "300ms", "--empty-grace", "20ms"}

func waitPR(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(append([]string{"wait", "pr"}, args...), func(string) string { return "" }, &out, &errb)
	return code, out.String(), errb.String()
}

// The four outcomes, end to end through the real `gh` lookup, each with its own
// exit code — the point of the command.
func TestWaitPRExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		steps []string
		code  int
		want  []string
	}{
		{"passed", []string{rollup(checkRun("test", "COMPLETED", "SUCCESS"))}, 0, []string{"PASSED"}},
		{"failed names the check", []string{rollup(checkRun("test", "COMPLETED", "FAILURE"), checkRun("lint", "COMPLETED", "SUCCESS"))}, 1, []string{"FAILED", "test"}},
		{"failure beats timeout, running listed as abandoned", []string{rollup(checkRun("test", "IN_PROGRESS", ""), checkRun("lint", "COMPLETED", "FAILURE"))}, 1, []string{"FAILED", "lint", "The failure is decisive", "abandoned", "test"}},
		{"superseded cancelled run is ignored and named", []string{rollup(runIn(100, "test", "COMPLETED", "CANCELLED"), runIn(200, "test", "COMPLETED", "SUCCESS"))}, 0, []string{"PASSED", "ignored, superseded", "test (run 100, cancelled)"}},
		{"timed out names what ran", []string{rollup(checkRun("test", "IN_PROGRESS", ""), checkRun("lint", "COMPLETED", "SUCCESS"))}, 2, []string{"TIMED OUT", "still running: test"}},
		{"no checks is not green", []string{rollup()}, 3, []string{"NO CHECKS"}},
		{"pending commit status is not done", []string{rollup(statusCtx("ci/legacy", "PENDING"))}, 2, []string{"TIMED OUT", "ci/legacy"}},
		{"failed commit status", []string{rollup(statusCtx("ci/legacy", "ERROR"))}, 1, []string{"FAILED", "ci/legacy"}},
		{"skipped exits 0 but is named", []string{rollup(checkRun("test", "COMPLETED", "SUCCESS"), checkRun("deploy", "COMPLETED", "SKIPPED"))}, 0, []string{"skipped: deploy"}},
		{"gh keeps failing", []string{"ERR:HTTP 502 Bad Gateway"}, 4, []string{"ERROR", "HTTP 502"}},
		{"merged PR", []string{`{"state":"MERGED","headRefOid":"abc","statusCheckRollup":[]}`}, 4, []string{"MERGED"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, tc.steps...)
			code, out, errb := waitPR(t, append([]string{"425"}, fast...)...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, out, errb)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("stdout missing %q:\n%s", w, out)
				}
			}
			if tc.code != 0 && strings.Contains(out, "PASSED") {
				t.Errorf("a non-zero outcome must not say PASSED:\n%s", out)
			}
		})
	}
}

// The reported bug, through the CLI: a check in flight has conclusion "", and
// the wait must still be blocking.
func TestWaitPRKeepsBlockingWhileACheckIsInFlight(t *testing.T) {
	inflight := rollup(checkRun("test", "IN_PROGRESS", ""))
	done := rollup(checkRun("test", "COMPLETED", "SUCCESS"))
	calls, _ := fakeGH(t, inflight, inflight, inflight, inflight, done)
	code, out, _ := waitPR(t, "425", "--interval", "5ms", "--timeout", "5s")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if calls() < 5 {
		t.Fatalf("returned after %d polls while a check was still in flight", calls())
	}
}

func TestWaitPRPassesRepoAndNumberToGH(t *testing.T) {
	_, args := fakeGH(t, rollup(checkRun("test", "COMPLETED", "SUCCESS")))
	waitPR(t, "425", "--repo", "acme/widgets", "--interval", "5ms", "--timeout", "1s")
	got := args()[0]
	for _, w := range []string{"pr view", "-R acme/widgets", "425", "state,headRefOid,statusCheckRollup"} {
		if !strings.Contains(got, w) {
			t.Errorf("gh was run as %q, missing %q", got, w)
		}
	}
}

func TestWaitPRFlagsMayFollowTheNumber(t *testing.T) {
	fakeGH(t, rollup(checkRun("test", "COMPLETED", "FAILURE")))
	for _, args := range [][]string{{"--json", "425"}, {"425", "--json"}} {
		code, out, errb := waitPR(t, append(args, fast...)...)
		if code != 1 {
			t.Fatalf("%v: exit %d stderr %q", args, code, errb)
		}
		var got struct {
			Outcome  string `json:"outcome"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil || got.Outcome != "failed" || got.ExitCode != 1 {
			t.Errorf("%v: --json output %q (%v) -> %+v", args, out, err, got)
		}
	}
}

func TestWaitPRWithoutGHIsAClearErrorNotSuccess(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a directory with nothing in it
	code, out, _ := waitPR(t, "425", "--interval", "5ms", "--timeout", "1s")
	if code != 4 {
		t.Fatalf("exit %d, want 4:\n%s", code, out)
	}
	if !strings.Contains(out, "gh not found") {
		t.Errorf("want a clear 'gh not found' message:\n%s", out)
	}
}

func TestWaitPRUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"abc"}, {"0"}, {"1", "2"}, {"1", "--timeout", "0s"}, {"1", "--interval", "-1s"}, {"1", "--nope"}} {
		code, _, errb := waitPR(t, args...)
		if code != 4 {
			t.Errorf("%v: exit %d, want 4 (never 0-3, which mean the PR was judged)", args, code)
		}
		if errb == "" {
			t.Errorf("%v: no explanation on stderr", args)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"wait"}, func(string) string { return "" }, &out, &errb); code != 4 || errb.Len() == 0 {
		t.Errorf("`wait` alone: exit %d stderr %q", code, errb.String())
	}
}

// The codes are part of the interface, so `lacquer help` must say all of them.
func TestHelpDocumentsTheWaitExitCodes(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, func(string) string { return "" }, &out, &errb); code != 0 {
		t.Fatalf("help exit %d", code)
	}
	help := out.String()
	for _, want := range []string{
		"wait pr <N>", "0  every check finished and none failed", "1  at least one check FAILED", "A failure is decisive", "and NONE failed",
		"2  TIMED OUT", "3  NO CHECKS", "4  the wait itself failed", "no model tokens",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
