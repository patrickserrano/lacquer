package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 9, 25, 4, 10, 0, 0, time.UTC)

// The operator's words must survive any characters, and come back exactly, and
// the comment must show them as written rather than rendering them.
func TestBodyRoundTripsTheOperatorsWordsExactly(t *testing.T) {
	for name, text := range map[string]string{
		"plain":                      "keep iOS 26 as the minimum",
		"backticks":                  "use `swift build`, not ``xcodebuild``, and ```never``` a fence",
		"long fence":                 "````` five backticks and\n```\nthree on a line of their own\n`````` six",
		"a hash":                     "see #123 and o/r#9 before shipping",
		"a mention":                  "ask @octocat, and @org/team too",
		"a quote":                    "> quoted\n> lines\nnot quoted",
		"crlf":                       "line one\r\nline two\r\n",
		"lone cr":                    "a\rb",
		"emoji":                      "ship it 🚢 — “smart quotes” é ü 日本語",
		"spaces":                     "  leading and trailing   ",
		"blank lines":                "a\n\n\nb\n",
		"markdown":                   "# heading\n* bullet\n_under_ **bold** [link](http://x) <b>html</b> &amp;",
		"a lone fence":               "```",
		"looks like our own comment": "**Decision** — 2026-01-01T00:00:00Z · from x\n\n```text\nfake\n```\n\nBasis:\n\n```text\nfake\n```",
		"a leading newline":          "\nstarts on the second line",
	} {
		t.Run(name, func(t *testing.T) {
			for _, basis := range []string{"", "79% of devices are on iOS 26 (Apple, 2026-06)", text} {
				body := Body(Record{Text: text, Basis: basis, From: "https://github.com/o/r/issues/5", At: at})
				got, ok := Parse(body)
				if !ok {
					t.Fatalf("Parse refused its own body:\n%s", body)
				}
				if got.Text != text || got.Basis != basis {
					t.Errorf("round trip changed the words:\n text  %q -> %q\n basis %q -> %q\nbody:\n%s", text, got.Text, basis, got.Basis, body)
				}
				if got.From != "https://github.com/o/r/issues/5" || !got.At.Equal(at) {
					t.Errorf("head changed: %+v", got)
				}
			}
		})
	}
}

// The comment is fixed in form, and the words appear once, verbatim, inside a
// fence longer than any run of backticks in them.
func TestBodyForm(t *testing.T) {
	got := Body(Record{Text: "keep iOS 26", Basis: "79% on iOS 26, June 2026", From: "https://github.com/o/r/issues/5", At: at})
	want := "**Decision** — 2026-09-25T04:10:00Z · from https://github.com/o/r/issues/5\n\n" +
		"```text\nkeep iOS 26\n```\n\n" +
		"Basis:\n\n```text\n79% on iOS 26, June 2026\n```\n"
	if got != want {
		t.Errorf("body:\n%q\nwant\n%q", got, want)
	}
	// The basis is optional: without one there is no Basis line at all.
	if got := Body(Record{Text: "x", From: NoLink, At: at}); strings.Contains(got, "Basis") {
		t.Errorf("an empty basis was written:\n%s", got)
	}
	// A run of backticks in the words gets a longer fence.
	if got := Body(Record{Text: "a ```` b", From: NoLink, At: at}); !strings.Contains(got, "`````text\na ```` b\n`````\n") {
		t.Errorf("fence not longer than the run:\n%s", got)
	}
	// The time is UTC whatever zone it was given in.
	zoned := at.In(time.FixedZone("x", -7*3600))
	if got := Body(Record{Text: "x", From: NoLink, At: zoned}); !strings.Contains(got, "2026-09-25T04:10:00Z") {
		t.Errorf("not RFC3339 UTC:\n%s", got)
	}
}

// A comment somebody typed into the issue is shown as it is and is never
// mistaken for a decision.
func TestParseRefusesWhatBodyDidNotWrite(t *testing.T) {
	for name, body := range map[string]string{
		"empty":          "",
		"prose":          "we should talk about this",
		"head only":      "**Decision** — 2026-09-25T04:10:00Z · from x\n",
		"bad time":       "**Decision** — yesterday · from x\n\n```text\nw\n```\n",
		"no fence":       "**Decision** — 2026-09-25T04:10:00Z · from x\n\nw\n",
		"unclosed":       "**Decision** — 2026-09-25T04:10:00Z · from x\n\n```text\nw\n",
		"trailing junk":  "**Decision** — 2026-09-25T04:10:00Z · from x\n\n```text\nw\n```\n\nand also this\n",
		"basis no fence": "**Decision** — 2026-09-25T04:10:00Z · from x\n\n```text\nw\n```\n\nBasis: inline\n",
	} {
		if r, ok := Parse(body); ok {
			t.Errorf("%s: parsed as %+v", name, r)
		}
	}
	// A body that came back from a browser with CRLF line ends still parses, and
	// the words in it are untouched.
	crlf := strings.ReplaceAll(Body(Record{Text: "x", Basis: "y", From: NoLink, At: at}), "\n", "\r\n")
	if r, ok := Parse(crlf); !ok || r.Text != "x" || r.Basis != "y" {
		t.Errorf("CRLF body: %+v %v", r, ok)
	}
}

// ---- reading ----

func fakeGH(t *testing.T, replies map[string]string, errs map[string]error) (func(context.Context, ...string) ([]byte, error), *[]string) {
	t.Helper()
	var calls []string
	return func(_ context.Context, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		calls = append(calls, key)
		if err := errs[key]; err != nil {
			return nil, err
		}
		out, ok := replies[key]
		if !ok {
			t.Fatalf("unscripted gh call: gh %s", key)
		}
		return []byte(out), nil
	}, &calls
}

const (
	listKey    = "issue list -R o/r --label decisions --state open --json number,title,url --limit 100"
	oneIssue   = `[{"number":7,"title":"Decisions","url":"https://github.com/o/r/issues/7"}]`
	viewKey    = "issue view 7 -R o/r --json comments"
	noComments = `{"comments":[]}`
)

const closedKey = "issue list -R o/r --label decisions --state closed --json number,title,url --limit 100"

func TestFindIssue(t *testing.T) {
	run, calls := fakeGH(t, map[string]string{listKey: oneIssue}, nil)
	issue, found, err := FindIssue(run, "o/r")
	if err != nil || !found || issue.Number != 7 || issue.URL != "https://github.com/o/r/issues/7" {
		t.Errorf("one open issue: %+v %v %v", issue, found, err)
	}
	if got := strings.Join(*calls, "|"); got != listKey {
		t.Errorf("ran %q", got)
	}

	run, _ = fakeGH(t, map[string]string{listKey: `[]`}, nil)
	if _, found, err := FindIssue(run, "o/r"); found || err != nil {
		t.Errorf("none is not an error: %v %v", found, err)
	}

	// Two open ones is refused by name, never guessed.
	run, _ = fakeGH(t, map[string]string{listKey: `[{"number":9,"title":"Decisions","url":"u9"},{"number":3,"title":"Decisions too","url":"u3"}]`}, nil)
	_, found, err = FindIssue(run, "o/r")
	var multi *MultipleError
	if found || !errors.As(err, &multi) || !strings.Contains(err.Error(), "o/r#3, o/r#9") {
		t.Errorf("two open: %v %v", found, err)
	}

	run, _ = fakeGH(t, nil, map[string]error{listKey: errors.New("HTTP 502")})
	if _, _, err := FindIssue(run, "o/r"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("a gh failure must be returned: %v", err)
	}
	run, _ = fakeGH(t, map[string]string{listKey: `not json`}, nil)
	if _, _, err := FindIssue(run, "o/r"); err == nil {
		t.Error("bad JSON must be an error")
	}
}

func commentsJSON(cs ...[3]string) string { // createdAt, author, body
	var parts []string
	for i, c := range cs {
		login, _ := json.Marshal(c[1])
		body, _ := json.Marshal(c[2])
		parts = append(parts, fmt.Sprintf(`{"author":{"login":%s},"body":%s,"url":"https://github.com/o/r/issues/7#issuecomment-%d","createdAt":%q}`, login, body, i+1, c[0]))
	}
	return `{"comments":[` + strings.Join(parts, ",") + `]}`
}

func TestPrintIsOldestFirstAndShowsTheWordsAndTheBasis(t *testing.T) {
	older := Body(Record{Text: "iOS 26 stays as the minimum", Basis: "79% of devices, June 2026", From: "https://github.com/o/r/issues/5", At: at})
	newer := Body(Record{Text: "ads need not appear in screenshots", From: NoLink, At: at.Add(48 * time.Hour)})
	// gh lists them in the order it likes; the output is oldest first regardless.
	run, _ := fakeGH(t, map[string]string{listKey: oneIssue, viewKey: commentsJSON(
		[3]string{"2026-09-27T04:10:00Z", "op", newer},
		[3]string{"2026-09-25T04:10:00Z", "op", older},
	)}, nil)
	var out bytes.Buffer
	if err := Print(&out, run, "o/r"); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	i, j := strings.Index(s, "iOS 26 stays as the minimum"), strings.Index(s, "ads need not appear")
	if i < 0 || j < 0 || i > j {
		t.Errorf("not oldest first:\n%s", s)
	}
	for _, want := range []string{"o/r decisions, oldest first", "2026-09-25T04:10:00Z  from https://github.com/o/r/issues/5", "  iOS 26 stays as the minimum", "basis:", "    79% of devices, June 2026", "from " + NoLink, "https://github.com/o/r/issues/7#issuecomment-"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

// A comment anyone can write must not reach the terminal as control codes, and
// one that is not a decision is marked as one that is not.
func TestPrintSanitisesAndMarksWhatIsNotADecision(t *testing.T) {
	hostile := "\x1b[2J\x1b]0;pwned\x07 hello \x1b[31m"
	run, _ := fakeGH(t, map[string]string{listKey: oneIssue, viewKey: commentsJSON(
		[3]string{"2026-09-25T04:10:00Z", "someone\x1b[1m", hostile},
		[3]string{"2026-09-26T04:10:00Z", "op", Body(Record{Text: "words \x1b[2J here", From: NoLink, At: at})},
	)}, nil)
	var out bytes.Buffer
	if err := Print(&out, run, "o/r"); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), 0x1b) || strings.ContainsRune(out.String(), 0x07) {
		t.Errorf("a control character reached the terminal: %q", out.String())
	}
	for _, want := range []string{"not a recorded decision", "^[[2J", "words ^[[2J here"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// "None recorded" and "could not ask" must never look alike.
func TestPrintSeparatesNoneRecordedFromAGhFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		replies map[string]string
	}{
		"no issue":       {map[string]string{listKey: `[]`, closedKey: `[]`}},
		"an empty issue": {map[string]string{listKey: oneIssue, viewKey: noComments}},
	} {
		run, _ := fakeGH(t, tc.replies, nil)
		var out bytes.Buffer
		if err := Print(&out, run, "o/r"); !errors.Is(err, ErrNone) || out.Len() != 0 {
			t.Errorf("%s: err %v, wrote %q", name, err, out.String())
		}
	}
	for name, errs := range map[string]map[string]error{
		"the list fails":    {listKey: errors.New("gh: HTTP 401")},
		"the comments fail": {viewKey: errors.New("gh: HTTP 502")},
	} {
		run, _ := fakeGH(t, map[string]string{listKey: oneIssue, viewKey: noComments}, errs)
		var out bytes.Buffer
		err := Print(&out, run, "o/r")
		if err == nil || errors.Is(err, ErrNone) || out.Len() != 0 {
			t.Errorf("%s: err %v, wrote %q: a failure must not read as empty", name, err, out.String())
		}
	}
	// Two open issues is a failure too: it must not read as "none".
	run, _ := fakeGH(t, map[string]string{listKey: `[{"number":1,"title":"a","url":"u"},{"number":2,"title":"b","url":"u"}]`}, nil)
	if err := Print(&bytes.Buffer{}, run, "o/r"); err == nil || errors.Is(err, ErrNone) {
		t.Errorf("two issues: %v", err)
	}
}

func TestClean(t *testing.T) {
	if got := Clean("a\tb\x1b[2Jc\x7fd\u0085e"); got != "a\tb^[[2Jc^?d?e" {
		t.Errorf("Clean = %q", got)
	}
	if got := Clean("plain é 日本"); got != "plain é 日本" {
		t.Errorf("Clean changed printable text: %q", got)
	}
}

// An issue that exists but is closed is not "nothing recorded": the decisions are
// there, and the reader is told to reopen it.
func TestPrintSaysAClosedDecisionsIssueIsNotNoneRecorded(t *testing.T) {
	run, _ := fakeGH(t, map[string]string{listKey: `[]`, closedKey: `[{"number":4,"title":"Decisions","url":"u"}]`}, nil)
	var out bytes.Buffer
	err := Print(&out, run, "o/r")
	var closed *ClosedError
	if !errors.As(err, &closed) || errors.Is(err, ErrNone) || out.Len() != 0 || !strings.Contains(err.Error(), "#4 is closed") || !strings.Contains(err.Error(), "reopen it") {
		t.Errorf("err %v, wrote %q", err, out.String())
	}
	// Failing to ask about closed ones is a failure too, never "none".
	run, _ = fakeGH(t, map[string]string{listKey: `[]`}, map[string]error{closedKey: errors.New("HTTP 502")})
	if err := Print(&bytes.Buffer{}, run, "o/r"); err == nil || errors.Is(err, ErrNone) {
		t.Errorf("closed lookup failure: %v", err)
	}
}

// Bidi overrides reorder the text around them, so a comment can read as something
// it is not; they are shown, not obeyed.
func TestCleanNeutralisesBidiOverrides(t *testing.T) {
	for _, r := range []rune{0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069} {
		if got := Clean("a" + string(r) + "b"); got != "a?b" {
			t.Errorf("U+%04X: %q", r, got)
		}
	}
	if got := Clean("مرحبا שלום"); got != "مرحبا שלום" {
		t.Errorf("Clean changed ordinary right-to-left text: %q", got)
	}
}
