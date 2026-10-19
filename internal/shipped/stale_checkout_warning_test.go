package shipped

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// A SessionStart hook warns, in one line, when the checkout a session starts in
// is behind its upstream or carries a CLAUDE.md region older than the lacquer
// version recorded at that upstream (lacquer#511). A stale checkout is a state
// indistinguishable from a healthy one: the rules the agent reads are simply
// old, and nothing says so.
//
// The hook must also be invisible when there is nothing to say, and must never
// cost a session its start. So half of these tests prove the warning CAN fire
// (a fixture that is behind, one with an old marker) and the other half prove
// it stays silent where it has no business speaking (up to date, detached,
// no upstream, no lock, not a repository) and bounded where git misbehaves (a
// git that hangs, a git that fails).

const staleScript = "core/root/.claude/scripts/stale-checkout-warning.sh"

// staleHookLimit is the script's own hard limit, in seconds, mirrored here so a
// test fails if the script is changed to wait longer than the settings entry
// allows. The settings `timeout` is the second, outer bound.
const staleHookLimit = 3

// markerBlock renders a CLAUDE.md carrying managed-region start markers.
func markerBlock(stamps map[string]string) string {
	var b strings.Builder
	b.WriteString("# Project\n\nproject-owned text\n\n")
	for _, key := range []string{"core", "ios"} {
		v, ok := stamps[key]
		if !ok {
			continue
		}
		b.WriteString("<!-- lacquer:" + key + ":start v" + v + " -->\nbody of " + key + "\n<!-- lacquer:" + key + ":end -->\n\n")
	}
	return b.String()
}

// staleRepo is a consumer checkout with a bare "origin" and a second clone
// ("other") used to move origin ahead of it.
type staleRepo struct {
	t      *testing.T
	origin string
	other  string
	work   string
}

// newStaleRepo builds origin with one commit holding .lacquer.lock at lockVer
// (skipped when empty) and CLAUDE.md with the given region stamps, then clones
// it to work. Everything is committed and pushed, so work starts up to date.
func newStaleRepo(t *testing.T, lockVer string, stamps map[string]string) *staleRepo {
	t.Helper()
	base := t.TempDir()
	r := &staleRepo{
		t:      t,
		origin: filepath.Join(base, "origin.git"),
		other:  filepath.Join(base, "other"),
		work:   filepath.Join(base, "work"),
	}
	if err := os.MkdirAll(r.origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Init(t, r.origin, "--bare", "-b", "main")

	seed := filepath.Join(base, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Init(t, seed, "-b", "main")
	if lockVer != "" {
		writeFile(t, filepath.Join(seed, ".lacquer.lock"), `{"version":"`+lockVer+`","files":{}}`+"\n")
	}
	if stamps != nil {
		writeFile(t, filepath.Join(seed, "CLAUDE.md"), markerBlock(stamps))
	}
	writeFile(t, filepath.Join(seed, "README.md"), "seed\n")
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-qm", "seed")
	gitIn(t, seed, "remote", "add", "origin", r.origin)
	gitIn(t, seed, "push", "-q", "-u", "origin", "main")

	for _, dst := range []string{r.work, r.other} {
		if err := gittest.Clone(r.origin, dst); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// advanceOrigin pushes n commits to origin from the other clone, then fetches
// them into work WITHOUT merging, which is exactly the stale state: the
// remote-tracking ref is ahead of HEAD and nothing has been merged.
func (r *staleRepo) advanceOrigin(n int) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		writeFile(r.t, filepath.Join(r.other, "f"+string(rune('a'+i))), "x\n")
		gitIn(r.t, r.other, "add", "-A")
		gitIn(r.t, r.other, "commit", "-qm", "advance")
	}
	gitIn(r.t, r.other, "push", "-q", "origin", "main")
	gitIn(r.t, r.work, "fetch", "-q", "origin")
}

// run executes the script as the hook does: `sh <script>`, project dir given in
// CLAUDE_PROJECT_DIR, working directory somewhere unrelated, so the script is
// shown to use the project dir and not whatever it was launched from.
func (r *staleRepo) run(extraPath string) (stdout string, elapsed time.Duration, err error) {
	r.t.Helper()
	return runStaleScript(r.t, r.work, extraPath)
}

func runStaleScript(t *testing.T, projectDir, extraPath string) (string, time.Duration, error) {
	t.Helper()
	script := filepath.Join(root(t), filepath.FromSlash(staleScript))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script)
	cmd.Dir = t.TempDir()
	env := os.Environ()
	if extraPath != "" {
		env = append(env, "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Env = append(env, "CLAUDE_PROJECT_DIR="+projectDir)
	var out strings.Builder
	cmd.Stdout = &out
	start := time.Now()
	err := cmd.Run()
	return out.String(), time.Since(start), err
}

func (r *staleRepo) mustRun() string {
	r.t.Helper()
	out, _, err := r.run("")
	if err != nil {
		r.t.Fatalf("the hook must exit 0 on every path, got %v (stdout %q)", err, out)
	}
	return out
}

func wantSilent(t *testing.T, out string) {
	t.Helper()
	if out != "" {
		t.Fatalf("want no output, got %q", out)
	}
}

func wantOneLine(t *testing.T, out string, contains ...string) {
	t.Helper()
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("want exactly one line, got %q", out)
	}
	for _, c := range contains {
		if !strings.Contains(out, c) {
			t.Errorf("warning %q does not contain %q", out, c)
		}
	}
}

func TestStaleCheckoutWarnsWhenBehindUpstream(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0"})
	r.advanceOrigin(2)
	out := r.mustRun()
	wantOneLine(t, out, "2 commits behind", "git merge --ff-only")
	if strings.Contains(out, "rebase") || strings.Contains(out, "reset") {
		t.Errorf("the fix must be a fast-forward merge, never a rebase or reset: %q", out)
	}
}

func TestStaleCheckoutWarnsOnAnOldRegionMarker(t *testing.T) {
	// Up to date with origin, but the region was stamped by an older lacquer
	// than the lock origin carries.
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.70.0", "ios": "1.73.0"})
	wantOneLine(t, r.mustRun(), "core", "v1.70.0", "v1.73.0", "lacquer sync")
}

func TestStaleCheckoutReadsALegacyBareCounterStamp(t *testing.T) {
	// `v72` is 0.72.0 (see region.stampPat), so it is older than 1.73.0.
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "72"})
	wantOneLine(t, r.mustRun(), "core", "v72", "lacquer sync")
}

func TestStaleCheckoutComparesMarkersNumericallyNotAsText(t *testing.T) {
	// "1.9.0" sorts after "1.10.0" as text and before it as a version.
	r := newStaleRepo(t, "1.10.0", map[string]string{"core": "1.9.0"})
	wantOneLine(t, r.mustRun(), "v1.9.0", "v1.10.0")

	r = newStaleRepo(t, "1.9.0", map[string]string{"core": "1.10.0"})
	wantSilent(t, r.mustRun())
}

func TestStaleCheckoutComparesMarkersToTheLockAtUpstreamNotTheLocalOne(t *testing.T) {
	// Locally the marker matches the local lock; origin has since bumped the
	// lock. The issue asks for the lock AT THE UPSTREAM REF.
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0"})
	writeFile(t, filepath.Join(r.other, ".lacquer.lock"), `{"version":"1.74.0","files":{}}`+"\n")
	gitIn(t, r.other, "add", "-A")
	gitIn(t, r.other, "commit", "-qm", "bump lock")
	gitIn(t, r.other, "push", "-q", "origin", "main")
	gitIn(t, r.work, "fetch", "-q", "origin")
	out := r.mustRun()
	// It is behind AND the marker is old against upstream's lock: both lines.
	if !strings.Contains(out, "1 commit behind") || !strings.Contains(out, "v1.74.0") {
		t.Fatalf("want the behind line and the stale-marker line naming v1.74.0, got %q", out)
	}
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; n != 2 {
		t.Errorf("want one line per condition (2), got %d: %q", n, out)
	}
}

func TestStaleCheckoutSilentWhenUpToDate(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0", "ios": "1.73.0"})
	wantSilent(t, r.mustRun())
}

func TestStaleCheckoutSilentWhenAheadOfUpstream(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0"})
	writeFile(t, filepath.Join(r.work, "local"), "x\n")
	gitIn(t, r.work, "add", "-A")
	gitIn(t, r.work, "commit", "-qm", "local only")
	wantSilent(t, r.mustRun())
}

func TestStaleCheckoutSilentOnADetachedHEAD(t *testing.T) {
	// Detached on an OLD commit while origin is well ahead: the case where
	// "compare to origin/main" would be wrong, because nothing here follows it.
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0"})
	r.advanceOrigin(3)
	gitIn(t, r.work, "checkout", "-q", "--detach", "HEAD")
	wantSilent(t, r.mustRun())
}

func TestStaleCheckoutSilentWithNoUpstream(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.73.0"})
	r.advanceOrigin(2)
	gitIn(t, r.work, "checkout", "-q", "-b", "topic")
	wantSilent(t, r.mustRun())
}

func TestStaleCheckoutSilentWhereThereIsNothingToCompare(t *testing.T) {
	t.Run("no lock at upstream", func(t *testing.T) {
		r := newStaleRepo(t, "", map[string]string{"core": "1.0.0"})
		wantSilent(t, r.mustRun())
	})
	t.Run("lock at upstream is not JSON", func(t *testing.T) {
		r := newStaleRepo(t, "", map[string]string{"core": "1.0.0"})
		writeFile(t, filepath.Join(r.other, ".lacquer.lock"), "<<<<<<< not json\n")
		gitIn(t, r.other, "add", "-A")
		gitIn(t, r.other, "commit", "-qm", "bad lock")
		gitIn(t, r.other, "push", "-q", "origin", "main")
		gitIn(t, r.work, "fetch", "-q", "origin")
		gitIn(t, r.work, "merge", "-q", "--ff-only", "origin/main")
		wantSilent(t, r.mustRun())
	})
	t.Run("lock with no version", func(t *testing.T) {
		r := newStaleRepo(t, "", map[string]string{"core": "1.0.0"})
		writeFile(t, filepath.Join(r.other, ".lacquer.lock"), `{"files":{}}`+"\n")
		gitIn(t, r.other, "add", "-A")
		gitIn(t, r.other, "commit", "-qm", "versionless lock")
		gitIn(t, r.other, "push", "-q", "origin", "main")
		gitIn(t, r.work, "fetch", "-q", "origin")
		gitIn(t, r.work, "merge", "-q", "--ff-only", "origin/main")
		wantSilent(t, r.mustRun())
	})
	t.Run("no CLAUDE.md", func(t *testing.T) {
		r := newStaleRepo(t, "1.73.0", nil)
		wantSilent(t, r.mustRun())
	})
	t.Run("CLAUDE.md without markers", func(t *testing.T) {
		r := newStaleRepo(t, "1.73.0", map[string]string{})
		wantSilent(t, r.mustRun())
	})
	t.Run("not a git repository", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), markerBlock(map[string]string{"core": "1.0.0"}))
		out, _, err := runStaleScript(t, dir, "")
		if err != nil {
			t.Fatalf("must exit 0, got %v", err)
		}
		wantSilent(t, out)
	})
	t.Run("project dir does not exist", func(t *testing.T) {
		out, _, err := runStaleScript(t, filepath.Join(t.TempDir(), "gone"), "")
		if err != nil {
			t.Fatalf("must exit 0, got %v", err)
		}
		wantSilent(t, out)
	})
}

func TestStaleCheckoutBehindStillWarnsWhenTheLockAtUpstreamIsUnreadable(t *testing.T) {
	// An unreadable lock silences only the marker comparison, not the
	// independent behind-upstream check.
	r := newStaleRepo(t, "", map[string]string{"core": "1.0.0"})
	r.advanceOrigin(1)
	wantOneLine(t, r.mustRun(), "1 commit behind", "git merge --ff-only")
}

// fakeGit writes an executable `git` into a fresh directory and returns it.
func fakeGit(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Foxy, 2026-10-18: the hard time limit goes in the proof. With a git that
// never returns, the hook exits 0 within its limit, prints nothing, and so
// session start is unaffected.
func TestStaleCheckoutHookHonoursItsTimeLimitWhenGitHangs(t *testing.T) {
	// Even on a repository that WOULD warn, a hung git must produce nothing.
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.0.0"})
	r.advanceOrigin(2)
	bin := fakeGit(t, "exec sleep 60")

	out, elapsed, err := r.run(bin)
	if err != nil {
		t.Fatalf("a hung git must not fail the hook: %v", err)
	}
	wantSilent(t, out)
	if limit := time.Duration(staleHookLimit)*time.Second + 2*time.Second; elapsed > limit {
		t.Fatalf("the hook took %v with a hung git, want within %v", elapsed, limit)
	}
}

func TestStaleCheckoutHookIsSilentWhenGitFails(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.0.0"})
	r.advanceOrigin(2)
	bin := fakeGit(t, "echo 'fatal: something broke' >&2\necho 'garbage on stdout'\nexit 128")
	out, _, err := r.run(bin)
	if err != nil {
		t.Fatalf("a failing git must not fail the hook: %v", err)
	}
	wantSilent(t, out)
}

func TestStaleCheckoutHookIsSilentWhenGitIsMissing(t *testing.T) {
	r := newStaleRepo(t, "1.73.0", map[string]string{"core": "1.0.0"})
	r.advanceOrigin(2)
	script := filepath.Join(root(t), filepath.FromSlash(staleScript))
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	// A PATH holding only sh and the tools a plain script needs, but no git.
	bin := t.TempDir()
	for _, tool := range []string{"sh", "sed", "grep", "sleep", "kill", "cat", "head", "tr", "rm", "mktemp", "dirname", "printf", "wc", "awk", "cut"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(bin, tool))
		}
	}
	cmd := exec.Command(sh, script)
	cmd.Env = []string{"PATH=" + bin, "CLAUDE_PROJECT_DIR=" + r.work}
	out, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("no git must not fail the hook: %v", runErr)
	}
	wantSilent(t, string(out))
}

// ---- rendering -----------------------------------------------------------

type staleSettings struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func sessionStartCommands(t *testing.T, raw string) (freshness []int, others []string) {
	t.Helper()
	var s staleSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	for _, e := range s.Hooks["SessionStart"] {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, "stale-checkout-warning.sh") {
				freshness = append(freshness, h.Timeout)
			} else {
				others = append(others, h.Command)
			}
		}
	}
	return freshness, others
}

func TestEveryProfileRendersTheStaleCheckoutHook(t *testing.T) {
	for _, tc := range []struct {
		fixture    string
		wantOthers []string
	}{
		{"rootapp", []string{"osascript -l JavaScript .claude/scripts/allow_mcp.js"}}, // ios: MCP hook untouched
		{"multistack", nil},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			p := fromFixture(t, tc.fixture)
			p.sync()
			timeouts, others := sessionStartCommands(t, p.read(".claude/settings.json"))
			if len(timeouts) != 1 {
				t.Fatalf("rendered settings carry %d stale-checkout SessionStart hooks, want exactly 1", len(timeouts))
			}
			if timeouts[0] <= staleHookLimit || timeouts[0] > 10 {
				t.Errorf("settings timeout %ds must exceed the script's own %ds limit and stay short", timeouts[0], staleHookLimit)
			}
			if !strings.Contains(p.read(".claude/scripts/stale-checkout-warning.sh"), "exit 0") {
				t.Error("the script is not shipped alongside the settings entry")
			}
			for _, w := range tc.wantOthers {
				found := false
				for _, o := range others {
					found = found || o == w
				}
				if !found {
					t.Errorf("existing SessionStart hook %q was lost or changed; have %q", w, others)
				}
			}
		})
	}
}

func TestEveryProfileSettingsFileShipsTheStaleCheckoutHook(t *testing.T) {
	for _, profile := range []string{"ios", "web", "supabase"} {
		t.Run(profile, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root(t), "profiles", profile, "root", ".claude", "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			timeouts, _ := sessionStartCommands(t, string(raw))
			if len(timeouts) != 1 {
				t.Fatalf("profile %s settings.json carries %d stale-checkout hooks, want 1", profile, len(timeouts))
			}
		})
	}
}

// The hook command, as rendered, must itself never fail a session: it runs when
// the script is absent (a project that excluded it) and must exit 0 silently.
func TestStaleCheckoutHookCommandIsSilentWhenTheScriptIsAbsent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "web", "root", ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s staleSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	var command string
	for _, e := range s.Hooks["SessionStart"] {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, "stale-checkout-warning.sh") {
				command = h.Command
			}
		}
	}
	if command == "" {
		t.Fatal("web settings have no stale-checkout hook")
	}
	empty := t.TempDir()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = empty
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+empty)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("with the script absent the hook command must be silent and exit 0, got err=%v out=%q", err, out)
	}
}

// Claude Code hook `timeout` is in SECONDS. The iOS MCP SessionStart hook once
// carried 10000, read as milliseconds by whoever wrote it: nearly three hours
// of permitted hang on every session start. No SessionStart hook may allow more
// than 30 seconds, in any profile, as shipped or as rendered.
func TestNoSessionStartHookAllowsMoreThanThirtySeconds(t *testing.T) {
	const maxSeconds = 30
	check := func(t *testing.T, raw string) {
		t.Helper()
		var s staleSettings
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range s.Hooks["SessionStart"] {
			for _, h := range e.Hooks {
				n++
				if h.Timeout <= 0 || h.Timeout > maxSeconds {
					t.Errorf("SessionStart hook %q has timeout %d; want 1..%d (seconds)", h.Command, h.Timeout, maxSeconds)
				}
			}
		}
		if n == 0 {
			t.Error("no SessionStart hooks found, so nothing was checked")
		}
	}
	for _, profile := range []string{"ios", "web", "supabase"} {
		t.Run("shipped/"+profile, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root(t), "profiles", profile, "root", ".claude", "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			check(t, string(raw))
		})
	}
	for _, fixture := range []string{"rootapp", "multistack"} {
		t.Run("rendered/"+fixture, func(t *testing.T) {
			p := fromFixture(t, fixture)
			p.sync()
			check(t, p.read(".claude/settings.json"))
		})
	}
}
