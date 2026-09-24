package shipped

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/region"
)

// scripts/sim-os-log.sh — the sanctioned, read-only way to read os_log from the
// simulator a run OWNS (lacquer#413).
//
// FlowDeck's log stream carries stdout only, so SwiftUI and CoreData diagnostics,
// which land in os_log, are invisible to it. The machine-wide flowdeck skill says
// never to reach for simctl, and an IC diagnosing dailybread#539 had to break that
// rule once, disclosed, to see them. The script makes the safe form the easy one.
//
// These tests stand a FAKE `xcrun` in front of the real one and record every call
// it receives. No real simulator is ever booted, spawned or listed on the machine
// running them. Each refusal is asserted twice: the script exits non-zero AND the
// fake never saw a `simctl spawn` — a refusal that still ran the command, or that
// refused after running it, is a script that reads as guarded and is not.

const (
	simLogScript = "scripts/sim-os-log.sh"
	ownUDID      = "1A2B3C4D-2222-3333-4444-5555AAAA6666"
	otherUDID    = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" // valid shape, not in the list
)

// fakeXcrun answers `simctl list devices -j` with a fixed device list and records
// every invocation — one line per call, arguments space-joined — to $FAKE_CALLS,
// plus the argv of the last `simctl spawn`, one argument per line, to $FAKE_ARGV.
// Any other subcommand exits 99, so a script that reached for one fails loudly
// even before the test inspects the call log.
const fakeXcrun = `#!/bin/sh
echo "$*" >> "$FAKE_CALLS"
if [ "$1" = simctl ] && [ "$2" = list ]; then
  cat <<'JSON'
{
  "devices" : {
    "com.apple.CoreSimulator.SimRuntime.iOS-27-0" : [
      { "udid" : "1A2B3C4D-2222-3333-4444-5555AAAA6666", "name" : "Own", "state" : "Booted" },
      { "udid" : "99999999-8888-7777-6666-555555555555", "name" : "Other", "state" : "Shutdown" }
    ]
  }
}
JSON
  exit 0
fi
if [ "$1" = simctl ] && [ "$2" = spawn ]; then
  : > "$FAKE_ARGV"
  for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_ARGV"; done
  echo "fake os_log line"
  exit "${FAKE_SPAWN_EXIT:-0}"
fi
echo "fake xcrun: unexpected call: $*" >&2
exit 99
`

type simLog struct {
	t     *testing.T
	calls string // path of the call log
	argv  string // path of the last spawn's argv
	path  string // directory holding the fake xcrun
}

func newSimLog(t *testing.T) *simLog {
	t.Helper()
	dir := t.TempDir()
	s := &simLog{t: t, calls: filepath.Join(dir, "calls"), argv: filepath.Join(dir, "argv"), path: dir}
	writeExe(t, filepath.Join(dir, "xcrun"), fakeXcrun)
	return s
}

// run executes the shipped script and returns stdout, stderr and the exit code.
func (s *simLog) run(env []string, args ...string) (stdout, stderr string, code int) {
	s.t.Helper()
	cmd := exec.Command(filepath.Join(root(s.t), "profiles", "ios", "root", filepath.FromSlash(simLogScript)), args...)
	cmd.Env = append(os.Environ(), "PATH="+s.path+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_CALLS="+s.calls, "FAKE_ARGV="+s.argv)
	cmd.Env = append(cmd.Env, env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.String(), errb.String(), 0
	case errors.As(err, &exit):
		return out.String(), errb.String(), exit.ExitCode()
	default:
		s.t.Fatalf("could not run %s: %v", simLogScript, err)
		return "", "", -1
	}
}

// callLog is every line the fake xcrun was called with.
func (s *simLog) callLog() []string {
	b, err := os.ReadFile(s.calls)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func (s *simLog) spawnArgv() []string {
	b, err := os.ReadFile(s.argv)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// wantNoSpawn fails if xcrun was asked for anything but the device listing.
func (s *simLog) wantNoSpawn() {
	s.t.Helper()
	for _, c := range s.callLog() {
		if c != "simctl list devices -j" {
			s.t.Errorf("a refused invocation still called xcrun %q", c)
		}
	}
	if _, err := os.Stat(s.argv); err == nil {
		s.t.Errorf("a refused invocation reached `simctl spawn`: %v", s.spawnArgv())
	}
}

func TestSimOSLogIsShippedAndExecutable(t *testing.T) {
	p := filepath.Join(root(t), "profiles", "ios", "root", filepath.FromSlash(simLogScript))
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("the iOS profile does not ship %s: %v", simLogScript, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable (%v) — synced repos would get a script they cannot run", simLogScript, fi.Mode())
	}

	project := syncedProject(t, "")
	shipped := filepath.Join(project, filepath.FromSlash(simLogScript))
	fi, err = os.Stat(shipped)
	if err != nil {
		t.Fatalf("sync did not deliver %s to an iOS project: %v", simLogScript, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("the synced %s lost its executable bit (%v)", simLogScript, fi.Mode())
	}
}

// A valid own UDID runs exactly `simctl spawn <udid> log show` with a bounded
// window, and nothing else. The default window applies when --last is omitted.
func TestSimOSLogRunsBoundedLogShowOnTheOwnDevice(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string // full argv the fake xcrun must have received
	}{
		{"defaults", []string{ownUDID},
			[]string{"simctl", "spawn", ownUDID, "log", "show", "--last", "5m", "--style", "compact", "--info"}},
		{"explicit window", []string{ownUDID, "--last", "20m"},
			[]string{"simctl", "spawn", ownUDID, "log", "show", "--last", "20m", "--style", "compact", "--info"}},
		{"lowercase udid is normalised", []string{strings.ToLower(ownUDID), "--last", "90s"},
			[]string{"simctl", "spawn", ownUDID, "log", "show", "--last", "90s", "--style", "compact", "--info"}},
		{"subsystem becomes a predicate", []string{ownUDID, "--subsystem", "com.x.demo"},
			[]string{"simctl", "spawn", ownUDID, "log", "show", "--last", "5m", "--style", "compact", "--info",
				"--predicate", `subsystem == "com.x.demo"`}},
		{"explicit predicate is one argument", []string{ownUDID, "--predicate", `subsystem == "com.apple.coredata" AND category == 'x'`, "--style", "json"},
			[]string{"simctl", "spawn", ownUDID, "log", "show", "--last", "5m", "--style", "json", "--info",
				"--predicate", `subsystem == "com.apple.coredata" AND category == 'x'`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSimLog(t)
			stdout, stderr, code := s.run(nil, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, "fake os_log line") {
				t.Errorf("the log output did not reach stdout: %q", stdout)
			}
			got := s.spawnArgv()
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("xcrun received\n  %q\nwant\n  %q", got, tc.want)
			}
			// Bounded by construction: whatever the caller passed, a window was.
			if !containsString(got, "--last") {
				t.Errorf("no --last window reached `log show`: %q", got)
			}
			// The only calls: the membership check, then the one read.
			calls := s.callLog()
			if len(calls) != 2 || calls[0] != "simctl list devices -j" || !strings.HasPrefix(calls[1], "simctl spawn "+ownUDID+" log show ") {
				t.Errorf("unexpected xcrun calls: %q", calls)
			}
		})
	}
}

// The command it prints is what it ran — asserted by having a shell PARSE the
// printed line back into words and comparing them to the argv the fake received,
// so quoting a predicate with spaces and quotes cannot make the disclosure differ
// from the action. Printed once, to stderr, so stdout stays pure log.
func TestSimOSLogPrintsExactlyWhatItRan(t *testing.T) {
	s := newSimLog(t)
	pred := `subsystem == "com.x.demo" AND eventMessage CONTAINS 'it''s'`
	stdout, stderr, code := s.run(nil, ownUDID, "--last", "3m", "--predicate", pred)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr)
	}
	if stdout != "fake os_log line\n" {
		t.Errorf("stdout must be exactly the log output, so the disclosure line has to stay on stderr: %q", stdout)
	}

	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.Contains(l, "xcrun simctl spawn") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("the command must be printed exactly once, found %d in stderr:\n%s", len(lines), stderr)
	}
	line := lines[0]
	idx := strings.Index(line, "xcrun ")
	// Have bash re-parse the printed command into its words.
	parse := exec.Command("bash", "-c", `set -- `+line[idx:]+`; printf '%s\n' "$@"`)
	out, err := parse.Output()
	if err != nil {
		t.Fatalf("the printed command %q is not valid shell: %v", line, err)
	}
	printed := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	ran := append([]string{"xcrun"}, s.spawnArgv()...)
	if strings.Join(printed, "\x00") != strings.Join(ran, "\x00") {
		t.Errorf("printed command differs from what ran\n printed: %q\n     ran: %q", printed, ran)
	}
}

func TestSimOSLogRefuses(t *testing.T) {
	cases := []struct {
		name string
		args []string
		msg  string // substring the refusal must contain — the SPECIFIC reason, since a second layer may also refuse
	}{
		{"no arguments", nil, "missing udid"},
		{"empty udid", []string{""}, "missing udid"},
		{"the literal booted", []string{"booted"}, "whichever simulator"},
		{"booted with a window", []string{"booted", "--last", "5m"}, "whichever simulator"},
		{"an unknown but well-formed udid", []string{otherUDID}, "not a simulator"},
		{"a udid-shaped string is not a prefix match", []string{"1A2B3C4D-2222-3333-4444"}, "udid"},
		{"a name instead of a udid", []string{"iPhone 17 Pro"}, "udid"},
		{"a flag in the udid position", []string{"--last", "5m"}, "udid"},
		{"a mutating subcommand in the udid position", []string{"boot"}, "udid"},
		{"a mutating subcommand after the udid", []string{ownUDID, "boot"}, "boot"},
		{"erase after a valid window", []string{ownUDID, "--last", "5m", "erase"}, "erase"},
		{"shutdown", []string{ownUDID, "shutdown"}, "shutdown"},
		{"an unknown flag", []string{ownUDID, "--install"}, "--install"},
		{"log stream smuggled as a flag", []string{ownUDID, "--stream"}, "--stream"},
		{"a second udid", []string{ownUDID, ownUDID}, ownUDID},
		{"double dash passthrough", []string{ownUDID, "--", "killall", "Simulator"}, "--"},
		{"an unbounded window", []string{ownUDID, "--last", "forever"}, "--last"},
		{"a zero window", []string{ownUDID, "--last", "0m"}, "--last"},
		{"a window with no unit", []string{ownUDID, "--last", "5"}, "--last"},
		{"a missing window value", []string{ownUDID, "--last"}, "--last"},
		{"a missing predicate value", []string{ownUDID, "--predicate"}, "--predicate"},
		{"an empty predicate", []string{ownUDID, "--predicate", ""}, "--predicate"},
		{"an unknown style", []string{ownUDID, "--style", "rm -rf"}, "--style"},
		{"predicate and subsystem together", []string{ownUDID, "--predicate", "x", "--subsystem", "y"}, "--subsystem"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSimLog(t)
			stdout, stderr, code := s.run(nil, tc.args...)
			if code == 0 {
				t.Fatalf("exit 0, want a refusal\nstdout: %s\nstderr: %s", stdout, stderr)
			}
			if !strings.Contains(stderr, tc.msg) {
				t.Errorf("the refusal does not say why (want %q in it):\n%s", tc.msg, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal wrote to stdout: %q", stdout)
			}
			s.wantNoSpawn()
		})
	}
}

// A predicate is data handed to `log show`, never shell. A value that looks like a
// second command is passed through as the one argument it is.
func TestSimOSLogNeverExecutesAPredicate(t *testing.T) {
	s := newSimLog(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	pred := `x"; touch ` + marker + `; echo "`
	if _, stderr, code := s.run(nil, ownUDID, "--predicate", pred); code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the predicate was executed as shell")
	}
	got := s.spawnArgv()
	if got[len(got)-1] != pred {
		t.Errorf("the predicate did not arrive as a single argument: %q", got)
	}
}

// A failing read must be visible: the script's exit status is `log show`'s, not
// a flattened 0 and not a refusal's.
func TestSimOSLogPropagatesTheReadsFailure(t *testing.T) {
	s := newSimLog(t)
	_, _, code := s.run([]string{"FAKE_SPAWN_EXIT=7"}, ownUDID)
	if code != 7 {
		t.Errorf("exit %d, want the read's own status 7", code)
	}
}

// The script must not be able to do anything but read. Asserted on the source as
// well as on behaviour, because behaviour only covers the paths a test takes.
func TestSimOSLogSourceOnlyReads(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", filepath.FromSlash(simLogScript)))
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, l := range strings.Split(string(b), "\n") {
		if trimmed := strings.TrimSpace(l); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			code = append(code, trimmed)
		}
	}
	src := strings.Join(code, "\n")

	// Every simctl subcommand the script names is one of the two it is allowed.
	for _, m := range regexp.MustCompile(`simctl\s+(\S+)`).FindAllStringSubmatch(src, -1) {
		if m[1] != "list" && m[1] != "spawn" {
			t.Errorf("the script names `simctl %s`; only `list` and `spawn` are permitted", m[1])
		}
	}
	for _, w := range []string{"killall", "pkill", "log stream"} {
		if strings.Contains(src, w) {
			t.Errorf("the script mentions %q outside a comment; it must only ever `log show`", w)
		}
	}
	if !strings.Contains(src, "simctl spawn") || !strings.Contains(src, "log show") {
		t.Error("the script no longer runs `simctl spawn <udid> log show`")
	}
}

// ---------------------------------------------------------------------------
// The rule text
// ---------------------------------------------------------------------------

// simLogPhrases are the load-bearing claims of the block. Each is asserted on the
// shipped rule file, on what a synced project actually renders, and on the site
// mirror — a block present in one and absent in the others is the drift the
// docsmirror check exists to stop.
var simLogPhrases = []struct{ phrase, why string }{
	{"os_log", "the thing flowdeck's stream cannot see"},
	{"stdout only", "why the exception exists"},
	{"scripts/sim-os-log.sh", "the script that makes the safe form the easy one"},
	{"read-only", "the exception is a read, nothing more"},
	{"own simulator", "scoped to the device the run owns, never `booted`"},
	{"sanctioned exception", "how a session following the global flowdeck skill should treat it"},
	{"PR body", "the read is disclosed, as the dailybread IC did"},
	{"stays with flowdeck", "everything mutating is unchanged"},
}

func TestIOSRulesSanctionAnOSLogRead(t *testing.T) {
	r := root(t)
	source, err := os.ReadFile(filepath.Join(r, "profiles", "ios", "CLAUDE.ios.md"))
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := os.ReadFile(filepath.Join(r, "site", "src", "content", "docs", "guides", "ios-rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The rendered region a project's agent reads.
	project := syncedProject(t, "")
	var rendered string
	for _, name := range []string{"CLAUDE.md", "ios/CLAUDE.md"} {
		raw, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		if body, ok := region.ExtractBody(string(raw), "ios"); ok {
			rendered = body
		}
	}
	if rendered == "" {
		t.Fatal("no synced CLAUDE.md carries an ios region — this test would assert nothing")
	}

	for _, doc := range []struct{ name, body string }{
		{"profiles/ios/CLAUDE.ios.md", string(source)},
		{"the rendered ios region", rendered},
		{"site/.../ios-rules.md", string(mirror)},
	} {
		for _, p := range simLogPhrases {
			if !strings.Contains(doc.body, p.phrase) {
				t.Errorf("%s lacks %q (%s)", doc.name, p.phrase, p.why)
			}
		}
	}

	// The block must not contradict the surrounding "preference, not a wall" text
	// by hardening into a prohibition on the raw tools.
	if strings.Contains(string(source), "never use simctl") {
		t.Error("the iOS rules must not restate the global flowdeck skill's blanket ban — lacquer does not ship it and this profile says the raw tools are not blocked")
	}
}
