package inboxwatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Writing an answered decision back to its issue is what makes GitHub the source
// of state (#424): a decision that lives only in inbox-replies.jsonl is one nobody
// greps. WriteBack is the one function that posts it, so a decisions log (#427)
// reuses it rather than growing a second path to `gh issue comment`.

// writeBackTimeout bounds the comment, so a hung gh cannot hold the popup on "sending".
var writeBackTimeout = ghTimeout

// ErrNotGitHubRef is returned for a ref that is not a GitHub issue or PR. It is
// "nothing to write back to", not a failure.
var ErrNotGitHubRef = errors.New("not a GitHub issue or pull request ref")

// GitHubRef is a parsed issue or PR reference.
type GitHubRef struct {
	Repo   string // owner/name
	Number int
	PR     bool // whether it named a pull request
}

func (r GitHubRef) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)
)

// validRepo is the check on an owner/name that goes to gh after -R: a name that
// gh would read as a flag, or that carries whitespace or a path, never gets there.
func validRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && ownerRe.MatchString(owner) && repoRe.MatchString(name) && name != "." && name != ".."
}

// ParseGitHubRef accepts exactly two shapes:
//
//	https://github.com/<owner>/<repo>/issues/<n>   (or /pull/<n>)
//	<owner>/<repo>#<n>
//
// Anything else is refused, and the ref is agent-written text, so it is checked
// the way parseRef checks one, and more tightly: no query, no fragment, no
// trailing path, no other host, and an owner and a repo of the characters
// GitHub allows.
func ParseGitHubRef(ref string) (GitHubRef, bool) {
	if rest, ok := strings.CutPrefix(ref, "https://github.com/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) != 4 || (parts[2] != "issues" && parts[2] != "pull") {
			return GitHubRef{}, false
		}
		repo := parts[0] + "/" + parts[1]
		n, err := strconv.Atoi(parts[3])
		if !validRepo(repo) || err != nil || n < 1 || strconv.Itoa(n) != parts[3] {
			return GitHubRef{}, false
		}
		return GitHubRef{Repo: repo, Number: n, PR: parts[2] == "pull"}, true
	}
	repo, n, ok := parseRef(ref)
	if !ok || !validRepo(repo) {
		return GitHubRef{}, false
	}
	return GitHubRef{Repo: repo, Number: n}, true
}

// CommentBody is the operator's text, verbatim, under one line that says where
// it came from. Nothing agent-written goes in: not the entry's title, not its body.
func CommentBody(text string, at time.Time) string {
	return fmt.Sprintf("**From the operator's inbox** (lacquer inbox watch, %s):\n\n%s\n", at.UTC().Format(time.RFC3339), text)
}

// WriteBack posts text to the issue or PR ref names, through `gh ... --body-file -`
// so the text travels on stdin and never in an argument list or a shell. It
// returns the ref it posted to. A ref that does not parse returns
// ErrNotGitHubRef and runs nothing. `session:` refs, a bare "#12" and other
// hosts all land there.
func WriteBack(c Commander, ref, text string, at time.Time) (GitHubRef, error) {
	g, ok := ParseGitHubRef(ref)
	if !ok {
		return GitHubRef{}, ErrNotGitHubRef
	}
	_, err := postBody(c, g, CommentBody(text, at))
	return g, err
}

// postBody comments body on g and returns what gh printed, which is the
// comment's URL. It is the one place that runs `gh ... comment`, so a reply and a
// decision cannot differ in how the text travels or in what a timeout is called.
func postBody(c Commander, g GitHubRef, body string) (string, error) {
	sub := "issue"
	if g.PR {
		sub = "pr"
	}
	argv := []string{sub, "comment", strconv.Itoa(g.Number), "-R", g.Repo, "--body-file", "-"}
	ctx, cancel := context.WithTimeout(context.Background(), writeBackTimeout)
	defer cancel()
	out, err := c.RunContext(ctx, body, "gh", argv...)
	if ctx.Err() == context.DeadlineExceeded {
		// gh was killed at the deadline, but it may have posted before that.
		return out, &PostTimeoutError{Ref: g, After: writeBackTimeout}
	}
	return out, err
}

// PostTimeoutError is a comment that did not finish in time. It says nothing
// about whether it posted.
type PostTimeoutError struct {
	Ref   GitHubRef
	After time.Duration
}

func (e *PostTimeoutError) Error() string {
	kind := "issues"
	if e.Ref.PR {
		kind = "pull"
	}
	return fmt.Sprintf("gh timed out after %s and was stopped; the comment may or may not have posted, check https://github.com/%s/%s/%d", e.After, e.Ref.Repo, kind, e.Ref.Number)
}
