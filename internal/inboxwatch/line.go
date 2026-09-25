package inboxwatch

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// "Not this line" answers one line of a PR's diff: the operator's words go to the
// PR as one plain comment that names the line, and to the overseer as
// "[inbox <id>] <path:line> <text>". It is not a review thread and stages nothing.
// The two are independent: either can fail without the other, and the result says
// which happened.

// LineSentEvent answers CmdLine.
type LineSentEvent struct {
	// OverseerSent is whether the words are in the overseer's pane, and Overseer
	// says so, or why not.
	OverseerSent bool
	Overseer     string
	// Commented is whether the comment is on the PR, and Comment says so (with its
	// URL), or why not. CommentUnsure is set when the post timed out and may have
	// gone through anyway: it is neither retried nor reported as failed.
	Commented     bool
	CommentUnsure bool
	Comment       string
}

// Nothing is whether neither half is done or possibly done, so retrying is safe.
func (ev LineSentEvent) Nothing() bool {
	return !ev.OverseerSent && !ev.Commented && !ev.CommentUnsure
}

// codeSpan is s as a markdown code span, whatever backticks s holds. s is a path
// out of a diff, so it is untrusted.
func codeSpan(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if fence != "`" || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// LineCommentBody is the PR comment for one answered line: the usual one-line
// provenance, a header naming path:line and which file's line number that is, and
// then the operator's text, verbatim and last. Nothing agent-written but the path.
func LineCommentBody(path string, line int, side, head, text string, at time.Time) string {
	which := "new file"
	if side == "old" {
		which = "old file, a removed line"
	}
	short := head
	if len(short) > 7 {
		short = short[:7]
	}
	header := fmt.Sprintf("Not this line: %s (line %d of the %s, at %s)", codeSpan(clean(path)), line, which, short)
	return CommentBody(header+"\n\n"+text, at)
}

// line does both halves and reports each. The overseer goes first only because it
// is quick; a failure of either never skips the other.
func (e Env) line(c Cmd) Event {
	pl := fmt.Sprintf("%s:%d", clean(c.Path), c.Line)
	if c.Side == "old" {
		// The number is the old file's; only a removed line has one, so only it is marked.
		pl += " (old)"
	}
	var ev LineSentEvent

	// The same function a reply uses, so the pane, the keystrokes and the record
	// are the same as any other reply. It is given no Ref, so it comments nothing:
	// the comment made below is the one with the header.
	switch re := e.reply(Cmd{Kind: CmdReply, ID: c.ID, Text: pl + " " + c.Text}).(type) {
	case RepliedEvent:
		// "sent, but the reply was not recorded" is not OK, and it was sent.
		ev.OverseerSent = re.OK || strings.HasPrefix(re.Note, "sent, but")
		switch {
		case re.OK:
			ev.Overseer = "sent to the overseer as [inbox " + clean(c.ID) + "] " + pl + " …"
		case ev.OverseerSent:
			ev.Overseer = "overseer: " + clean(re.Note)
		default:
			ev.Overseer = "NOT sent to the overseer: " + clean(re.Note)
		}
	}

	g, ok := ParseGitHubRef(c.Ref)
	if !ok {
		ev.Comment = "NOT posted: " + ErrNotGitHubRef.Error()
		return ev
	}
	g.PR = true // it was loaded as one
	if err := e.gate(g.Repo); err != nil {
		ev.Comment = "NOT posted: " + clean(err.Error())
		return ev
	}
	out, err := postBody(e.Cmd, g, LineCommentBody(c.Path, c.Line, c.Side, c.Head, c.Text, e.now()))
	var to *PostTimeoutError
	switch {
	case errors.As(err, &to):
		ev.CommentUnsure = true
		ev.Comment = "may or may not have posted: " + clean(err.Error())
	case err != nil:
		ev.Comment = "NOT posted: " + clean(err.Error())
	default:
		ev.Commented = true
		ev.Comment = "commented on " + g.String()
		if u := strings.TrimSpace(out); u != "" {
			ev.Comment += ": " + clean(u)
		}
	}
	return ev
}
