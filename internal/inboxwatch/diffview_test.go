package inboxwatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

var (
	sha1 = strings.Repeat("a", 40)
	sha2 = strings.Repeat("b", 40)
)

const (
	viewArgs = "pr view 12 -R o/r --json files,headRefOid"
	diffArgs = "pr diff 12 -R o/r --color=never"
)

// diffGH is a fake gh that records every call and answers by argv.
type diffGH struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
	fail  map[string]error
}

func (g *diffGH) run(_ context.Context, args ...string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := strings.Join(args, " ")
	g.calls = append(g.calls, k)
	if err := g.fail[k]; err != nil {
		return nil, err
	}
	out, ok := g.out[k]
	if !ok {
		return nil, fmt.Errorf("fake gh: no answer for %q", k)
	}
	return []byte(out), nil
}

func (g *diffGH) n(sub string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if strings.HasPrefix(c, sub) {
			n++
		}
	}
	return n
}

func viewJSON(sha string) string {
	return `{"headRefOid":"` + sha + `","files":[{"path":"src/a.go","additions":3,"deletions":1},{"path":"gone.txt","additions":0,"deletions":2}]}`
}

const sampleDiff = `diff --git a/src/a.go b/src/a.go
index 111..222 100644
--- a/src/a.go
+++ b/src/a.go
@@ -10,3 +10,4 @@ func f() {
 keep
-old
+new
+++ b/not/a/header
 tail
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 333..000
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-x
-y
`

func newGH(sha, diff string) *diffGH {
	return &diffGH{out: map[string]string{viewArgs: viewJSON(sha), diffArgs: diff}}
}

func diffEnv(t *testing.T, gh *diffGH, cmd Commander) Env {
	t.Helper()
	return Env{
		InboxPath:  filepath.Join(t.TempDir(), "inbox.jsonl"),
		Run:        gh.run,
		Cmd:        cmd,
		ExtraRepos: []string{"o/r"},
		Diffs:      &DiffMemo{},
		Now:        func() time.Time { return decAt },
		Overseer:   Overseer{Pane: "%1"},
	}
}

func cacheFiles(t *testing.T, e Env) []string {
	t.Helper()
	ents, err := os.ReadDir(e.diffDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, en := range ents {
		out = append(out, en.Name())
	}
	return out
}

func TestParseDiffNumbersAndKinds(t *testing.T) {
	pd := parseDiff(sampleDiff)
	if len(pd.Files) != 2 {
		t.Fatalf("files = %v", pd.Files)
	}
	find := func(text string) diffLine {
		for _, l := range pd.Lines {
			if l.Text == text {
				return l
			}
		}
		t.Fatalf("no line %q", text)
		return diffLine{}
	}
	type want struct {
		kind     diffKind
		path     string
		old, new int
	}
	for text, w := range map[string]want{
		" keep":                            {dkCtx, "src/a.go", 10, 10},
		"-old":                             {dkDel, "src/a.go", 11, 0},
		"+new":                             {dkAdd, "src/a.go", 0, 11},
		"+++ b/not/a/header":               {dkAdd, "src/a.go", 0, 12}, // an added line, not a file header
		" tail":                            {dkCtx, "src/a.go", 12, 13},
		"-x":                               {dkDel, "gone.txt", 1, 0},
		"-y":                               {dkDel, "gone.txt", 2, 0},
		"diff --git a/gone.txt b/gone.txt": {dkFile, "gone.txt", 0, 0},
		"@@ -10,3 +10,4 @@ func f() {":     {dkHunk, "src/a.go", 0, 0},
	} {
		g := find(text)
		if g.Kind != w.kind || g.Path != w.path || g.Old != w.old || g.New != w.new {
			t.Errorf("%q parsed as %+v, want %+v", text, g, w)
		}
	}
	// A deleted file's path survives "+++ /dev/null".
	if l := find("+++ /dev/null"); l.Path != "gone.txt" || l.Kind != dkMeta {
		t.Errorf("/dev/null header: %+v", l)
	}
	// A hunk header without counts means one line each.
	one := parseDiff("diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -3 +3 @@\n-a\n+b\n")
	if l := one.Lines[len(one.Lines)-1]; l.Kind != dkAdd || l.New != 3 {
		t.Errorf("count-less hunk: %+v", one.Lines)
	}
}

func openDiff(t *testing.T, e Env, ref string) (Program, DiffEvent) {
	t.Helper()
	d := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "TITLE-SECRET", Body: "BODY-SECRET", Ref: ref})
	p, cmds := feed(t, d, "v")
	if len(cmds) != 1 || cmds[0].Kind != CmdDiff || cmds[0].Ref != ref {
		t.Fatalf("v must ask for the diff: %v", cmds)
	}
	ev := e.Exec(cmds[0]).(DiffEvent)
	p, _ = p.Update(ev)
	return p, ev
}

// moveTo presses j until the diff view's cursor is on the line whose text is text.
func moveTo(t *testing.T, p Program, text string) Program {
	t.Helper()
	p, _ = feed(t, p, "g")
	for i := 0; i < 500; i++ {
		v := p.(Detail).ov.(*diffView)
		if v.lines[v.cur].Text == text {
			return p
		}
		p, _ = feed(t, p, "j")
	}
	t.Fatalf("no line %q", text)
	return p
}

func TestDiffViewColoursAndFiles(t *testing.T) {
	p, ev := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	if ev.Err != "" {
		t.Fatal(ev.Err)
	}
	p, _ = p.Update(ResizeEvent{W: 100, H: 40}) // tall enough that the file list and the diff share the screen
	all := plainAll(p.View())
	for _, want := range []string{"DIFF  o/r#12  head aaaaaaa  ·  2 files  +3 −3", "src/a.go  +3 −1", "gone.txt  +0 −2"} {
		if !strings.Contains(all, want) {
			t.Errorf("view lacks %q:\n%s", want, all)
		}
	}
	// Park the cursor on the last line so no row of interest is highlighted, then
	// read the colour each kind is drawn in.
	p = moveTo(t, p, "-y")
	f := p.View()
	for needle, want := range map[string]string{
		"src/a.go  +3":   "\x1b[0;90;49m",   // the file list: dim
		"+new":           "\x1b[0;32;49m",   // green
		"-old":           "\x1b[0;31;49m",   // red
		"@@ -10,3":       "\x1b[0;90;49m",   // dim
		"diff --git a/s": "\x1b[0;1;39;49m", // bold
		"index 111":      "\x1b[0;90;49m",   // meta is dim
		" keep":          "\x1b[0;39;49m",   // context is plain
	} {
		if got := styleOf(t, rowOf(f, needle), needle); got != want {
			t.Errorf("%q drawn %q, want %q", needle, got, want)
		}
	}
	// The line the cursor is on is highlighted.
	if row := rowOf(f, "-y"); !strings.Contains(row, "48;5;236") {
		t.Errorf("the cursor's row is not highlighted: %q", row)
	}
}

func TestFilesAreJumpedBetweenWithNAndP(t *testing.T) {
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	cur := func() string { v := p.(Detail).ov.(*diffView); return v.lines[v.cur].Text }
	if cur() != "diff --git a/src/a.go b/src/a.go" {
		t.Fatalf("opens on the first file: %q", cur())
	}
	p, _ = feed(t, p, "n")
	if cur() != "diff --git a/gone.txt b/gone.txt" {
		t.Errorf("n: %q", cur())
	}
	p, _ = feed(t, p, "n")
	if !strings.Contains(plainAll(p.View()), "last file") || cur() != "diff --git a/gone.txt b/gone.txt" {
		t.Errorf("n at the last file stays: %q", cur())
	}
	p, _ = feed(t, p, "p")
	if cur() != "diff --git a/src/a.go b/src/a.go" {
		t.Errorf("p: %q", cur())
	}
}

func TestSameHeadIsFetchedOnce(t *testing.T) {
	gh := newGH(sha1, sampleDiff)
	e := diffEnv(t, gh, &fakeCmd{})
	for i := 0; i < 2; i++ {
		if ev := e.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"}).(DiffEvent); ev.Err != "" || ev.Reused != (i == 1) {
			t.Fatalf("open %d: %+v", i, ev)
		}
	}
	// Each open asks which commit the PR is at; only the first fetches the diff.
	if gh.n("pr diff") != 1 || gh.n("pr view") != 2 {
		t.Errorf("calls = %v", gh.calls)
	}
}

func TestANewHeadRefetchesAndPrunesTheOldCacheFile(t *testing.T) {
	gh := newGH(sha1, sampleDiff)
	e := diffEnv(t, gh, &fakeCmd{})
	e.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"})
	// Other PRs' files, and one whose repo name ends like this PR's prefix, stay.
	dir := e.diffDir()
	keep := []string{"o_r_13_" + sha1 + ".diff", "o_r_1_" + sha1 + ".diff", "o_r_12_" + sha1[:39] + ".txt"}
	for _, k := range keep {
		if err := os.WriteFile(filepath.Join(dir, k), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gh.out[viewArgs] = viewJSON(sha2)
	gh.out[diffArgs] = "diff --git a/n b/n\n"
	if ev := e.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"}).(DiffEvent); ev.Reused || ev.Head != sha2 {
		t.Fatalf("a new head must refetch: %+v", ev)
	}
	if gh.n("pr diff") != 2 {
		t.Errorf("calls = %v", gh.calls)
	}
	got := strings.Join(cacheFiles(t, e), " ")
	if strings.Contains(got, "o_r_12_"+sha1+".diff") || !strings.Contains(got, "o_r_12_"+sha2+".diff") {
		t.Errorf("only the newest head is kept: %s", got)
	}
	for _, k := range keep {
		if !strings.Contains(got, k) {
			t.Errorf("pruning took %s: %s", k, got)
		}
	}
}

func TestAFailureIsNeverAnEmptyDiff(t *testing.T) {
	for name, mut := range map[string]func(g *diffGH){
		"view fails": func(g *diffGH) { g.fail[viewArgs] = errors.New("HTTP 502") },
		"diff fails": func(g *diffGH) { g.fail[diffArgs] = errors.New("HTTP 502") },
		"bad json":   func(g *diffGH) { g.out[viewArgs] = "<html>" },
		"no head":    func(g *diffGH) { g.out[viewArgs] = `{"headRefOid":"","files":[]}` },
	} {
		gh := newGH(sha1, sampleDiff)
		gh.fail = map[string]error{}
		mut(gh)
		e := diffEnv(t, gh, &fakeCmd{})
		p, ev := openDiff(t, e, "o/r#12")
		if !strings.HasPrefix(ev.Err, "couldn't load the diff: ") || ev.Text != "" {
			t.Errorf("%s: %+v", name, ev)
		}
		if all := plainAll(p.View()); !strings.Contains(all, "couldn't load the diff") || strings.Contains(all, "empty") {
			t.Errorf("%s: view:\n%s", name, all)
		}
		if n := cacheFiles(t, e); len(n) != 0 {
			t.Errorf("%s: a failed fetch wrote %v", name, n)
		}
		if _, cmds := feed(t, p, "r"); len(cmds) != 0 {
			t.Errorf("%s: r on a failed load asked for %v", name, cmds)
		}
	}
	// A real empty diff is said to be empty, and is a different message.
	e := diffEnv(t, newGH(sha1, ""), &fakeCmd{})
	p, ev := openDiff(t, e, "o/r#12")
	if ev.Err != "" || !strings.Contains(plainAll(p.View()), "diff is empty") {
		t.Errorf("empty diff: %+v\n%s", ev, plainAll(p.View()))
	}
}

func TestAPROutsideTheRosterIsNotFetched(t *testing.T) {
	gh := newGH(sha1, sampleDiff)
	e := diffEnv(t, gh, &fakeCmd{})
	e.ExtraRepos = []string{"o/other"}
	p, ev := openDiff(t, e, "o/r#12")
	if !strings.Contains(ev.Err, "not in the roster") || len(gh.calls) != 0 || len(cacheFiles(t, e)) != 0 {
		t.Errorf("ev = %+v, calls = %v", ev, gh.calls)
	}
	if !strings.Contains(plainAll(p.View()), "not in the roster") {
		t.Errorf("the view must say so:\n%s", plainAll(p.View()))
	}
	// An issue URL is not a PR, and nothing is fetched for it either.
	if ev := e.Exec(Cmd{Kind: CmdDiff, Ref: "https://github.com/o/other/issues/3"}).(DiffEvent); ev.Err == "" || len(gh.calls) != 0 {
		t.Errorf("issue url: %+v %v", ev, gh.calls)
	}
}

func TestNoViewKeyWithoutAPRorFileRef(t *testing.T) {
	for _, ref := range []string{"", "session:abc", "https://github.com/o/r/issues/3", "https://example.com/x"} {
		d := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: ref})
		p, cmds := feed(t, d, "v")
		if len(cmds) != 0 || !strings.Contains(plainAll(p.View()), "no diff or plan on this item") {
			t.Errorf("ref %q: %v\n%s", ref, cmds, plainAll(p.View()))
		}
		if strings.Contains(plainAll(d.View()), "v ") {
			t.Errorf("ref %q: the hint offers a view it cannot open", ref)
		}
	}
	d := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "o/r#12"})
	if got := plainAll(d.View()); !strings.Contains(got, "v diff") {
		t.Errorf("a PR ref's hint offers the diff:\n%s", got)
	}
	d = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "file:~/x.md"})
	if got := plainAll(d.View()); !strings.Contains(got, "v plan") {
		t.Errorf("a file: ref's hint offers the plan:\n%s", got)
	}
}

func TestRefreshAndTicksNeverFetchTheDiff(t *testing.T) {
	d := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "o/r#12"})
	p, cmds := feed(t, d, "v")
	if count(cmds, CmdDiff) != 1 {
		t.Fatal(cmds)
	}
	for i := 0; i < 5; i++ {
		var c []Cmd
		p, c = p.Update(TickEvent{Now: t0.Add(time.Duration(i+2) * 10 * time.Minute)})
		if len(c) != 0 {
			t.Fatalf("a tick asked for %v", c)
		}
	}
	_, c := p.Update(ResizeEvent{W: 100, H: 30})
	if len(c) != 0 {
		t.Fatalf("a resize asked for %v", c)
	}
}

func TestControlCharactersAreSanitisedInThePopupAndTheCache(t *testing.T) {
	nasty := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-x\n+ev\x1b[?1049l\x1b]52;c;AAAA\x07il\tTAB\r\n"
	gh := newGH(sha1, nasty)
	e := diffEnv(t, gh, &fakeCmd{})
	p, _ := openDiff(t, e, "o/r#12")
	for _, l := range p.View().Lines {
		// Only the renderer's own sequences may appear: SGR ("m"), never a
		// private-mode or OSC sequence from the diff.
		if strings.Contains(strings.ReplaceAll(l, "\x1b[K", ""), "\x1b[?") || strings.Contains(l, "\x1b]") || strings.ContainsRune(l, 7) {
			t.Fatalf("a row carries the diff's escape: %q", l)
		}
	}
	p, _ = feed(t, p, "G")
	if all := plainAll(p.View()); !strings.Contains(all, "^[[?1049l") || !strings.Contains(all, "^M") || !strings.Contains(all, "TAB") {
		t.Errorf("the popup shows the controls as ^X:\n%s", all)
	}
	b, err := os.ReadFile(filepath.Join(e.diffDir(), DiffCacheName(GitHubRef{Repo: "o/r", Number: 12}, sha1)))
	if err != nil {
		t.Fatal(err)
	}
	want := "+ev^[[?1049l^[]52;c;AAAA^Gil\tTAB^M\n"
	if !strings.HasSuffix(string(b), want) || strings.ContainsAny(string(b), "\x1b\x07\r") {
		t.Errorf("cache = %q", b)
	}
	// A tab and a newline survive.
	if !strings.Contains(string(b), "\t") || strings.Count(string(b), "\n") != 6 {
		t.Errorf("tabs and newlines are kept: %q", b)
	}
}

func TestCacheHoldsOnlyTheDiffAndIsNamedByTheAgreedFormat(t *testing.T) {
	gh := newGH(sha1, sampleDiff)
	e := diffEnv(t, gh, &fakeCmd{})
	openDiff(t, e, "o/r#12") // the entry's title is TITLE-SECRET and its body BODY-SECRET
	names := cacheFiles(t, e)
	if len(names) != 1 || names[0] != "o_r_12_"+sha1+".diff" {
		t.Fatalf("files = %v", names)
	}
	b, _ := os.ReadFile(filepath.Join(e.diffDir(), names[0]))
	if string(b) != sampleDiff {
		t.Errorf("the file is the diff and nothing else:\n%s", b)
	}
	if strings.Contains(string(b), "SECRET") {
		t.Error("agent-written entry text reached the cache")
	}
	if fi, _ := os.Stat(filepath.Join(e.diffDir(), names[0])); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}

func TestCacheAndPopupTruncationMarkers(t *testing.T) {
	line := "+" + strings.Repeat("x", 78) + "\n"
	big := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -0,0 +1,20000 @@\n" + strings.Repeat(line, 20000) // 1.6 MB
	gh := newGH(sha1, big)
	e := diffEnv(t, gh, &fakeCmd{})
	p, ev := openDiff(t, e, "o/r#12")
	if !ev.Cut || len(ev.Text) > maxDiffShown {
		t.Errorf("the popup's text is capped: cut=%v len=%d", ev.Cut, len(ev.Text))
	}
	p, _ = feed(t, p, "G")
	if all := plainAll(p.View()); !strings.Contains(all, "truncated") {
		t.Errorf("the popup says it is cut:\n%s", all)
	}
	b, _ := os.ReadFile(filepath.Join(e.diffDir(), DiffCacheName(GitHubRef{Repo: "o/r", Number: 12}, sha1)))
	if len(b) > maxDiffCache || !strings.HasSuffix(string(b), "\n"+DiffCacheMarker+"\n") {
		t.Fatalf("cache: len=%d tail=%q", len(b), b[max(len(b)-80, 0):])
	}
	if strings.Count(string(b), DiffCacheMarker) != 1 {
		t.Error("the marker is one line")
	}
	body := strings.TrimSuffix(string(b), DiffCacheMarker+"\n")
	if !strings.HasPrefix(big, body) || !strings.HasSuffix(body, "\n") {
		t.Error("what precedes the marker is a whole-line prefix of the diff")
	}
	// A diff under the cap has no marker.
	e2 := diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{})
	e2.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"})
	small, _ := os.ReadFile(filepath.Join(e2.diffDir(), DiffCacheName(GitHubRef{Repo: "o/r", Number: 12}, sha1)))
	if strings.Contains(string(small), "lacquer:") {
		t.Error("a whole diff carries no marker")
	}
}

// --- not this line -------------------------------------------------------

func TestTheHeaderNamesPathAndLineForAddedRemovedAndContextLines(t *testing.T) {
	find := func(text string) Cmd {
		t.Helper()
		// The view is shared by every copy of a Detail, so each search gets its own.
		p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
		q := moveTo(t, p, text)
		q, _ = feed(t, q, "r")
		q, cmds := feed(t, q, "not this\r")
		if len(cmds) != 1 || cmds[0].Kind != CmdLine {
			t.Fatalf("%q: %v\n%s", text, cmds, plainAll(q.View()))
		}
		return cmds[0]
	}
	for text, w := range map[string]struct {
		line int
		side string
	}{"+new": {11, "new"}, "-old": {11, "old"}, " keep": {10, "new"}, "-y": {2, "old"}} {
		c := find(text)
		if c.Path == "" || c.Line != w.line || c.Side != w.side || c.Text != "not this" {
			t.Errorf("%q → %+v, want line %d %s", text, c, w.line, w.side)
		}
	}
}

func TestOnlyDiffLinesCanBeAnswered(t *testing.T) {
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	// The cursor starts on the first file's "diff --git" line.
	q, cmds := feed(t, p, "r")
	if len(cmds) != 0 || !strings.Contains(plainAll(q.View()), "move to a +, - or context line") {
		t.Errorf("a file header cannot be answered:\n%s", plainAll(q.View()))
	}
	if strings.Contains(plainAll(q.View()), "not this line →") {
		t.Error("no box opened")
	}
}

func TestTheTargetIsShownBeforeEnter(t *testing.T) {
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	p = moveTo(t, p, "+new")
	p, _ = feed(t, p, "r")
	got := flat(plainAll(p.View()))
	if !strings.Contains(got, "not this line → o/r#12 src/a.go:") || !strings.Contains(got, "file line)") || !strings.Contains(got, "comments on the PR, and tells the overseer") {
		t.Errorf("the target is on screen while typing:\n%s", got)
	}
}

func lineCmd() Cmd {
	return Cmd{Kind: CmdLine, ID: "a1", Ref: "o/r#12", Head: sha1, Path: "src/a.go", Line: 11, Side: "new", Text: "no: keep `this`\nsecond $(line)"}
}

func TestNotThisLinePostsAVerbatimPlainCommentOnStdinAndTellsTheOverseer(t *testing.T) {
	c := &fakeCmd{out: map[string]string{"gh pr": "https://github.com/o/r/pull/12#issuecomment-9\n"}}
	e := diffEnv(t, newGH(sha1, ""), c)
	ev := e.Exec(lineCmd()).(LineSentEvent)
	if !ev.Commented || !ev.OverseerSent || ev.Nothing() || !strings.Contains(ev.Comment, "issuecomment-9") {
		t.Fatalf("ev = %+v", ev)
	}
	var comment, sent = -1, -1
	for i, call := range c.calls {
		switch {
		case strings.HasPrefix(call, "gh "):
			comment = i
		case strings.HasPrefix(call, "tmux send-keys -t %1 -l"):
			sent = i
		}
	}
	if comment < 0 || c.calls[comment] != "gh pr comment 12 -R o/r --body-file -" {
		t.Fatalf("calls = %q", c.calls)
	}
	wantBody := "**From the operator's inbox** (lacquer inbox watch, 2026-09-25T04:10:00Z):\n\n" +
		"Not this line: `src/a.go` (line 11 of the new file, at aaaaaaa)\n\n" +
		"no: keep `this`\nsecond $(line)\n"
	if c.stdin[comment] != wantBody {
		t.Errorf("stdin = %q\nwant    %q", c.stdin[comment], wantBody)
	}
	if want := "tmux send-keys -t %1 -l [inbox a1] src/a.go:11 no: keep `this`\nsecond $(line)"; sent < 0 || c.calls[sent] != want {
		t.Errorf("overseer got %q, want %q", c.calls, want)
	}
	// Only one comment: the reply's own write-back is not also made.
	n := 0
	for _, call := range c.calls {
		if strings.HasPrefix(call, "gh ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d comments: %q", n, c.calls)
	}
}

func TestARemovedLineIsSaidToBeTheOldFilesLine(t *testing.T) {
	c := &fakeCmd{}
	e := diffEnv(t, newGH(sha1, ""), c)
	cmd := lineCmd()
	cmd.Side, cmd.Line, cmd.Text = "old", 7, "why"
	e.Exec(cmd)
	if body := c.stdin[0+indexOf(c.calls, "gh ")]; !strings.Contains(body, "(line 7 of the old file, a removed line, at aaaaaaa)") {
		t.Errorf("body = %q", body)
	}
}

func indexOf(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestTheCommentIsGatedToTheRoster(t *testing.T) {
	c := &fakeCmd{}
	e := diffEnv(t, newGH(sha1, ""), c)
	e.ExtraRepos = []string{"o/other"}
	ev := e.Exec(lineCmd()).(LineSentEvent)
	if ev.Commented || indexOf(c.calls, "gh ") >= 0 || !strings.Contains(ev.Comment, "not in the roster") {
		t.Errorf("an unknown repository was posted to: %+v %q", ev, c.calls)
	}
	// The overseer half is independent of it.
	if !ev.OverseerSent {
		t.Errorf("the overseer send does not depend on the gate: %+v", ev)
	}
	bad := lineCmd()
	bad.Ref = "session:abc"
	if ev := e.Exec(bad).(LineSentEvent); ev.Commented || indexOf(c.calls, "gh ") >= 0 {
		t.Errorf("a non-PR ref: %+v", ev)
	}
}

func TestEitherHalfCanFailWithoutTheOther(t *testing.T) {
	// The comment fails; the overseer has it.
	c := &fakeCmd{fail: map[string]error{"gh pr": errors.New("HTTP 403")}}
	ev := diffEnv(t, newGH(sha1, ""), c).Exec(lineCmd()).(LineSentEvent)
	if ev.Commented || !ev.OverseerSent || ev.Nothing() || !strings.Contains(ev.Comment, "NOT posted: HTTP 403") || !strings.Contains(ev.Overseer, "sent to the overseer") {
		t.Errorf("comment down: %+v", ev)
	}
	// The overseer fails; the comment is up.
	c = &fakeCmd{fail: map[string]error{"tmux send-keys": errors.New("no such pane")}}
	ev = diffEnv(t, newGH(sha1, ""), c).Exec(lineCmd()).(LineSentEvent)
	if !ev.Commented || ev.OverseerSent || ev.Nothing() || !strings.Contains(ev.Overseer, "NOT sent to the overseer: tmux send-keys: no such pane") {
		t.Errorf("overseer down: %+v", ev)
	}
	// No overseer configured: the comment still goes, and the note says why.
	c = &fakeCmd{}
	e := diffEnv(t, newGH(sha1, ""), c)
	e.Overseer = Overseer{}
	ev = e.Exec(lineCmd()).(LineSentEvent)
	if !ev.Commented || ev.OverseerSent || !strings.Contains(ev.Overseer, "NOT sent") {
		t.Errorf("no overseer: %+v", ev)
	}
	// Both fail: nothing went out, so the words may be retried.
	c = &fakeCmd{fail: map[string]error{"gh pr": errors.New("x"), "tmux send-keys": errors.New("y")}}
	if ev = diffEnv(t, newGH(sha1, ""), c).Exec(lineCmd()).(LineSentEvent); !ev.Nothing() {
		t.Errorf("both down: %+v", ev)
	}
}

func TestTheViewReportsWhatWasAndWasNotDoneAndKeepsWordsOnlyWhenNothingWent(t *testing.T) {
	open := func() Program {
		p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
		p = moveTo(t, p, "+new")
		p, _ = feed(t, p, "r")
		p, cmds := feed(t, p, "my words\r")
		if len(cmds) != 1 || cmds[0].Kind != CmdLine {
			t.Fatalf("%v", cmds)
		}
		return p
	}
	p, _ := open().Update(LineSentEvent{OverseerSent: true, Overseer: "sent to the overseer as [inbox a1] f:1 …", Comment: "NOT posted: HTTP 403"})
	got := flat(plainAll(p.View()))
	if !strings.Contains(got, "comment: NOT posted: HTTP 403") || !strings.Contains(got, "overseer: sent to the overseer") || strings.Contains(got, "> my words") {
		t.Errorf("one half done:\n%s", got)
	}
	p, _ = open().Update(LineSentEvent{Overseer: "NOT sent to the overseer: no pane", Comment: "NOT posted: HTTP 403"})
	if got := flat(plainAll(p.View())); !strings.Contains(got, "> my words") || !strings.Contains(got, "NOT sent") {
		t.Errorf("nothing went, so the words come back:\n%s", got)
	}
}

func TestSendingRefreshesTheEntryAndBlocksKeys(t *testing.T) {
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	p = moveTo(t, p, "+new")
	p, _ = feed(t, p, "r")
	p, _ = feed(t, p, "words\r")
	q, cmds := feed(t, p, "jjjq")
	if len(cmds) != 0 || !strings.Contains(plainAll(q.View()), "DIFF  o/r#12") {
		t.Errorf("keys while sending are ignored, the view stays up: %v\n%s", cmds, plainAll(q.View()))
	}
	_, cmds = p.Update(LineSentEvent{OverseerSent: true, Commented: true})
	if len(cmds) != 1 || cmds[0].Kind != CmdEntry {
		t.Errorf("the entry is re-read after the overseer has a reply: %v", cmds)
	}
}

func TestQGivesTheDetailBack(t *testing.T) {
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{}), "o/r#12")
	p, _ = feed(t, p, "q")
	if p.Done() || !strings.Contains(plainAll(p.View()), "TITLE-SECRET") {
		t.Errorf("q closes the diff, not the popup:\n%s", plainAll(p.View()))
	}
	p, _ = feed(t, p, "q")
	if !p.Done() {
		t.Error("a second q closes the popup")
	}
}

// --- plan view -----------------------------------------------------------

func planEnv(t *testing.T) (Env, string) {
	t.Helper()
	home := t.TempDir()
	return Env{Home: home}, home
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRefusals(t *testing.T) {
	e, home := planEnv(t)
	outside := t.TempDir()
	dev := filepath.Join(home, "Developer")
	write(t, filepath.Join(outside, "secret.txt"), "TOP-SECRET")
	write(t, filepath.Join(home, ".ssh", "id_rsa"), "TOP-SECRET")
	write(t, filepath.Join(home, ".config", "op", "x"), "TOP-SECRET")
	write(t, filepath.Join(home, ".netrc"), "TOP-SECRET")
	write(t, filepath.Join(home, ".claude", ".credentials.json"), "TOP-SECRET")
	write(t, filepath.Join(home, ".claude", "settings.json"), "TOP-SECRET")
	write(t, filepath.Join(home, "Library", "Keychains", "k"), "TOP-SECRET")
	write(t, filepath.Join(home, "Documents", "taxes.md"), "TOP-SECRET")
	write(t, filepath.Join(home, "Developer-evil", "x.md"), "TOP-SECRET") // looks like a root, is not one
	write(t, filepath.Join(dev, "x", ".ssh", "id"), "TOP-SECRET")
	write(t, filepath.Join(dev, "x", ".env"), "TOP-SECRET")
	write(t, filepath.Join(dev, "x", ".git", "config"), "TOP-SECRET")
	write(t, filepath.Join(dev, "ok", "plan.md"), "fine")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(outside, filepath.Join(dev, "escape")))                                  // a link out of the roots
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dev, "ok", "leaf"))) // a leaf link out
	must(os.Symlink(filepath.Join(home, "Documents"), filepath.Join(dev, "docs")))           // a link to $HOME outside the roots
	must(os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(dev, "notes")))               // a link into a dot-dir
	// APFS is case-insensitive: this reaches ~/Library under another spelling.
	must(os.Symlink(filepath.Join(home, "library", "Keychains"), filepath.Join(dev, "lnk")))
	must(os.Mkdir(filepath.Join(dev, "adir"), 0o755))
	must(syscall.Mkfifo(filepath.Join(dev, "fifo"), 0o644))

	for name, ref := range map[string]string{
		"outside $HOME":          "file:" + filepath.Join(outside, "secret.txt"),
		"dotdot out of the root": "file:" + dev + "/ok/../../Documents/taxes.md",
		"symlink dir escaping":   "file:" + filepath.Join(dev, "escape", "secret.txt"),
		"symlink leaf escaping":  "file:" + filepath.Join(dev, "ok", "leaf"),
		"symlink to Documents":   "file:" + filepath.Join(dev, "docs", "taxes.md"),
		"symlink into dot-dir":   "file:" + filepath.Join(dev, "notes", "id_rsa"),
		"symlink to lowercase":   "file:~/Developer/lnk/k",
		"dot-dir .ssh":           "file:~/.ssh/id_rsa",
		"dot-dir .config/op":     "file:~/.config/op/x",
		"dot-file leaf":          "file:~/.netrc",
		".claude credentials":    "file:~/.claude/.credentials.json",
		".claude settings":       "file:~/.claude/settings.json",
		"~/Library":              "file:~/Library/Keychains/k",
		"~/library":              "file:~/library/Keychains/k",
		"~/LIBRARY":              "file:~/LIBRARY/Keychains/k",
		"~/Documents":            "file:~/Documents/taxes.md",
		"a lookalike root":       "file:~/Developer-evil/x.md",
		"dot-dir under a root":   "file:~/Developer/x/.ssh/id",
		"dot-file under a root":  "file:~/Developer/x/.env",
		".git under a root":      "file:~/Developer/x/.git/config",
		"a directory":            "file:~/Developer/adir",
		"a fifo":                 "file:~/Developer/fifo",
		"a root itself":          "file:~/Developer",
		"home itself":            "file:~",
		"a relative path":        "file:Developer/ok/plan.md",
		"empty":                  "file:",
		"missing":                "file:~/Developer/nope.md",
	} {
		ev := e.Exec(Cmd{Kind: CmdPlan, Ref: ref}).(PlanEvent)
		if ev.Err == "" || ev.Text != "" || strings.Contains(ev.Err+ev.Text, "TOP-SECRET") {
			t.Errorf("%s: %q was not refused: %+v", name, ref, ev)
		}
	}
	if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Documents/taxes.md"}).(PlanEvent); !strings.Contains(ev.Err, "outside the plan roots") {
		t.Errorf("the refusal says why: %q", ev.Err)
	}
	if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Developer/ok/plan.md"}).(PlanEvent); ev.Err != "" || ev.Text != "fine" {
		t.Errorf("the control case is readable: %+v", ev)
	}
}

func TestPlanAllowsOnlyThePlanRootsAndTheFleetsOwnDotDirectories(t *testing.T) {
	e, home := planEnv(t)
	write(t, filepath.Join(home, "Developer", "fleet-ops", "briefs", "b.md"), "brief")
	write(t, filepath.Join(home, "Developer", "harness", ".worktrees", "u.brief.md"), "wt brief")
	write(t, filepath.Join(home, "Developer", "r", ".claude", "worktrees", "w", "plan.md"), "wt plan")
	write(t, filepath.Join(home, ".claude", "plans", "p.md"), "plan")
	write(t, filepath.Join(home, "Developer", "r", ".claude", "settings.local.json"), "no")
	for ref, want := range map[string]string{
		"file:~/Developer/fleet-ops/briefs/b.md":            "brief",
		"file:" + home + "/Developer/fleet-ops/briefs/b.md": "brief",
		"file:~/Developer/harness/.worktrees/u.brief.md":    "wt brief",
		"file:~/Developer/r/.claude/worktrees/w/plan.md":    "wt plan",
		"file:~/.claude/plans/p.md":                         "plan",
		"file:~/Developer/fleet-ops/briefs/../briefs/b.md":  "brief",
	} {
		if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: ref}).(PlanEvent); ev.Err != "" || ev.Text != want {
			t.Errorf("%s: %+v", ref, ev)
		}
	}
	if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Developer/r/.claude/settings.local.json"}).(PlanEvent); ev.Err == "" {
		t.Error("the rest of a project's .claude is not allowed")
	}
	// A symlink under a root to a file in another root is fine: it is the same file.
	if err := os.Symlink(filepath.Join(home, ".claude", "plans", "p.md"), filepath.Join(home, "Developer", "link.md")); err != nil {
		t.Fatal(err)
	}
	if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Developer/link.md"}).(PlanEvent); ev.Err != "" || ev.Text != "plan" {
		t.Errorf("link between roots: %+v", ev)
	}
}

// The check is by inode, so a spelling of a root that differs in case (APFS folds
// it) is the same directory and is allowed. Where the file system is case
// sensitive there is no such spelling to test.
func TestPlanRootsAreMatchedByIdentityNotSpelling(t *testing.T) {
	e, home := planEnv(t)
	write(t, filepath.Join(home, "Developer", "fleet-ops", "briefs", "b.md"), "brief")
	if _, err := os.Stat(filepath.Join(home, "developer", "fleet-ops")); err != nil {
		t.Skip("this file system is case sensitive")
	}
	for _, ref := range []string{"file:~/developer/fleet-ops/briefs/b.md", "file:~/DEVELOPER/fleet-ops/briefs/b.md"} {
		if ev := e.Exec(Cmd{Kind: CmdPlan, Ref: ref}).(PlanEvent); ev.Err != "" || ev.Text != "brief" {
			t.Errorf("%s: %+v", ref, ev)
		}
	}
}

func TestPlanViewIsReadOnlyScrollableAndSanitised(t *testing.T) {
	e, home := planEnv(t)
	body := "# Plan\n\tstep one\x1b[2J\n" + strings.Repeat("line\n", 60) + "END " + strings.Repeat("w", 200) + "\n"
	write(t, filepath.Join(home, "Developer", "p.md"), body)
	d := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "file:~/Developer/p.md"})
	p, cmds := feed(t, d, "v")
	if len(cmds) != 1 || cmds[0].Kind != CmdPlan {
		t.Fatal(cmds)
	}
	p, _ = p.Update(e.Exec(cmds[0]))
	got := plainAll(p.View())
	if !strings.Contains(got, "PLAN  ~/Developer/p.md  (read-only)") || !strings.Contains(got, "    step one^[[2J") {
		t.Errorf("plan:\n%s", got)
	}
	for _, l := range p.View().Lines {
		if strings.Contains(l, "\x1b[2J") {
			t.Fatalf("the file's escape reached the screen: %q", l)
		}
	}
	// r, d and o do nothing here: it is read-only.
	if _, cmds := feed(t, p, "rdodd"); len(cmds) != 0 {
		t.Errorf("keys in the plan view asked for %v", cmds)
	}
	p, _ = feed(t, p, "G")
	end := flat(plainAll(p.View()))
	if !strings.Contains(end, "END "+strings.Repeat("w", 50)) || !strings.Contains(end, strings.Repeat("w", 10)) {
		t.Errorf("a long line is wrapped, never cut:\n%s", end)
	}
	total := 0
	for _, l := range strings.Split(end, " ") {
		total += strings.Count(l, "w")
	}
	if total != 200 {
		t.Errorf("all 200 w's are somewhere on screen, got %d", total)
	}
}

func TestPlanTruncationIsSaid(t *testing.T) {
	e, home := planEnv(t)
	write(t, filepath.Join(home, "Developer", "big.md"), strings.Repeat("0123456789abcdef\n", 100000)) // 1.7 MB
	ev := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Developer/big.md"}).(PlanEvent)
	if !ev.Cut || len(ev.Text) > maxPlanShown || !strings.HasSuffix(ev.Text, "\n") {
		t.Fatalf("cut=%v len=%d", ev.Cut, len(ev.Text))
	}
	v := newPlanView()
	v.event(ev)
	v.scroll(1<<30, 80, 20)
	if got := plainAll(v.view(80, 20)); !strings.Contains(got, PlanTruncated) {
		t.Errorf("the view ends with the marker:\n%s", got)
	}
	write(t, filepath.Join(home, "Developer", "small.md"), "x\n")
	small := e.Exec(Cmd{Kind: CmdPlan, Ref: "file:~/Developer/small.md"}).(PlanEvent)
	if small.Cut {
		t.Error("a small file is not marked cut")
	}
}

// --- review round: the pieces #497's review found -------------------------

func TestCacheNeverExceedsItsCapEvenForOneHugeLine(t *testing.T) {
	e := diffEnv(t, newGH(sha1, strings.Repeat("y", 300000)), &fakeCmd{})
	e.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"})
	b, err := os.ReadFile(filepath.Join(e.diffDir(), DiffCacheName(GitHubRef{Repo: "o/r", Number: 12}, sha1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 256*1024 || !strings.HasSuffix(string(b), "\n"+DiffCacheMarker+"\n") {
		t.Errorf("len = %d (cap %d), tail %q", len(b), 256*1024, b[max(len(b)-60, 0):])
	}
}

const renameDiff = `diff --git a/x b/y.go b/z name.go
similarity index 90%
rename from x b/y.go
rename to z name.go
index 111..222 100644
--- a/x b/y.go
+++ b/z name.go
@@ -1,2 +1,2 @@
 keep
-gone
+here
diff --git "a/q\303\251.txt" "b/q\303\251.txt"
--- "a/q\303\251.txt"
+++ "b/q\303\251.txt"
@@ -1 +1 @@
-was
+is
`

func TestARemovedLineInARenamedFileKeepsTheOldPath(t *testing.T) {
	pd := parseDiff(renameDiff)
	get := func(text string) diffLine {
		for _, l := range pd.Lines {
			if l.Text == text {
				return l
			}
		}
		t.Fatalf("no line %q", text)
		return diffLine{}
	}
	if l := get("-gone"); l.Path != "z name.go" || l.OldPath != "x b/y.go" {
		t.Errorf("removed line: %+v", l)
	}
	if l := get("+here"); l.Path != "z name.go" {
		t.Errorf("added line: %+v", l)
	}
	if p, n, side := get("-gone").target(); p != "x b/y.go" || n != 2 || side != "old" {
		t.Errorf("target of the removed line = %q %d %s", p, n, side)
	}
	if p, n, side := get("+here").target(); p != "z name.go" || n != 2 || side != "new" {
		t.Errorf("target of the added line = %q %d %s", p, n, side)
	}
	// git's quoting is undone, for both names.
	if l := get("-was"); l.OldPath != "q\u00e9.txt" || l.Path != "q\u00e9.txt" {
		t.Errorf("quoted names: %+v", l)
	}
	// And it reaches the command the operator's keypress sends.
	p, _ := openDiff(t, diffEnv(t, newGH(sha1, renameDiff), &fakeCmd{}), "o/r#12")
	p = moveTo(t, p, "-gone")
	p, _ = feed(t, p, "r")
	_, cmds := feed(t, p, "why\r")
	if len(cmds) != 1 || cmds[0].Path != "x b/y.go" || cmds[0].Side != "old" || cmds[0].Line != 2 {
		t.Errorf("cmds = %+v", cmds)
	}
}

func TestOnlyARemovedLineIsMarkedOldForTheOverseer(t *testing.T) {
	for _, tc := range []struct{ side, want string }{
		{"old", "[inbox a1] src/a.go:7 (old) why"},
		{"new", "[inbox a1] src/a.go:7 why"},
	} {
		c := &fakeCmd{}
		cmd := lineCmd()
		cmd.Side, cmd.Line, cmd.Text = tc.side, 7, "why"
		diffEnv(t, newGH(sha1, ""), c).Exec(cmd)
		if got := c.calls[indexOf(c.calls, "tmux send-keys -t %1 -l")]; got != "tmux send-keys -t %1 -l "+tc.want {
			t.Errorf("%s: overseer got %q, want %q", tc.side, got, tc.want)
		}
	}
}

func TestStaleTempFilesAreSweptAndNothingElse(t *testing.T) {
	e := diffEnv(t, newGH(sha1, sampleDiff), &fakeCmd{})
	dir := e.diffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	age := func(name string, by time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-by)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	age(".write-123456.tmp", time.Hour)  // stale: swept
	age(".write-777.tmp", time.Minute)   // in use, maybe: kept
	age(".write-abc.tmp", time.Hour)     // not the pattern CreateTemp makes: kept
	age("notes.tmp", time.Hour)          // not ours: kept
	age(".write-123456.diff", time.Hour) // not ours: kept
	e.Exec(Cmd{Kind: CmdDiff, Ref: "o/r#12"})
	got := strings.Join(cacheFiles(t, e), " ")
	for _, gone := range []string{".write-123456.tmp"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived: %s", gone, got)
		}
	}
	for _, kept := range []string{".write-777.tmp", ".write-abc.tmp", "notes.tmp", ".write-123456.diff", "o_r_12_" + sha1 + ".diff"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%s was removed: %s", kept, got)
		}
	}
}
