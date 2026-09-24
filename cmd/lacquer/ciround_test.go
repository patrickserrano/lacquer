package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/cirounds/fakegh"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

// TestFakeGHHelper is not a test. It is the body of the `gh` that statefulGH puts
// on PATH: a shim re-executes this test binary, which answers from fakegh's
// on-disk state, so every `lacquer ci-round` run below sees a real subprocess on
// a real PATH and a PR whose state survives the process, as a real one does.
func TestFakeGHHelper(t *testing.T) {
	dir := os.Getenv("FAKEGH_STATE")
	if dir == "" {
		t.Skip("helper for statefulGH")
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	out, err := fakegh.Run(dir, args...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
	os.Exit(0)
}

// statefulGH installs the fake and returns its state directory. head is the
// PR's head commit.
func statefulGH(t *testing.T, head string) string {
	t.Helper()
	state := t.TempDir()
	if err := fakegh.Init(state, head); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	shim := "#!/bin/sh\nexec '" + self + "' -test.run='^TestFakeGHHelper$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKEGH_STATE", state)
	return state
}

// project makes a repository whose origin/main carries manifest (when non-empty)
// and chdirs into it. It returns the repository's HEAD.
func project(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	gittest.Init(t, dir)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".lacquer.toml")
	}
	git("commit", "--allow-empty", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	chdir(t, dir)
	return git("rev-parse", "HEAD")
}

// commit adds a commit on the working branch, as the agent's fix would, and
// returns its SHA.
func commit(t *testing.T, msg string) string {
	t.Helper()
	for _, args := range [][]string{{"commit", "--allow-empty", "-m", msg}, {"rev-parse", "HEAD"}} {
		cmd := exec.Command("git", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		if args[0] == "rev-parse" {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func ciRound(t *testing.T, env map[string]string, args ...string) (code int, out, errs string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(append([]string{"ci-round"}, args...), func(k string) string { return env[k] }, &o, &e)
	return code, o.String(), e.String()
}

const goodReason = "the lint check failed on an unused import in wait.go, removed it"

// The whole loop, at the command, over a real subprocess gh, with each call a
// fresh process-equivalent: only the PR carries state.
func TestCIRoundCommandTwoRoundsThenRefusalOnTheThirdAttempt(t *testing.T) {
	a := project(t, "")
	state := statefulGH(t, a)

	code, out, errs := ciRound(t, nil, "begin", "7") // --sha defaults to git HEAD
	if code != 0 {
		t.Fatalf("round 1: exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "round 1 of 2") {
		t.Errorf("round 1 output: %s", out)
	}

	if err := fakegh.SetRollup(state, "OPEN", a, fakegh.Failed("lint")); err != nil {
		t.Fatal(err)
	}
	b := commit(t, "fix lint")
	// No reason: refused, and refused BEFORE anything is recorded.
	if code, out, _ = ciRound(t, nil, "begin", "7"); code != 11 {
		t.Fatalf("round 2 with no reason: exit %d, want 11\n%s", code, out)
	}
	// Flags either side of the number.
	if code, out, errs = ciRound(t, nil, "begin", "--reason", goodReason, "7"); code != 0 {
		t.Fatalf("round 2: exit %d\n%s%s", code, out, errs)
	}

	if err := fakegh.SetRollup(state, "OPEN", b, fakegh.Failed("test")); err != nil {
		t.Fatal(err)
	}
	commit(t, "another guess")
	code, out, _ = ciRound(t, nil, "begin", "7", "--reason", goodReason+" test")
	if code != 10 {
		t.Fatalf("third attempt: exit %d, want 10\n%s", code, out)
	}
	if !strings.Contains(out, "EXHAUSTED") || !strings.Contains(out, "test") {
		t.Errorf("refusal does not report what is failing:\n%s", out)
	}
	// Nothing was pushed or failed: the only gh writes are comments.
	for _, c := range fakegh.Calls(state) {
		if strings.HasPrefix(c, "pr comment") || strings.HasPrefix(c, "pr view") {
			continue
		}
		t.Errorf("unexpected gh call %q", c)
	}
	if code, _, _ = ciRound(t, nil, "status", "7"); code != 10 {
		t.Errorf("status of an exhausted PR: exit %d, want 10", code)
	}
}

// The cap comes from the manifest as committed on origin/main.
func TestCIRoundCommandReadsTheCapFromTheManifest(t *testing.T) {
	a := project(t, "[project]\nname=\"x\"\nci_round_cap = 1\n")
	state := statefulGH(t, a)
	if code, out, _ := ciRound(t, nil, "begin", "7"); code != 0 {
		t.Fatalf("round 1: exit %d\n%s", code, out)
	}
	if err := fakegh.SetRollup(state, "OPEN", a, fakegh.Failed("lint")); err != nil {
		t.Fatal(err)
	}
	commit(t, "second")
	if code, out, _ := ciRound(t, nil, "begin", "7", "--reason", goodReason); code != 10 {
		t.Fatalf("with ci_round_cap = 1 a second round: exit %d, want 10\n%s", code, out)
	}
}

// A branch must not be able to raise its own cap by editing its own manifest.
func TestCIRoundCommandIgnoresTheWorkingTreeManifest(t *testing.T) {
	a := project(t, "[project]\nname=\"x\"\nci_round_cap = 1\n")
	state := statefulGH(t, a)
	if err := os.WriteFile(".lacquer.toml", []byte("[project]\nname=\"x\"\nci_round_cap = 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := ciRound(t, nil, "begin", "7"); code != 0 {
		t.Fatalf("round 1: exit %d\n%s", code, out)
	}
	if err := fakegh.SetRollup(state, "OPEN", a, fakegh.Failed("lint")); err != nil {
		t.Fatal(err)
	}
	commit(t, "second")
	if code, out, _ := ciRound(t, nil, "begin", "7", "--reason", goodReason); code != 10 {
		t.Fatalf("a working-tree ci_round_cap raised the cap: exit %d\n%s", code, out)
	}
}

// An invalid or unreadable manifest is not "use the default": it is COULD NOT
// CHECK, and nothing is recorded.
func TestCIRoundCommandFailsClosedOnABadManifest(t *testing.T) {
	project(t, "[project]\nname=\"x\"\nci_round_cap = 0\n")
	state := statefulGH(t, strings.Repeat("a", 40))
	code, _, errs := ciRound(t, nil, "begin", "7")
	if code != 13 || !strings.Contains(errs, "ci_round_cap") {
		t.Fatalf("exit %d, stderr %q; want 13 naming ci_round_cap", code, errs)
	}
	if len(fakegh.Calls(state)) != 0 {
		t.Errorf("talked to gh before the cap was known: %v", fakegh.Calls(state))
	}
}

func TestCIRoundCommandUsageErrors(t *testing.T) {
	project(t, "")
	statefulGH(t, strings.Repeat("a", 40))
	for _, args := range [][]string{{}, {"nope", "7"}, {"begin"}, {"begin", "x"}, {"begin", "0"}, {"begin", "7", "8"}, {"status", "--bogus", "7"}, {"begin", "7", "--review", ""}, {"begin", "7", "--review", "  "}, {"reset", "7", "--review", "PM requested changes"}, {"begin", "7", "--review", "PM requested changes", "--reason", goodReason}} {
		if code, _, _ := ciRound(t, nil, args...); code != 13 {
			t.Errorf("ci-round %v: exit %d, want 13", args, code)
		}
	}
}

// $LACQUER_INBOX is honoured, so an agent's environment is enough.
func TestCIRoundCommandRaisesAnInboxActionOnExhaustion(t *testing.T) {
	a := project(t, "[project]\nname=\"x\"\nci_round_cap = 1\n")
	state := statefulGH(t, a)
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	env := map[string]string{"LACQUER_INBOX": path}
	if code, out, _ := ciRound(t, env, "begin", "7"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if err := fakegh.SetRollup(state, "OPEN", a, fakegh.Failed("lint")); err != nil {
		t.Fatal(err)
	}
	commit(t, "second")
	if code, out, _ := ciRound(t, env, "begin", "7", "--reason", goodReason); code != 10 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	es, _, err := inbox.ListOpen(path)
	if err != nil || len(es) != 1 || es[0].Type != inbox.Action {
		t.Fatalf("inbox = %v, %v", es, err)
	}
}

// Every exit code the command can return is in `lacquer help`, or an agent has
// to guess at what 12 means.
func TestUsageDocumentsEveryCIRoundExitCode(t *testing.T) {
	var out bytes.Buffer
	usage(&out)
	for _, s := range []string{"ci-round begin", "ci-round status", " 0  granted", " 10  EXHAUSTED", " 11  round 2+", " 12  nothing to spend a round on", " 13  could not check", "ci_round_cap"} {
		if !strings.Contains(out.String(), strings.TrimSpace(s)) {
			t.Errorf("usage lacks %q", s)
		}
	}
}

func TestCIRoundReviewOnGreenAndCap(t *testing.T) {
	a := project(t, "")
	state := statefulGH(t, a)
	if code, out, _ := ciRound(t, nil, "begin", "7"); code != 0 {
		t.Fatalf("%d: %s", code, out)
	}
	if err := fakegh.SetRollup(state, "OPEN", a, fakegh.Passed("lint")); err != nil {
		t.Fatal(err)
	}
	b := commit(t, "review correction")
	if code, out, _ := ciRound(t, nil, "begin", "7", "--reason", goodReason); code != 12 {
		t.Fatalf("without review: %d: %s", code, out)
	}
	if code, out, errs := ciRound(t, nil, "begin", "7", "--review", "PM requested correcting the misleading comment"); code != 0 {
		t.Fatalf("review: %d: %s%s", code, out, errs)
	}
	if code, out, _ := ciRound(t, nil, "status", "7"); code != 10 || !strings.Contains(out, "2/2 used (1 failure, 1 review)") {
		t.Fatalf("status: %d: %s", code, out)
	}
	if err := fakegh.SetRollup(state, "OPEN", b, fakegh.Passed("lint")); err != nil {
		t.Fatal(err)
	}
	commit(t, "third review")
	if code, out, _ := ciRound(t, nil, "begin", "7", "--review", "PM requested another correction"); code != 10 {
		t.Fatalf("cap: %d: %s", code, out)
	}
	cs, err := fakegh.Comments(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cs[1].Body, `"kind":"review"`) {
		t.Fatalf("review ledger: %s", cs[1].Body)
	}
}

func TestCIRoundUnrecordedPushAndExplicitReset(t *testing.T) {
	for _, reader := range []string{"begin", "status"} {
		t.Run(reader, func(t *testing.T) {
			a := project(t, "")
			state := statefulGH(t, a)
			if code, out, _ := ciRound(t, nil, "begin", "7"); code != 0 {
				t.Fatalf("%d: %s", code, out)
			}
			b := commit(t, "push without begin")
			if err := fakegh.SetRollup(state, "OPEN", b, fakegh.Failed("lint")); err != nil {
				t.Fatal(err)
			}
			commit(t, "next attempt")
			if code, out, _ := ciRound(t, nil, reader, "7", "--reason", goodReason); code != 10 {
				t.Fatalf("unrecorded: %d: %s", code, out)
			}
			if code, out, _ := ciRound(t, nil, "begin", "7", "--reason", goodReason); code != 10 {
				t.Fatalf("cap: %d: %s", code, out)
			}
			if code, out, _ := ciRound(t, nil, "reset", "7"); code != 11 {
				t.Fatalf("missing reason: %d: %s", code, out)
			}
			if code, out, errs := ciRound(t, nil, "reset", "7", "--reason", "Operator investigated and approved a fresh budget"); code != 0 {
				t.Fatalf("reset: %d: %s%s", code, out, errs)
			}
			if code, out, _ := ciRound(t, nil, "begin", "7"); code != 0 || !strings.Contains(out, "round 1 of 2") {
				t.Fatalf("fresh: %d: %s", code, out)
			}
			cs, err := fakegh.Comments(state)
			if err != nil {
				t.Fatal(err)
			}
			var unrecorded, resets int
			for _, c := range cs {
				if strings.Contains(c.Body, `"kind":"unrecorded"`) {
					unrecorded++
				}
				if strings.Contains(c.Body, `"kind":"reset"`) {
					resets++
				}
			}
			if unrecorded != 1 || resets != 1 {
				t.Fatalf("ledger: %d unrecorded, %d resets", unrecorded, resets)
			}
		})
	}
}
