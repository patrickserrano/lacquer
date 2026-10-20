package cirounds

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/cirounds/fakegh"
)

// repo is a real temporary git repository shaped like a PR:
//
//	base -- head -- child     (the PR branch; head is the PR's head)
//	  \-- other               (an unrelated commit, as on main)
//
// The test process is moved into it, so Options.Git == nil (real git, the
// production default) sees these commits and nothing else.
type repo struct{ base, head, child, other string }

func newRepo(t *testing.T) repo {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	g := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string) string {
		g("commit", "--allow-empty", "-m", msg)
		return g("rev-parse", "HEAD")
	}
	g("init", "-q", "-b", "main")
	var r repo
	r.base = commit("base")
	r.head = commit("head")
	r.child = commit("child")
	g("checkout", "-q", "-b", "elsewhere", r.base)
	r.other = commit("other")
	t.Chdir(dir)
	return r
}

// realRig is a PR whose head is the repo's head commit, with Options.Git left nil.
func realRig(t *testing.T, head string) (*rig, repo) {
	t.Helper()
	rp := newRepo(t)
	r := newRig(t)
	if head == "" {
		head = rp.head
	}
	r.head(head)
	return r, rp
}

func (r *rig) realOpts(sha string) Options {
	o := r.opts(sha, "")
	o.Git = nil
	return o
}

// refused asserts a refusal that recorded nothing for the refused SHA and that
// names both SHAs, the head branch and the remedy.
func refused(t *testing.T, r *rig, got Result, given string) {
	t.Helper()
	want(t, got, CodeUnavailable)
	for _, c := range r.comments() {
		if strings.Contains(c.Body, given) {
			t.Errorf("a refused SHA was recorded on the PR:\n%s", c.Body)
		}
	}
}

func refusalNamesEverything(t *testing.T, got Result, given, head string) {
	t.Helper()
	for _, s := range []string{given, head, "pr-branch", "run from the PR branch's checkout after committing, or pass --sha <PR head>"} {
		if !strings.Contains(strings.ToLower(got.Text), strings.ToLower(s)) {
			t.Errorf("refusal lacks %q:\n%s", s, got.Text)
		}
	}
}

// Run from the main checkout, whose HEAD is behind the PR branch: the implicit
// HEAD is an ancestor of the PR's head, not the head or a descendant.
func TestWrongCheckoutHEADIsRefused(t *testing.T) {
	r, rp := realRig(t, "")
	got := Begin(context.Background(), r.realOpts(rp.base))
	refused(t, r, got, rp.base)
	refusalNamesEverything(t, got, rp.base, rp.head)
}

func TestShaOfAnUnrelatedCommitIsRefused(t *testing.T) {
	r, rp := realRig(t, "")
	got := Begin(context.Background(), r.realOpts(rp.other))
	refused(t, r, got, rp.other)
	refusalNamesEverything(t, got, rp.other, rp.head)
}

func TestThePRHeadIsAccepted(t *testing.T) {
	r, rp := realRig(t, "")
	got := Begin(context.Background(), r.realOpts(rp.head))
	want(t, got, CodeGranted)
	if got.Round != 1 {
		t.Errorf("round %d, want 1", got.Round)
	}
}

// The commit about to be pushed builds on the head already on the PR: round 2,
// which needs the head's failing check named.
func TestADescendantOfThePRHeadIsAccepted(t *testing.T) {
	r, rp := realRig(t, "")
	r.head(rp.head, fakegh.Failed("lint"))
	want(t, Begin(context.Background(), r.realOpts(rp.head)), CodeGranted)
	o := r.realOpts(rp.child)
	o.Reason = goodReason
	got := Begin(context.Background(), o)
	want(t, got, CodeGranted)
	if got.Round != 2 {
		t.Errorf("round %d, want 2", got.Round)
	}
}

// gh cannot say what the head is, or says nothing usable: fail closed.
func TestUnreadablePRHeadIsRefused(t *testing.T) {
	t.Run("gh fails", func(t *testing.T) {
		r, rp := realRig(t, "")
		o := r.realOpts(rp.child)
		o.Run = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("boom") }
		got := Begin(context.Background(), o)
		refused(t, r, got, rp.child)
	})
	t.Run("empty headRefOid", func(t *testing.T) {
		r, rp := realRig(t, "")
		if err := fakegh.SetRollup(r.dir, "OPEN", ""); err != nil {
			t.Fatal(err)
		}
		got := Begin(context.Background(), r.realOpts(rp.child))
		refused(t, r, got, rp.child)
	})
}

// The PR's head is a commit this checkout has never fetched, so ancestry cannot
// be decided. The tool does not fetch for the user.
func TestPRHeadMissingLocallyIsRefused(t *testing.T) {
	r, rp := realRig(t, sha('f'))
	got := Begin(context.Background(), r.realOpts(rp.child))
	refused(t, r, got, rp.child)
	if !strings.Contains(got.Text, "git fetch") {
		t.Errorf("does not say to fetch:\n%s", got.Text)
	}
}

// A git call that errors (here standing in for a timeout) refuses too.
func TestGitErrorRefuses(t *testing.T) {
	r, rp := realRig(t, "")
	o := r.realOpts(rp.child)
	o.Git = func(context.Context, ...string) ([]byte, error) { return nil, context.DeadlineExceeded }
	refused(t, r, Begin(context.Background(), o), rp.child)
}

// A SHA already recorded as a round still answers "already recorded", even from
// a checkout where it would no longer pass the ancestry test.
func TestRecordedShaStillAnswersAlreadyRecorded(t *testing.T) {
	r, rp := realRig(t, "")
	want(t, Begin(context.Background(), r.realOpts(rp.head)), CodeGranted)
	r.head(rp.child) // the PR moved on; rp.head is no longer its head
	got := Begin(context.Background(), r.realOpts(rp.head))
	want(t, got, CodeGranted)
	if !strings.Contains(got.Text, "already recorded") {
		t.Errorf("not answered as already recorded:\n%s", got.Text)
	}
}

// A refused begin writes NOTHING: on a fresh PR the head is not charged as an
// unrecorded push, so the legitimate begin that follows is still round 1.
func TestRefusalOnAFreshPRRecordsNothing(t *testing.T) {
	r, rp := realRig(t, "")
	refused(t, r, Begin(context.Background(), r.realOpts(rp.base)), rp.base)
	if n := len(r.comments()); n != 0 {
		t.Fatalf("the refused call posted %d ledger comment(s), want none", n)
	}
	got := Begin(context.Background(), r.realOpts(rp.head))
	want(t, got, CodeGranted)
	if got.Round != 1 || got.Spent != 1 {
		t.Errorf("round/spent = %d/%d, want 1/1", got.Round, got.Spent)
	}
	cs := r.comments()
	if len(cs) != 1 || strings.Contains(strings.ToLower(cs[0].Body), "unrecorded") {
		t.Errorf("want exactly the round-1 record and no unrecorded charge, got %d comments", len(cs))
	}
}
