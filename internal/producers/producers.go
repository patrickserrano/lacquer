// Package producers is what makes the inbox write itself. internal/inbox is
// only the store; until this package nothing but a human's `console inbox add`
// (and ci-round's exhausted budget) ever appended to it, and a queue that
// depends on someone remembering to write to it rots (see inbox's own history).
//
// Three producers live here, each in the process that can see the event:
//
//   - GateRejection: `lacquer wait pr` ends, and says so, when a PR was never
//     tested, timed out, or could not be checked. It runs locally as the
//     operator's user, so it can write the operator's inbox; a GitHub workflow
//     on the self-hosted runners cannot (different macOS user).
//   - HarvestMerges: a PR merge happens through `gh pr merge`, typed by a PM,
//     and lacquer never sees it. So the console asks GitHub, on read, what
//     merged since it last looked.
//   - AgentIdle: a background agent going idle is a Claude Code Stop hook firing
//     in that session, so `lacquer console inbox hook stop` (shipped in the iOS
//     profile's .claude/settings.json) writes it, keyed on $CLAUDE_JOB_DIR so
//     that only background sessions ever do. See agent_idle.go.
//
// All use only the two existing entry types and the existing fields. The phone
// mirror reads this file; a new type or field would break it silently.
package producers

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

var prURLRe = regexp.MustCompile(`github\.com/([^/]+/[^/]+)/pull/\d+`)

// slug is the owner/name a wait was about: what the caller named, else what the
// PR's own URL says, else "" (gh inferred it from the checkout and reported no URL).
func slug(res ciwait.Result) string {
	if res.Repo != "" {
		return res.Repo
	}
	if m := prURLRe.FindStringSubmatch(res.URL); m != nil {
		return m[1]
	}
	return ""
}

// prRef is "<owner/repo>#<N>", or "#<N>" when the repository is not known.
func prRef(res ciwait.Result) string { return fmt.Sprintf("%s#%d", slug(res), res.PR) }

// GateRejection records, for one finished `wait pr`, the outcomes that need a
// human. It returns the entry it added, or added=false when there was nothing
// to add (and err when the inbox could not be written or read).
//
//	Passed    nothing: nobody needs to know.
//	Failed    nothing, by decision: the IC is already fixing it, and the
//	          operator's stuck view raises a PR left failing for hours.
//	TimedOut  ACTION: neither a pass nor a failure; a human has to look.
//	NoChecks  ACTION: the PR was never tested, which is not green.
//	Error     ACTION naming the reason, except when the PR is no longer open
//	          (a merge or close is not a problem) or the wait was interrupted
//	          (the person at the keyboard did that).
//
// Idempotent: an open entry with the same ref and title for the same head sha
// is not added twice, so re-running the wait on an unchanged PR stays quiet.
func GateRejection(path string, res ciwait.Result, interrupted bool) (entry inbox.Entry, added bool, err error) {
	var title, body string
	head := res.Head
	switch res.Outcome {
	case ciwait.TimedOut:
		title = prRef(res) + ": CI timed out, neither passed nor failed"
		if run := res.Running(); len(run) > 0 {
			names := make([]string, len(run))
			for i, c := range run {
				names[i] = c.Name
			}
			body = "still running: " + strings.Join(names, ", ")
		}
	case ciwait.NoChecks:
		title = prRef(res) + ": no checks ran, so this PR was never tested"
		body = res.Message
	case ciwait.Error:
		if interrupted || res.State == "MERGED" || res.State == "CLOSED" {
			return inbox.Entry{}, false, nil
		}
		title = prRef(res) + ": wait pr could not run, CI state unknown"
		body = res.Message
	default: // Passed, Failed
		return inbox.Entry{}, false, nil
	}
	if head != "" {
		body = strings.TrimSpace(body + "\nhead " + head)
	}

	ref := res.URL
	if ref == "" && slug(res) != "" {
		ref = fmt.Sprintf("https://github.com/%s/pull/%d", slug(res), res.PR)
	}
	dup, err := openDuplicate(path, ref, title, head)
	if err != nil {
		return inbox.Entry{}, false, err
	}
	if dup {
		return inbox.Entry{}, false, nil
	}
	e, err := inbox.Add(path, inbox.Entry{Type: inbox.Action, Title: title, Body: body, Ref: ref, Project: slug(res)})
	if err != nil {
		return inbox.Entry{}, false, err
	}
	return e, true, nil
}

// openDuplicate reports whether an OPEN entry already carries this ref, title
// and head sha. The sha lives in the body because the entry has no field for
// it and may not gain one.
func openDuplicate(path, ref, title, head string) (bool, error) {
	open, _, err := inbox.ListOpen(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range open {
		if e.Ref == ref && e.Title == title && (head == "" || strings.Contains(e.Body, head)) {
			return true, nil
		}
	}
	return false, nil
}
