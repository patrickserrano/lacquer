package console

import (
	"fmt"
	"io"
	"strings"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Text renders the merged view.
//
// One line per project when there is nothing to say, because the console is
// read to decide what to do next, and a screen that costs scrolling to skim is
// one people stop opening. Detail is indented under the project it belongs to
// rather than spread across columns: notes vary wildly in length, and a table
// that wraps is harder to read than a list that does not.
//
// ACTION prints first, before every project row, and UNREAD prints right
// after it -- both ahead of the roster entirely, including the "roster is
// empty" short-circuit below. A decision blocked on the operator outranks the
// state of any single project; showing it below a page of project rows is how
// it gets scrolled past.
func Text(w io.Writer, res Result) {
	printInboxSection(w, "ACTION", "!!", res.Actions)
	printInboxSection(w, "UNREAD", "  ", res.Unread)

	if len(res.Rows) == 0 {
		fmt.Fprintln(w, "roster is empty")
		return
	}

	width := 0
	for _, r := range res.Rows {
		if len(r.Name) > width {
			width = len(r.Name)
		}
	}

	var idle, working, prs int
	for _, r := range res.Rows {
		mark := "  "
		if r.Blocking {
			mark = "!!"
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", mark, width, r.Name, strings.Join(summary(r), "   "))

		for _, n := range r.Notes {
			fmt.Fprintf(w, "   %-*s  · %s\n", width, "", n)
		}
		for _, s := range r.Sessions {
			if s.Status == "working" {
				working++
			} else {
				idle++
			}
			fmt.Fprintf(w, "   %-*s  ▸ session %s (%s) — claude attach %s\n",
				width, "", s.Name, s.Status, short(s.SessionID))
		}
		for _, p := range r.PRs {
			prs++
			d := ""
			if p.Draft {
				d = " draft"
			}
			checks := p.Checks
			if checks == "" {
				checks = "no checks"
			}
			fmt.Fprintf(w, "   %-*s  # %d%s [%s] %s\n", width, "", p.Number, d, checks, trim(p.Title, 60))
		}
	}

	fmt.Fprintf(w, "\n%d project(s) · %d session(s) (%d working) · %d open PR(s) · %d action(s) · %d unread\n",
		len(res.Rows), idle+working, working, prs, len(res.Actions), len(res.Unread))

	// Named loudly. A console missing a source looks exactly like a quiet
	// fleet, and the difference matters most when something is broken.
	for _, u := range res.Unavailable {
		fmt.Fprintf(w, "unavailable: %s — that column is blank, not empty\n", u)
	}
}

// printInboxSection renders one inbox.Entry section (ACTION or UNREAD) if it
// has anything to show, and nothing at all otherwise -- an empty header line
// with nothing under it is the "state indistinguishable from working" shape
// this repo's CLAUDE.md warns about (looks like "checked, found nothing" when
// it is actually "nothing to check").
//
// mark reuses the same two-column mark convention the project rows already
// use ("!!" for Blocking, two spaces otherwise): ACTION gets "!!" because a
// stalled decision is exactly the kind of thing that mark exists to flag;
// UNREAD does not, because finished-but-unacknowledged work is informational,
// not blocking. · and # are the same prefixes the project rows use for a note
// and a numbered reference, respectively -- reused rather than invented, so
// the visual language stays one language across the whole screen.
func printInboxSection(w io.Writer, header, mark string, entries []inbox.Entry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	for _, e := range entries {
		fmt.Fprintf(w, "%s %s  [%s]\n", mark, e.Title, e.ID)
		if e.Body != "" {
			fmt.Fprintf(w, "   · %s\n", e.Body)
		}
		var tail []string
		if e.Ref != "" {
			tail = append(tail, "#"+strings.TrimPrefix(e.Ref, "#"))
		}
		if e.Project != "" {
			tail = append(tail, e.Project)
		}
		if len(tail) > 0 {
			fmt.Fprintf(w, "   %s\n", strings.Join(tail, "   "))
		}
	}
	fmt.Fprintln(w)
}

// summary is the one-line right-hand side: the shape of this project right now.
func summary(r Row) []string {
	var out []string
	// First, so it frames everything after it. A retired project audits clean by
	// design, so it would otherwise read "clear" — indistinguishable from a live
	// project in good health, and the one row where "clear" means "nobody is
	// looking at this" rather than "this is fine".
	if r.Retired {
		out = append(out, "retired")
	}
	if n := len(r.Notes); n > 0 {
		out = append(out, fmt.Sprintf("%d finding(s)", n))
	}
	if n := len(r.Sessions); n > 0 {
		out = append(out, fmt.Sprintf("%d session(s)", n))
	}
	if n := len(r.PRs); n > 0 {
		out = append(out, fmt.Sprintf("%d PR(s)", n))
	}
	if len(out) == 0 {
		return []string{"clear"}
	}
	return out
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
