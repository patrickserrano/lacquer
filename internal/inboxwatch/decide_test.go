package inboxwatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/decisions"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

// ghLog is what a scripted gh saw, reads and writes in one order, so a test can
// assert the sequence and not only the set.
type scriptedGH struct {
	mu    sync.Mutex
	log   []string
	reads map[string]string   // by "arg arg arg", the reply
	out   map[string]string   // writes: the stdout, by argv
	errs  map[string]error    // by argv, for a read or a write
	hang  map[string]bool     // a write that never finishes on its own
	seq   map[string][]string // reads that answer differently each time, in order
}

func (g *scriptedGH) read(_ context.Context, args ...string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := strings.Join(args, " ")
	g.log = append(g.log, "READ  gh "+key)
	if err := g.errs[key]; err != nil {
		return nil, err
	}
	if q := g.seq[key]; len(q) > 0 {
		g.seq[key] = q[1:]
		return []byte(q[0]), nil
	}
	out, ok := g.reads[key]
	if !ok {
		return nil, fmt.Errorf("unscripted read: gh %s", key)
	}
	return []byte(out), nil
}

func (g *scriptedGH) Run(stdin, name string, args ...string) (string, error) {
	return g.RunContext(context.Background(), stdin, name, args...)
}

func (g *scriptedGH) RunContext(ctx context.Context, stdin, name string, args ...string) (string, error) {
	g.mu.Lock()
	key := strings.Join(args, " ")
	g.log = append(g.log, fmt.Sprintf("WRITE %s %s  <<< %q", name, key, stdin))
	err, hang := g.errs[key], g.hang[key]
	out := g.out[key]
	g.mu.Unlock()
	if hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return out, err
}

func (g *scriptedGH) lines() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.log...)
}

func (g *scriptedGH) writes() []string {
	var w []string
	for _, l := range g.lines() {
		if strings.HasPrefix(l, "WRITE") {
			w = append(w, l)
		}
	}
	return w
}

const (
	repoList   = "issue list -R o/r --label decisions --state open --json number,title,url --limit 100"
	fleetList  = "issue list -R o/fleet --label decisions --state open --json number,title,url --limit 100"
	labelList  = "label list -R o/r --search decisions --json name --limit 100"
	labelMake  = "label create decisions -R o/r --description " + decisions.LabelDescription + " --color " + decisions.LabelColor
	issueMake  = "issue create -R o/r --title Decisions --body-file - --label decisions"
	commentOn7 = "issue comment 7 -R o/r --body-file -"
	commentOn9 = "issue comment 9 -R o/r --body-file -"
	oneOpen    = `[{"number":7,"title":"Decisions","url":"https://github.com/o/r/issues/7"}]`
)

// flat is a screen with its wrapping undone: runs of whitespace, newlines
// included, as one space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

var decAt = time.Date(2026, 9, 25, 4, 10, 0, 0, time.UTC)

func decEnv(g *scriptedGH) Env {
	return Env{
		Cmd:        g,
		Run:        g.read,
		Now:        func() time.Time { return decAt },
		ExtraRepos: []string{"o/r", "o/fleet"},
		FleetRepo:  "o/fleet",
	}
}

func decideCmd(text string) Cmd {
	return Cmd{Kind: CmdDecide, ID: "a1", Text: text, Repo: "o/r", Ref: "https://github.com/o/r/issues/5"}
}

func wantBody(text, basis, from string) string {
	return decisions.Body(decisions.Record{Text: text, Basis: basis, From: from, At: decAt})
}

// First use: the label, then the issue, then the comment, in that order, with
// the exact argv and stdin of each. This is the fixture a real gh run must match.
func TestFirstUseCreatesTheLabelThenTheIssueThenComments(t *testing.T) {
	g := &scriptedGH{
		reads: map[string]string{repoList: `[]`, labelList: `[]`},
		out: map[string]string{
			issueMake:  "https://github.com/o/r/issues/9\n",
			commentOn9: "https://github.com/o/r/issues/9#issuecomment-4242\n",
		},
	}
	ev := decEnv(g).Exec(decideCmd("keep iOS 26 as the minimum")).(DecidedEvent)
	if !ev.OK || ev.URL != "https://github.com/o/r/issues/9#issuecomment-4242" {
		t.Fatalf("ev = %+v", ev)
	}
	want := []string{
		"READ  gh " + repoList,
		"READ  gh " + labelList,
		fmt.Sprintf("WRITE gh %s  <<< %q", labelMake, ""),
		fmt.Sprintf("WRITE gh %s  <<< %q", issueMake, decisions.IssueBody),
		"READ  gh " + repoList, // looked at again: two popups can race on a first use
		fmt.Sprintf("WRITE gh %s  <<< %q", commentOn9, wantBody("keep iOS 26 as the minimum", "", "https://github.com/o/r/issues/5")),
	}
	if got := g.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("gh calls:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range []string{"recorded in o/r#9", "https://github.com/o/r/issues/9#issuecomment-4242", "created the decisions label", "created issue o/r#9"} {
		if !strings.Contains(ev.Note, s) {
			t.Errorf("note lacks %q: %s", s, ev.Note)
		}
	}
}

// A repository that already has the label is not asked to make it again.
func TestFirstUseWithTheLabelAlreadyThereOnlyCreatesTheIssue(t *testing.T) {
	g := &scriptedGH{
		reads: map[string]string{repoList: `[]`, labelList: `[{"name":"Decisions"}]`},
		out:   map[string]string{issueMake: "https://github.com/o/r/issues/9\n", commentOn9: "https://github.com/o/r/issues/9#issuecomment-1\n"},
	}
	if ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent); !ev.OK {
		t.Fatalf("ev = %+v", ev)
	}
	for _, w := range g.writes() {
		if strings.Contains(w, "label create") {
			t.Errorf("the label exists, and was created again: %s", w)
		}
	}
	if n := len(g.writes()); n != 2 {
		t.Errorf("want issue create and comment, got %d writes: %v", n, g.writes())
	}
}

// The second use finds the issue and only comments.
func TestSecondUseFindsTheIssueAndOnlyComments(t *testing.T) {
	g := &scriptedGH{
		reads: map[string]string{repoList: oneOpen},
		out:   map[string]string{commentOn7: "https://github.com/o/r/issues/7#issuecomment-8\n"},
	}
	ev := decEnv(g).Exec(decideCmd("second")).(DecidedEvent)
	if !ev.OK {
		t.Fatalf("ev = %+v", ev)
	}
	want := []string{
		"READ  gh " + repoList,
		fmt.Sprintf("WRITE gh %s  <<< %q", commentOn7, wantBody("second", "", "https://github.com/o/r/issues/5")),
	}
	if got := g.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("gh calls:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTwoOpenDecisionsIssuesAreRefusedAndNamed(t *testing.T) {
	g := &scriptedGH{reads: map[string]string{repoList: `[{"number":9,"title":"Decisions","url":"u"},{"number":3,"title":"Decisions (old)","url":"u"}]`}}
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || !strings.Contains(ev.Note, "o/r#3, o/r#9") || !strings.Contains(ev.Note, "NOT recorded") || !strings.Contains(ev.Note, "Nothing was posted") {
		t.Errorf("ev = %+v", ev)
	}
	if w := g.writes(); len(w) != 0 {
		t.Errorf("wrote with two candidates: %v", w)
	}
}

// The repository an agent's ref names does not get to be where the operator's
// words go: every scope is checked against the roster and the extras first, and a
// refusal makes no gh call at all, a read included, and no issue, no label.
func TestAnUnknownRepositoryIsRefusedForBothScopesAndForCreation(t *testing.T) {
	for _, repo := range []string{"evil/repo", "o/R2", "o/fleet2"} {
		g := &scriptedGH{reads: map[string]string{
			"issue list -R " + repo + " --label decisions --state open --json number,title,url --limit 100": `[]`,
		}}
		c := decideCmd("x")
		c.Repo = repo
		ev := decEnv(g).Exec(c).(DecidedEvent)
		if ev.OK || !strings.Contains(ev.Note, repo+" is not in the roster") || !strings.Contains(ev.Note, "Nothing was posted") {
			t.Errorf("%s: ev = %+v", repo, ev)
		}
		if l := g.lines(); len(l) != 0 {
			t.Errorf("%s: gh was called for a repository outside the roster: %v", repo, l)
		}
	}
	// A fleet repository nobody put in the roster or the extras is not special:
	// the target says so, and the popup will not offer it.
	env := decEnv(&scriptedGH{})
	env.ExtraRepos = []string{"o/r"}
	tg := env.DecisionTargets("https://github.com/o/r/issues/5", "")
	if tg.Fleet != "" || !strings.Contains(tg.FleetWhy, "o/fleet is not in the roster") {
		t.Errorf("targets = %+v", tg)
	}
	if tg.Repo != "o/r" {
		t.Errorf("targets = %+v", tg)
	}
	// And forcing it past the popup is still refused by the gate.
	g := &scriptedGH{}
	env.Cmd, env.Run = g, g.read
	c := decideCmd("x")
	c.Repo = "o/fleet"
	if ev := env.Exec(c).(DecidedEvent); ev.OK || len(g.lines()) != 0 {
		t.Errorf("fleet repo outside the roster: %+v, calls %v", ev, g.lines())
	}
	// Also never a write in a repository that is not owner/name.
	for _, bad := range []string{"", "o", "-R/x", "o/r --title", "o/r/x"} {
		c := decideCmd("x")
		c.Repo = bad
		g := &scriptedGH{}
		e2 := decEnv(g)
		e2.ExtraRepos = append(e2.ExtraRepos, bad)
		if ev := e2.Exec(c).(DecidedEvent); ev.OK || len(g.lines()) != 0 {
			t.Errorf("%q: %+v calls %v", bad, ev, g.lines())
		}
	}
}

// The comment carries the operator's words and nothing agent-written, and the
// words survive every character.
func TestTheCommentIsVerbatimForTrickyTextAndHoldsNoEntryText(t *testing.T) {
	for name, text := range map[string]string{
		"backticks":  "use `swift build`, not ```xcodebuild```",
		"quote":      "> not a quote\n> really",
		"issue link": "see #123 and o/other#9",
		"mention":    "@octocat should see this, and @org/team",
		"crlf":       "one\r\ntwo\r\n",
		"emoji":      "ship 🚢 é 日本語",
		"padding":    "  spaced  ",
	} {
		g := &scriptedGH{reads: map[string]string{repoList: oneOpen}, out: map[string]string{commentOn7: "https://github.com/o/r/issues/7#issuecomment-1\n"}}
		if ev := decEnv(g).Exec(decideCmd(text)).(DecidedEvent); !ev.OK {
			t.Fatalf("%s: %+v", name, ev)
		}
		w := g.writes()
		if len(w) != 1 {
			t.Fatalf("%s: writes %v", name, w)
		}
		body := w[0][strings.Index(w[0], "<<< ")+4:]
		var stdin string
		if _, err := fmt.Sscanf(body, "%q", &stdin); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rec, ok := decisions.Parse(stdin)
		if !ok || rec.Text != text {
			t.Errorf("%s: the words did not survive:\n got %q\nwant %q\nbody:\n%s", name, rec.Text, text, stdin)
		}
		if stdin != wantBody(text, "", "https://github.com/o/r/issues/5") {
			t.Errorf("%s: comment is not the documented form:\n%q", name, stdin)
		}
	}
}

func TestTheBasisIsOptional(t *testing.T) {
	for _, basis := range []string{"", "79% of devices on iOS 26 as of June 2026"} {
		g := &scriptedGH{reads: map[string]string{repoList: oneOpen}, out: map[string]string{commentOn7: "u\n"}}
		c := decideCmd("keep iOS 26")
		c.Basis = basis
		if ev := decEnv(g).Exec(c).(DecidedEvent); !ev.OK {
			t.Fatalf("%q: %+v", basis, ev)
		}
		stdin := g.writes()[0]
		if got := strings.Contains(stdin, "Basis:"); got != (basis != "") {
			t.Errorf("basis %q: Basis line present = %v\n%s", basis, got, stdin)
		}
		if basis != "" && !strings.Contains(stdin, basis) {
			t.Errorf("the basis is missing: %s", stdin)
		}
	}
}

// A link to a repository the operator does not watch is never made, and the ref
// is never copied as it was written.
func TestTheCommentLinksOnlyARefInARepositoryTheOperatorWatches(t *testing.T) {
	for ref, want := range map[string]string{
		"https://github.com/o/r/issues/5":    "from https://github.com/o/r/issues/5",
		"o/r#6":                              "from https://github.com/o/r/issues/6",
		"https://github.com/o/r/pull/7":      "from https://github.com/o/r/pull/7",
		"https://github.com/evil/x/issues/1": "from " + decisions.NoLink,
		"session:abc123":                     "from " + decisions.NoLink,
		"":                                   "from " + decisions.NoLink,
		"https://evil.example/x [click](http://y)": "from " + decisions.NoLink,
	} {
		g := &scriptedGH{reads: map[string]string{repoList: oneOpen}, out: map[string]string{commentOn7: "u\n"}}
		c := decideCmd("x")
		c.Ref = ref
		if ev := decEnv(g).Exec(c).(DecidedEvent); !ev.OK {
			t.Fatalf("%q: %+v", ref, ev)
		}
		stdin := g.writes()[0]
		if !strings.Contains(stdin, want+`\n`) {
			t.Errorf("ref %q: comment lacks %q:\n%s", ref, want, stdin)
		}
		if strings.Contains(stdin, "evil") {
			t.Errorf("ref %q: an unwatched repository was linked:\n%s", ref, stdin)
		}
	}
}

// Every step that can fail says what was done and what was not.
func TestAGhFailureAtEachStepSaysWhatWasAndWasNotDone(t *testing.T) {
	ok := func() *scriptedGH {
		return &scriptedGH{
			reads: map[string]string{repoList: `[]`, labelList: `[]`},
			out:   map[string]string{issueMake: "https://github.com/o/r/issues/9\n", commentOn9: "u\n"},
			errs:  map[string]error{},
		}
	}
	for _, tc := range []struct {
		name   string
		fail   string
		writes int // writes attempted, the failing one included
		want   []string
	}{
		{"the lookup", repoList, 0, []string{"NOT recorded in o/r", "could not look up the decisions issue", "HTTP 500", "Nothing was posted"}},
		{"the label lookup", labelList, 0, []string{"NOT recorded", "could not look up the decisions label", "HTTP 500", "Nothing was posted"}},
		{"the label", labelMake, 1, []string{"NOT recorded", "could not create the decisions label", "HTTP 500", "Nothing was posted"}},
		{"the issue", issueMake, 2, []string{"NOT recorded", "could not create the decisions issue", "HTTP 500", "Already done: created the decisions label", "retrying will pick up from there"}},
		{"the comment", commentOn9, 3, []string{"NOT recorded", "posting the comment on o/r#9 failed", "HTTP 500", "Already done: created the decisions label; created issue o/r#9"}},
	} {
		g := ok()
		g.errs[tc.fail] = errors.New("HTTP 500")
		ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
		if ev.OK || ev.Unsure || ev.URL != "" {
			t.Errorf("%s: ev = %+v", tc.name, ev)
		}
		for _, w := range tc.want {
			if !strings.Contains(ev.Note, w) {
				t.Errorf("%s: note lacks %q: %s", tc.name, w, ev.Note)
			}
		}
		if n := len(g.writes()); n != tc.writes {
			t.Errorf("%s: %d writes, want %d: %v", tc.name, n, tc.writes, g.writes())
		}
	}
	// gh prints something that is not the issue it was asked to make: not trusted as where to post.
	g := ok()
	g.out[issueMake] = "https://github.com/evil/x/issues/1\n"
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || !strings.Contains(ev.Note, "did not say which") || len(g.writes()) != 2 {
		t.Errorf("ev = %+v writes %v", ev, g.writes())
	}
}

// A comment that timed out may have posted: the operator is told to look, and
// the popup will not offer a retry that could record it twice.
func TestATimedOutWriteIsReportedAsUnsure(t *testing.T) {
	old := writeBackTimeout
	writeBackTimeout = 20 * time.Millisecond
	defer func() { writeBackTimeout = old }()

	g := &scriptedGH{reads: map[string]string{repoList: oneOpen}, hang: map[string]bool{commentOn7: true}}
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || !ev.Unsure || !strings.Contains(ev.Note, "may or may not have posted") {
		t.Errorf("comment timeout: %+v", ev)
	}
	g = &scriptedGH{reads: map[string]string{repoList: `[]`, labelList: `[]`}, hang: map[string]bool{issueMake: true}, out: map[string]string{}}
	ev = decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || !ev.Unsure || !strings.Contains(ev.Note, "create the decisions issue") || !strings.Contains(ev.Note, "NOT posted") {
		t.Errorf("issue timeout: %+v", ev)
	}
}

// Nothing typed, nothing recorded.
func TestBlankTextIsNeverRecorded(t *testing.T) {
	g := &scriptedGH{}
	if ev := decEnv(g).Exec(decideCmd("  \n ")).(DecidedEvent); ev.OK || len(g.lines()) != 0 {
		t.Errorf("ev %+v calls %v", ev, g.lines())
	}
}

// ---- where a decision may go ----

func TestDecisionTargets(t *testing.T) {
	env := decEnv(&scriptedGH{})
	env.Roster = fleet.Roster{Project: []fleet.Entry{{Name: "Widgets", Repo: "o/r"}, {Name: "local"}}}
	env.ExtraRepos = []string{"o/fleet"}
	for name, tc := range map[string]struct {
		ref, project string
		repo         string
		whyHas       string
	}{
		"a ref in the roster":                {"https://github.com/o/r/issues/5", "", "o/r", ""},
		"a PR ref in the roster":             {"o/r#5", "nothing", "o/r", ""},
		"the ref wins over the project":      {"o/fleet#5", "Widgets", "o/fleet", ""},
		"an unknown ref, a mapped project":   {"https://github.com/evil/x/issues/1", "widgets", "o/r", ""},
		"a session ref, a mapped project":    {"session:abc", "Widgets", "o/r", ""},
		"an unknown ref, no project":         {"https://github.com/evil/x/issues/1", "", "", "evil/x, which is not in the roster or the extras"},
		"a session ref, an unmapped project": {"session:abc", "local", "", `its project "local" maps to no repository`},
		"nothing at all":                     {"", "", "", "the item names no project"},
	} {
		got := env.DecisionTargets(tc.ref, tc.project)
		if got.Repo != tc.repo || !strings.Contains(got.RepoWhy, tc.whyHas) || (tc.repo != "" && got.RepoWhy != "") || (tc.repo == "" && got.RepoWhy == "") {
			t.Errorf("%s: %+v", name, got)
		}
		if got.Fleet != "o/fleet" || got.FleetWhy != "" {
			t.Errorf("%s: fleet %+v", name, got)
		}
	}
	env.FleetRepo = ""
	if got := env.DecisionTargets("o/r#1", ""); got.Fleet != "" || !strings.Contains(got.FleetWhy, "no fleet repository") {
		t.Errorf("no fleet repo: %+v", got)
	}
	env.FleetRepo = "--bad"
	if got := env.DecisionTargets("o/r#1", ""); got.Fleet != "" || got.FleetWhy == "" {
		t.Errorf("a bad fleet repo: %+v", got)
	}
}

// ---- the popup ----

// decDetail is a popup that has read an entry, wired to env's targets.
func decDetail(t *testing.T, env Env, e inbox.Entry) Program {
	t.Helper()
	d := loadedDetail(t, false, e) // no overseer: recording a decision does not need one
	d.Targets = env.DecisionTargets
	return d
}

var decEntry = inbox.Entry{ID: "a1", Type: inbox.Action, Title: "AGENT TITLE do not record", Body: "AGENT BODY do not record",
	Ref: "https://github.com/o/r/issues/5", Project: "widgets"}

func TestTheDecisionKeyRecordsWordsBasisAndScopeAndPostsNothingElse(t *testing.T) {
	env := decEnv(&scriptedGH{})
	var p Program = decDetail(t, env, decEntry)

	p, cmds := feed(t, p, "D")
	if len(cmds) != 0 {
		t.Fatalf("D asked for %v before anything was typed", cmds)
	}
	if got := plainAll(p.View()); !strings.Contains(got, "record as a decision, in your own words") {
		t.Errorf("no words prompt:\n%s", got)
	}
	// The words are kept as typed, spaces and all: this is not the reply box, which trims.
	p, cmds = feed(t, p, "  keep iOS 26, `not` #123 @x  \r")
	if len(cmds) != 0 {
		t.Fatalf("Enter on the words asked for %v; the basis and the scope are still to come", cmds)
	}
	if got := plainAll(p.View()); !strings.Contains(got, "basis, optional") {
		t.Errorf("no basis prompt:\n%s", got)
	}
	p, cmds = feed(t, p, "79% on iOS 26, June 2026\r")
	if len(cmds) != 0 {
		t.Fatalf("Enter on the basis asked for %v", cmds)
	}
	// The scope prompt shows the exact repository each key would post to.
	scope := plainAll(p.View())
	for _, want := range []string{"r  this repo: o/r", "f  fleet-wide: o/fleet", "`decisions` issue? (made first if there is none)"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope prompt lacks %q:\n%s", want, scope)
		}
	}
	if !strings.Contains(plain(p.View().Lines[19]), "r this repo") || !strings.Contains(plain(p.View().Lines[19]), "Esc cancel, nothing is posted") {
		t.Errorf("hint row: %q", plain(p.View().Lines[19]))
	}

	q, cmds := feed(t, p, "r")
	if len(cmds) != 1 || cmds[0].Kind != CmdDecide {
		t.Fatalf("r = %v", cmds)
	}
	c := cmds[0]
	if c.Text != "  keep iOS 26, `not` #123 @x  " || c.Basis != "79% on iOS 26, June 2026" || c.Repo != "o/r" || c.Ref != decEntry.Ref || c.ID != "a1" {
		t.Errorf("cmd = %+v", c)
	}
	// Nothing agent-written travels with it.
	if s := fmt.Sprintf("%+v", c); strings.Contains(s, "AGENT") {
		t.Errorf("entry text reached the command: %s", s)
	}
	// No reply went to the overseer, and no second key press repeats it.
	if _, again := feed(t, q, "r"); len(again) != 0 {
		t.Errorf("a key while sending asked for %v", again)
	}

	// f goes to the fleet repository.
	if _, cmds := feed(t, p, "f"); len(cmds) != 1 || cmds[0].Repo != "o/fleet" {
		t.Errorf("f = %v", cmds)
	}
}

func TestEscAtEachPromptPostsNothing(t *testing.T) {
	env := decEnv(&scriptedGH{})
	for name, keys := range map[string]string{
		"at the words":         "Dsome words\x1b",
		"at the empty words":   "D\x1b",
		"at the basis":         "Dwords\rbasis\x1b",
		"at the empty basis":   "Dwords\r\x1b",
		"at the scope":         "Dwords\rbasis\r\x1b",
		"ctrl-c at the scope":  "Dwords\r\r\x03",
		"Enter on blank words": "D   \r",
	} {
		p, cmds := feed(t, decDetail(t, env, decEntry), keys)
		if len(cmds) != 0 {
			t.Errorf("%s: asked for %v", name, cmds)
		}
		d := p.(Detail)
		if d.Done() || d.dec.step != decOff || !strings.Contains(plain(d.View().Lines[19]), "decision cancelled; nothing was posted") {
			t.Errorf("%s: step %v, bar %q", name, d.dec.step, plain(d.View().Lines[19]))
		}
		// And nothing is left over for the next one.
		if _, cmds := feed(t, p, "r"); len(cmds) != 0 {
			t.Errorf("%s: a later key posted %v", name, cmds)
		}
	}
	// Esc outside a prompt still closes the popup, as before.
	if q, _ := feed(t, decDetail(t, env, decEntry), "\x1b"); !q.Done() {
		t.Error("Esc no longer closes the popup")
	}
}

// r or f on a scope that cannot be used says why and posts nothing; the other one still works.
func TestAnUnavailableScopeSaysWhyAndTheOtherStillWorks(t *testing.T) {
	env := decEnv(&scriptedGH{})
	sessionEntry := inbox.Entry{ID: "s1", Type: inbox.Action, Title: "t", Ref: "session:abc"}
	p, cmds := feed(t, decDetail(t, env, sessionEntry), "Dwords\r\r")
	scope := flat(plainAll(p.View()))
	if len(cmds) != 0 || !strings.Contains(scope, "r this repo is unavailable: the item's ref is not an issue or pull request; the item names no project") ||
		!strings.Contains(scope, "f fleet-wide: o/fleet") {
		t.Fatalf("scope prompt:\n%s", scope)
	}
	q, cmds := feed(t, p, "r")
	if len(cmds) != 0 || !strings.Contains(plain(q.View().Lines[19]), "this repo is unavailable") {
		t.Errorf("r on an unavailable scope: %v %q", cmds, plain(q.View().Lines[19]))
	}
	if _, cmds := feed(t, q, "f"); len(cmds) != 1 || cmds[0].Repo != "o/fleet" {
		t.Errorf("f = %v", cmds)
	}

	// With the fleet repository out of reach, only this repo is offered.
	noFleet := decEnv(&scriptedGH{})
	noFleet.ExtraRepos = []string{"o/r"}
	p, _ = feed(t, decDetail(t, noFleet, decEntry), "Dwords\r\r")
	if got := flat(plainAll(p.View())); !strings.Contains(got, "f fleet-wide is unavailable: o/fleet is not in the roster or the extra repositories") {
		t.Errorf("scope prompt:\n%s", got)
	}
	if q, cmds := feed(t, p, "f"); len(cmds) != 0 || !strings.Contains(plain(q.View().Lines[19]), "fleet-wide is unavailable") {
		t.Errorf("f with no fleet repo: %v", cmds)
	}

	// With neither, the key refuses up front and no box opens.
	none := decEnv(&scriptedGH{})
	none.ExtraRepos, none.FleetRepo = nil, ""
	q, cmds = feed(t, decDetail(t, none, sessionEntry), "D")
	if d := q.(Detail); d.dec.step != decOff || len(cmds) != 0 || !strings.Contains(plain(d.View().Lines[19]), "cannot record a decision") {
		t.Errorf("no targets: step %v bar %q", d.dec.step, plain(d.View().Lines[19]))
	}
	// A popup with no targets configured says so.
	bare := loadedDetail(t, false, decEntry)
	if q, _ := feed(t, bare, "D"); !strings.Contains(plain(q.View().Lines[19]), "not configured") {
		t.Errorf("no Targets: %q", plain(q.View().Lines[19]))
	}
}

// After a success the popup confirms with the comment's URL; after a failure it
// says what was and was not done and keeps the words for a retry.
func TestThePopupConfirmsWithTheURLAndKeepsTheWordsAfterAFailure(t *testing.T) {
	env := decEnv(&scriptedGH{})
	p, _ := feed(t, decDetail(t, env, decEntry), "Dkeep it\r\rr")

	ok, _ := p.Update(DecidedEvent{OK: true, URL: "https://github.com/o/r/issues/7#issuecomment-8", Note: "recorded in o/r#7: https://github.com/o/r/issues/7#issuecomment-8"})
	got := plainAll(ok.View())
	if ok.Done() || !strings.Contains(got, "recorded in o/r#7: https://github.com/o/r/issues/7#issuecomment-8") {
		t.Errorf("confirmation:\n%s", got)
	}
	// The next key clears it, and a fresh D starts clean.
	if q, _ := feed(t, ok, "j"); strings.Contains(plainAll(q.View()), "recorded in") {
		t.Error("the confirmation stayed after a key")
	}

	bad, cmds := p.Update(DecidedEvent{Note: "NOT recorded in o/r: could not create the decisions issue: HTTP 500. Already done: created the decisions label."})
	if len(cmds) != 0 {
		t.Fatalf("a failure must not retry by itself: %v", cmds)
	}
	got = flat(plainAll(bad.View()))
	if !strings.Contains(got, "NOT recorded in o/r") || !strings.Contains(got, "Already done: created the decisions label") || !strings.Contains(got, "r this repo: o/r") {
		t.Errorf("failure:\n%s", got)
	}
	// Back at the scope prompt with the same words: r retries them.
	if _, cmds := feed(t, bad, "r"); len(cmds) != 1 || cmds[0].Text != "keep it" {
		t.Errorf("retry = %v", cmds)
	}

	// A timeout is not offered a retry: it may already have posted.
	unsure, _ := p.Update(DecidedEvent{Unsure: true, Note: "gh timed out; the comment may or may not have posted"})
	if d := unsure.(Detail); d.dec.step != decOff || !strings.Contains(plainAll(unsure.View()), "may or may not have posted") {
		t.Errorf("unsure: step %v\n%s", d.dec.step, plainAll(unsure.View()))
	}
	if _, cmds := feed(t, unsure, "r"); len(cmds) != 0 {
		t.Errorf("r after a timeout posted %v", cmds)
	}
}

// An ordinary reply stays a reply: r still types to the overseer and never
// records anything, and the decision key documents itself in the hint row.
func TestAnOrdinaryReplyStaysAReplyAndTheHintNamesTheKey(t *testing.T) {
	env := decEnv(&scriptedGH{})
	d := loadedDetail(t, true, decEntry)
	d.Targets = env.DecisionTargets
	p, cmds := feed(t, d, "rhello\r")
	if len(cmds) != 1 || cmds[0].Kind != CmdReply || cmds[0].Text != "hello" {
		t.Errorf("reply = %v", cmds)
	}
	if got := plain(decDetail(t, env, decEntry).View().Lines[19]); !strings.Contains(got, "D record decision") {
		t.Errorf("hint row does not name the key: %q", got)
	}
	_ = p
}

// A decision goes to GitHub only: the overseer is never typed into.
func TestARecordedDecisionSendsNothingToTheOverseer(t *testing.T) {
	g := &scriptedGH{reads: map[string]string{repoList: oneOpen}, out: map[string]string{commentOn7: "https://github.com/o/r/issues/7#issuecomment-1\n"}}
	env := decEnv(g)
	env.Overseer = Overseer{Pane: "%7"}
	env.InboxPath = writeInbox(t)
	if ev := env.Exec(decideCmd("x")).(DecidedEvent); !ev.OK {
		t.Fatalf("%+v", ev)
	}
	for _, l := range g.lines() {
		if strings.Contains(l, "tmux") || strings.Contains(l, "send-keys") {
			t.Errorf("a decision typed into the overseer: %s", l)
		}
	}
	if _, err := ReadReplies(RepliesPath(env.InboxPath)); err != nil {
		t.Fatal(err)
	}
	if r, _ := ReadReplies(RepliesPath(env.InboxPath)); len(r) != 0 {
		t.Errorf("a decision was written to the replies file: %v", r)
	}
}

// The reply's own gate is the same function, and still refuses an unknown repository.
func TestTheReplyCommentStillGoesThroughTheSameGate(t *testing.T) {
	g := &scriptedGH{}
	env := decEnv(g)
	if _, err := env.WriteBack("https://github.com/evil/x/issues/1", "x"); err == nil || !strings.Contains(err.Error(), "evil/x is not in the roster") || len(g.lines()) != 0 {
		t.Errorf("err %v calls %v", err, g.lines())
	}
	if _, err := env.WriteBack("https://github.com/o/r/issues/1", "x"); err != nil || len(g.writes()) != 1 {
		t.Errorf("err %v calls %v", err, g.lines())
	}
}

// Each write asks the gate itself, whatever its caller did first: a write added
// later, a label or an issue, cannot be made to a repository outside the roster.
func TestEveryGitHubWriteAsksTheGateItself(t *testing.T) {
	g := &scriptedGH{}
	env := decEnv(g)
	for _, args := range [][]string{
		{"label", "create", "decisions", "-R", "evil/x"},
		{"issue", "create", "-R", "evil/x", "--title", "Decisions"},
		{"issue", "comment", "1", "-R", "evil/x", "--body-file", "-"},
	} {
		if _, err := env.write("evil/x", "body", args...); err == nil || !strings.Contains(err.Error(), "evil/x is not in the roster") {
			t.Errorf("gh %v: err %v", args, err)
		}
	}
	if _, _, err := env.postGated("https://github.com/evil/x/issues/1", "body"); err == nil {
		t.Error("postGated wrote to an unknown repository")
	}
	if l := g.lines(); len(l) != 0 {
		t.Errorf("gh ran for a repository outside the roster: %v", l)
	}
	// And one inside it goes through.
	if _, err := env.write("o/r", "", "label", "create", "decisions", "-R", "o/r"); err != nil || len(g.writes()) != 1 {
		t.Errorf("err %v writes %v", err, g.writes())
	}
}

// GitHub refuses a comment over 65536 characters. That is known before anything
// is written, so a first use must not create the label and the issue and only then
// fail, leaving them behind and failing every retry the same way.
func TestAnOverLengthDecisionIsRefusedBeforeAnyWrite(t *testing.T) {
	g := &scriptedGH{reads: map[string]string{repoList: `[]`, labelList: `[]`}}
	ev := decEnv(g).Exec(decideCmd(strings.Repeat("x", 70000))).(DecidedEvent)
	if ev.OK || !strings.Contains(ev.Note, "too long by") || !strings.Contains(ev.Note, "Nothing was posted") {
		t.Errorf("ev = %+v", ev)
	}
	if l := g.lines(); len(l) != 0 {
		t.Errorf("gh was called for a comment that cannot be posted: %v", l)
	}
	// The limit is on the whole comment, header and fences included, in characters
	// (not bytes): text just under it goes through, and 3-byte characters count once.
	fits := strings.Repeat("é", 65000)
	g = &scriptedGH{reads: map[string]string{repoList: oneOpen}, out: map[string]string{commentOn7: "u\n"}}
	if ev := decEnv(g).Exec(decideCmd(fits)).(DecidedEvent); !ev.OK {
		t.Errorf("65000 characters refused: %+v", ev)
	}
	over := maxCommentChars - len([]rune(wantBody("", "", "https://github.com/o/r/issues/5"))) + 1
	g = &scriptedGH{reads: map[string]string{repoList: oneOpen}}
	if ev := decEnv(g).Exec(decideCmd(strings.Repeat("y", over))).(DecidedEvent); ev.OK || !strings.Contains(ev.Note, "too long by 1 characters") {
		t.Errorf("one over the limit: %+v", ev)
	}
}

// A write that times out after an earlier one finished still says the earlier
// one did.
func TestATimeoutStillNamesWhatWasAlreadyDone(t *testing.T) {
	old := writeBackTimeout
	writeBackTimeout = 20 * time.Millisecond
	defer func() { writeBackTimeout = old }()
	g := &scriptedGH{reads: map[string]string{repoList: `[]`, labelList: `[]`}, hang: map[string]bool{issueMake: true}, out: map[string]string{}}
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if !ev.Unsure || !strings.Contains(ev.Note, "Already done: created the decisions label") {
		t.Errorf("issue timeout: %+v", ev)
	}
	g = &scriptedGH{reads: map[string]string{repoList: `[]`, labelList: `[]`}, hang: map[string]bool{commentOn9: true},
		out: map[string]string{issueMake: "https://github.com/o/r/issues/9\n"}}
	ev = decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if !ev.Unsure || !strings.Contains(ev.Note, "created the decisions label; created issue o/r#9") {
		t.Errorf("comment timeout: %+v", ev)
	}
}

// Two popups racing on a first use each make an issue. After creating one, look
// again, and post to neither if there are two.
func TestAFirstUseRacingAnotherPopupPostsToNeither(t *testing.T) {
	g := &scriptedGH{
		reads: map[string]string{labelList: `[]`},
		seq:   map[string][]string{repoList: {`[]`, `[{"number":8,"title":"Decisions","url":"u"},{"number":9,"title":"Decisions","url":"u"}]`}},
		out:   map[string]string{issueMake: "https://github.com/o/r/issues/9\n"},
	}
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || !strings.Contains(ev.Note, "o/r#8, o/r#9") || !strings.Contains(ev.Note, "created issue o/r#9") || !strings.Contains(ev.Note, "not posted") {
		t.Errorf("ev = %+v", ev)
	}
	for _, w := range g.writes() {
		if strings.Contains(w, "issue comment") {
			t.Errorf("posted despite two issues: %s", w)
		}
	}
	// And a re-check that cannot be made does not post either.
	g = &scriptedGH{reads: map[string]string{labelList: `[]`}, seq: map[string][]string{repoList: {`[]`}}, out: map[string]string{issueMake: "https://github.com/o/r/issues/9\n"}}
	if ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent); ev.OK || !strings.Contains(ev.Note, "could not re-check") || len(g.writes()) != 2 {
		t.Errorf("ev = %+v writes %v", ev, g.writes())
	}
}

// Real gh writes ZERO BYTES, not `[]`, for `label list --search` when nothing
// matches (measured, `| od -c` prints nothing). The fixtures above answer `[]`,
// a result that cannot fail; this one answers what gh does. The operator's first
// D in a repository with no decisions label failed on it.
func TestFirstUseWithRealGhsEmptyLabelListCreatesTheLabel(t *testing.T) {
	g := &scriptedGH{
		reads: map[string]string{repoList: `[]`, labelList: ""},
		out: map[string]string{
			issueMake:  "https://github.com/o/r/issues/9\n",
			commentOn9: "https://github.com/o/r/issues/9#issuecomment-4242\n",
		},
	}
	ev := decEnv(g).Exec(decideCmd("keep iOS 26 as the minimum")).(DecidedEvent)
	if !ev.OK || ev.URL != "https://github.com/o/r/issues/9#issuecomment-4242" {
		t.Fatalf("ev = %+v", ev)
	}
	want := []string{
		"READ  gh " + repoList,
		"READ  gh " + labelList,
		fmt.Sprintf("WRITE gh %s  <<< %q", labelMake, ""),
		fmt.Sprintf("WRITE gh %s  <<< %q", issueMake, decisions.IssueBody),
		"READ  gh " + repoList,
		fmt.Sprintf("WRITE gh %s  <<< %q", commentOn9, wantBody("keep iOS 26 as the minimum", "", "https://github.com/o/r/issues/5")),
	}
	if got := g.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("gh calls:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Malformed output is still an error, and nothing is written.
func TestFirstUseWithGarbageLabelListWritesNothing(t *testing.T) {
	g := &scriptedGH{reads: map[string]string{repoList: `[]`, labelList: "<html>rate limited</html>"}}
	ev := decEnv(g).Exec(decideCmd("x")).(DecidedEvent)
	if ev.OK || len(g.writes()) != 0 {
		t.Errorf("ev = %+v, writes = %v", ev, g.writes())
	}
}
