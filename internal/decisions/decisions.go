// Package decisions is the operator's decisions log (#427): what they decided,
// in their own words, kept where the work it governs can read it.
//
// The log is GitHub, not a file. Each repository has at most one open issue
// labelled `decisions`, and each recorded decision is one comment on it, so
// there is nothing to commit, no rendered copy to go stale, and the operator's
// words are greppable by anyone who can read the repository. Decisions that
// span repositories go in one such issue in the fleet-ops repository instead of
// being copied into each project's, where they would diverge.
//
// This package knows the shape of the log and how to read it. Writing is not
// here: it happens only in inboxwatch, behind the gate that refuses a
// repository outside the roster and its extras, and only on the operator's
// keypress.
//
// # The comment
//
// Every comment has this form, and nothing else is ever in one:
//
//	**Decision** — 2026-09-25T04:10:00Z · from inbox item https://github.com/o/r/issues/5
//
//	```text
//	<the operator's words, exactly as typed>
//	```
//
//	Basis:
//
//	```text
//	<the measurement or reason the decision was made against, if given>
//	```
//
// The words are in a code fence, not a blockquote, because a fence is the one
// markdown form that shows a string as written. In a quote, `#123` becomes a
// cross-reference that writes to another issue's timeline, `@name` notifies
// someone, and backticks, `*` and `_` are eaten. The fence is longer than any
// run of backticks in the text, so no text can close it early, and Parse gives
// back the exact string that Body was given.
//
// "Decisions expire into facts": a decision made against a measurement should
// record the measurement, so a later reader can tell whether the basis still
// holds. That is what Basis is for. It is optional.
//
// Nothing agent-written is in the comment. The entry's title and body are never
// passed in; the only thing taken from the entry is its ref, and that only as a
// link built from a parsed, allow-listed reference.
package decisions

import (
	"strings"
	"time"
)

const (
	// Label is what marks a repository's decisions issue.
	Label = "decisions"
	// IssueTitle is the title the issue is created with.
	IssueTitle = "Decisions"
	// LabelDescription and LabelColor are what the label is created with, in a
	// repository that has none.
	LabelDescription = "The operator's recorded decisions: verbatim, one comment each"
	LabelColor       = "5319e7"
)

// IssueBody is the fixed text the decisions issue is created with. It says what
// the issue is, so the next person to open it does not tidy it away.
const IssueBody = "The operator's recorded decisions for this repository, one comment each, " +
	"in the operator's own words.\n\n" +
	"Written by `lacquer console inbox watch` when the operator chooses to record a reply as a " +
	"decision; read with `lacquer decisions`. Keep this issue open, and keep it the only open " +
	"issue labelled `" + Label + "`: a second one makes the log refuse to guess which is meant.\n"

// NoLink is what the From line says when the inbox item had no ref that names an
// issue or pull request in a repository the operator watches.
const NoLink = "an inbox item with no issue or pull request link"

const (
	headMark  = "**Decision** — "
	fromMark  = " · from "
	basisMark = "Basis:"
	fenceInfo = "text"
)

// Record is one decision.
type Record struct {
	// Text is the operator's words. It is written exactly as given: not trimmed,
	// not reflowed, not summarised.
	Text string
	// Basis is the measurement or reason the decision was made against. Optional.
	Basis string
	// From is a link to the inbox item's ref, or NoLink.
	From string
	At   time.Time
}

// Body is the comment that records r.
func Body(r Record) string {
	var b strings.Builder
	b.WriteString(headMark + r.At.UTC().Format(time.RFC3339) + fromMark + r.From + "\n\n")
	b.WriteString(fenced(r.Text))
	if r.Basis != "" {
		b.WriteString("\n\n" + basisMark + "\n\n" + fenced(r.Basis))
	}
	b.WriteString("\n")
	return b.String()
}

// fenced puts s in a code fence one backtick longer than the longest run in s,
// so nothing in s can close it.
func fenced(s string) string {
	fence := strings.Repeat("`", max(3, longestRun(s, '`')+1))
	return fence + fenceInfo + "\n" + s + "\n" + fence
}

func longestRun(s string, c rune) int {
	best, run := 0, 0
	for _, r := range s {
		if r == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// Parse reads back a comment that Body wrote. ok is false for anything else, a
// comment somebody typed into the issue by hand included: such a comment is
// shown as it is, and is not a decision.
//
// One tolerance: a body whose first line ends in CRLF was rewritten by something
// between GitHub and here, and then every line end in it, the words' included,
// is taken as "\n". The words are exact for a body that came back as it was sent.
func Parse(body string) (r Record, ok bool) {
	if first, _, _ := strings.Cut(body, "\n"); strings.HasSuffix(first, "\r") {
		body = strings.ReplaceAll(body, "\r\n", "\n")
	}
	rest, found := strings.CutPrefix(body, headMark)
	if !found {
		return Record{}, false
	}
	head, rest, found := cutLine(rest)
	if !found {
		return Record{}, false
	}
	at, from, found := strings.Cut(head, fromMark)
	if !found || from == "" {
		return Record{}, false
	}
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return Record{}, false
	}
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "\r"), "\n") // the blank line after the head
	text, rest, found := unfence(rest)
	if !found {
		return Record{}, false
	}
	r = Record{Text: text, From: from, At: when}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return r, true
	}
	afterMark, found := strings.CutPrefix(rest, basisMark)
	if !found {
		return Record{}, false
	}
	basis, tail, found := unfence(strings.TrimLeft(afterMark, "\r\n"))
	if !found || strings.TrimSpace(tail) != "" {
		return Record{}, false
	}
	r.Basis = basis
	return r, true
}

// cutLine splits s at its first newline, dropping one "\r" before it: a comment
// that went through a browser can come back with CRLF line ends.
func cutLine(s string) (line, rest string, ok bool) {
	line, rest, ok = strings.Cut(s, "\n")
	return strings.TrimSuffix(line, "\r"), rest, ok
}

// unfence reads one fenced block from the start of s and returns what was in it,
// exactly, and what follows it.
func unfence(s string) (content, rest string, ok bool) {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	if n < 3 {
		return "", "", false
	}
	fence := s[:n]
	s, found := strings.CutPrefix(s[n:], fenceInfo)
	if !found {
		return "", "", false
	}
	_, s, found = cutLine(s)
	if !found {
		return "", "", false
	}
	// No run in the content is as long as the fence, so the first line that is
	// the fence is the one that closes it.
	closer := "\n" + fence
	i := strings.Index(s, closer)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(closer):], true
}
