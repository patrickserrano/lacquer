package console

import (
	"fmt"
	"io"
	"strings"
)

// Text renders the merged view.
//
// One line per project when there is nothing to say, because the console is
// read to decide what to do next, and a screen that costs scrolling to skim is
// one people stop opening. Detail is indented under the project it belongs to
// rather than spread across columns: notes vary wildly in length, and a table
// that wraps is harder to read than a list that does not.
func Text(w io.Writer, res Result) {
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

	fmt.Fprintf(w, "\n%d project(s) · %d session(s) (%d working) · %d open PR(s)\n",
		len(res.Rows), idle+working, working, prs)

	// Named loudly. A console missing a source looks exactly like a quiet
	// fleet, and the difference matters most when something is broken.
	for _, u := range res.Unavailable {
		fmt.Fprintf(w, "unavailable: %s — that column is blank, not empty\n", u)
	}
}

// summary is the one-line right-hand side: the shape of this project right now.
func summary(r Row) []string {
	var out []string
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
