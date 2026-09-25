package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
)

// Timeout bounds one gh call.
const Timeout = 45 * time.Second

// Issue is a repository's decisions issue.
type Issue struct {
	Number int
	Title  string
	URL    string
}

// MultipleError is a repository with two or more open decisions issues. Which one
// is the log is not a guess to make: a decision recorded in the wrong one is a
// decision nobody reads.
type MultipleError struct {
	Repo   string
	Issues []Issue
}

func (e *MultipleError) Error() string {
	var refs []string
	for _, i := range e.Issues {
		refs = append(refs, fmt.Sprintf("%s#%d", e.Repo, i.Number))
	}
	return fmt.Sprintf("%s has %d open issues labelled %q (%s): close all but one, then retry", e.Repo, len(e.Issues), Label, strings.Join(refs, ", "))
}

// ListArgs is the read-only gh call that finds a repository's decisions issues.
func ListArgs(repo string) []string {
	return []string{"issue", "list", "-R", repo, "--label", Label, "--state", "open", "--json", "number,title,url", "--limit", "100"}
}

// FindIssue returns the repository's one open decisions issue. found is false,
// with no error, when it has none: the log has not been started, which is not a
// failure. Two or more is a *MultipleError.
func FindIssue(run ciwait.Runner, repo string) (issue Issue, found bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	out, err := run(ctx, ListArgs(repo)...)
	if err != nil {
		return Issue{}, false, err
	}
	var rows []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return Issue{}, false, fmt.Errorf("bad JSON from gh: %w", err)
	}
	var issues []Issue
	for _, r := range rows {
		issues = append(issues, Issue{Number: r.Number, Title: r.Title, URL: r.URL})
	}
	switch len(issues) {
	case 0:
		return Issue{}, false, nil
	case 1:
		return issues[0], true, nil
	}
	sort.Slice(issues, func(a, b int) bool { return issues[a].Number < issues[b].Number })
	return Issue{}, false, &MultipleError{Repo: repo, Issues: issues}
}

// Comment is one comment on the decisions issue, as gh reports it.
type Comment struct {
	Author    string
	Body      string
	URL       string
	CreatedAt time.Time
}

// Comments reads every comment on issue, oldest first.
func Comments(run ciwait.Runner, repo string, issue Issue) ([]Comment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	out, err := run(ctx, "issue", "view", strconv.Itoa(issue.Number), "-R", repo, "--json", "comments")
	if err != nil {
		return nil, err
	}
	var v struct {
		Comments []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body      string `json:"body"`
			URL       string `json:"url"`
			CreatedAt string `json:"createdAt"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("bad JSON from gh: %w", err)
	}
	var cs []Comment
	for _, c := range v.Comments {
		at, err := time.Parse(time.RFC3339, c.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("comment %s has no readable createdAt %q", c.URL, c.CreatedAt)
		}
		cs = append(cs, Comment{Author: c.Author.Login, Body: c.Body, URL: c.URL, CreatedAt: at})
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].CreatedAt.Before(cs[b].CreatedAt) })
	return cs, nil
}

// ErrNone is what Print returns when the repository has no decisions issue, or
// an issue with no comment in it. It is not a failure: the caller says so and
// exits 0. It is a different value from every gh error so that "nothing has been
// recorded" can never be printed for a gh that could not be reached.
var ErrNone = errors.New("no decisions recorded")

// Print writes the repository's decisions to w, oldest first. It returns ErrNone
// when there are none; any other error is a gh that could not answer, and
// nothing has been written.
func Print(w io.Writer, run ciwait.Runner, repo string) error {
	issue, found, err := FindIssue(run, repo)
	if err != nil {
		return err
	}
	if !found {
		return ErrNone
	}
	cs, err := Comments(run, repo, issue)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		return ErrNone
	}
	fmt.Fprintf(w, "%s decisions, oldest first (%s)\n", Clean(repo), Clean(issue.URL))
	for _, c := range cs {
		fmt.Fprintln(w)
		if r, ok := Parse(c.Body); ok {
			fmt.Fprintf(w, "%s  from %s\n  %s\n", Clean(r.At.UTC().Format(time.RFC3339)), Clean(r.From), Clean(c.URL))
			writeIndented(w, "  ", r.Text)
			if r.Basis != "" {
				fmt.Fprintln(w, "  basis:")
				writeIndented(w, "    ", r.Basis)
			}
			continue
		}
		// Somebody typed this into the issue. It is shown, and marked as not one.
		fmt.Fprintf(w, "%s  (a comment by %s, not a recorded decision)\n  %s\n", Clean(c.CreatedAt.UTC().Format(time.RFC3339)), Clean(c.Author), Clean(c.URL))
		writeIndented(w, "  ", c.Body)
	}
	return nil
}

func writeIndented(w io.Writer, indent, s string) {
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		fmt.Fprintln(w, indent+Clean(l))
	}
}

// Clean makes s safe to print on a terminal. A comment is text anyone with
// access to the repository can write, and a raw ESC in it can move the cursor,
// retitle the window or rewrite what was already printed. Control characters
// (tab excepted; newline is the caller's line structure) are shown as ^X.
func Clean(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString("^" + string(r+0x40))
		case r == 0x7f:
			b.WriteString("^?")
		case r >= 0x80 && r <= 0x9f:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isControl(r rune) bool { return (r < 0x20 && r != '\t') || (r >= 0x7f && r <= 0x9f) }
