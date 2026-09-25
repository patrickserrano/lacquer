package inboxwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/patrickserrano/lacquer/internal/decisions"
)

// Recording a decision (#427) is the operator choosing, on an inbox item, to keep
// their words where the work they govern can read them: as a comment on one
// `decisions` issue, in that repository or, for a fleet-wide one, in the fleet
// repository. See package decisions for the log and the comment's form.
//
// It is a GitHub write in three possible parts (a label, an issue, a comment),
// each made only in response to the operator's keypress and each behind the same
// gate as a reply's comment: a repository outside the roster and the extras is
// refused, the fleet repository included. The ref on an entry is written by an
// agent, so it may point the operator's words at a repository they already watch
// and at nowhere else.

// DecisionTargets are the two places a decision from one inbox item can go, or
// the reason each cannot. The operator sees them before anything is posted.
type DecisionTargets struct {
	// Repo is "this repository": the one the entry's ref names, else the one its
	// project is mapped to. Empty when neither resolves, and RepoWhy says why.
	Repo, RepoWhy string
	// Fleet is the fleet-wide repository. Empty when it is not usable, and
	// FleetWhy says why.
	Fleet, FleetWhy string
}

// DecisionTargets resolves where a decision from an entry with this ref and
// project may go.
func (e Env) DecisionTargets(ref, project string) DecisionTargets {
	var t DecisionTargets
	t.Repo, t.RepoWhy = e.thisRepo(ref, project)
	switch {
	case e.FleetRepo == "":
		t.FleetWhy = "no fleet repository is configured"
	case !validRepo(e.FleetRepo):
		t.FleetWhy = fmt.Sprintf("the fleet repository %q is not owner/name", clean(e.FleetRepo))
	case e.gate(e.FleetRepo) != nil:
		t.FleetWhy = e.FleetRepo + " is not in the roster or the extra repositories, so nothing is written there"
	default:
		t.Fleet = e.FleetRepo
	}
	return t
}

func (e Env) thisRepo(ref, project string) (repo, why string) {
	var reasons []string
	if g, ok := ParseGitHubRef(ref); ok {
		if e.gate(g.Repo) == nil {
			return g.Repo, ""
		}
		reasons = append(reasons, "the item's ref names "+g.Repo+", which is not in the roster or the extras")
	} else {
		reasons = append(reasons, "the item's ref is not an issue or pull request")
	}
	if project == "" {
		reasons = append(reasons, "the item names no project")
	} else {
		for _, p := range e.Roster.Project {
			if p.Repo != "" && strings.EqualFold(p.Name, project) && validRepo(p.Repo) {
				return p.Repo, ""
			}
		}
		reasons = append(reasons, fmt.Sprintf("its project %q maps to no repository in the roster", clean(project)))
	}
	return "", strings.Join(reasons, "; ")
}

// refLink is what the comment links to for the entry's ref: the canonical
// URL of a parsed issue or PR in a repository this watcher covers. It is never
// the raw ref, which an agent wrote, and a link to a repository the operator does
// not watch is not made: GitHub would write a cross-reference into that
// repository's timeline.
func (e Env) refLink(ref string) string {
	g, ok := ParseGitHubRef(ref)
	if !ok || e.gate(g.Repo) != nil {
		return decisions.NoLink
	}
	kind := "issues"
	if g.PR {
		kind = "pull"
	}
	return fmt.Sprintf("https://github.com/%s/%s/%d", g.Repo, kind, g.Number)
}

// decide records c.Text as a decision in c.Repo's decisions issue, making the
// label and the issue first if there are none. Every outcome, including every
// failure, says what was done and what was not: a decision that is half recorded
// and reported as nothing is how one gets recorded twice, or never.
func (e Env) decide(c Cmd) Event {
	repo := c.Repo
	fail := func(done []string, why string, unsure bool) Event {
		note := "NOT recorded in " + clean(repo) + ": " + clean(why)
		if len(done) > 0 {
			note += ". Already done: " + strings.Join(done, "; ") + "."
		} else {
			note += ". Nothing was posted."
		}
		if unsure {
			note = clean(why)
			if len(done) > 0 {
				note += ". Already done: " + strings.Join(done, "; ") + "."
			}
		}
		return DecidedEvent{Note: note, Unsure: unsure}
	}
	if strings.TrimSpace(c.Text) == "" {
		return fail(nil, "there is no text to record", false)
	}
	if !validRepo(repo) {
		return fail(nil, fmt.Sprintf("%q is not owner/name", repo), false)
	}
	if err := e.gate(repo); err != nil {
		return fail(nil, err.Error()+", so nothing is written there", false)
	}
	if e.Run == nil || e.Cmd == nil {
		return fail(nil, "gh is not configured", false)
	}

	var done []string
	issue, found, err := decisions.FindIssue(e.Run, repo)
	if err != nil {
		return fail(nil, "could not look up the "+decisions.Label+" issue: "+err.Error(), false)
	}
	if !found {
		// First use: the label, then the issue. Each is its own write and is named
		// as done if a later one fails, so a retry knows where it stands.
		has, err := e.hasLabel(repo)
		if err != nil {
			return fail(nil, "could not look up the "+decisions.Label+" label: "+err.Error(), false)
		}
		if !has {
			if out, err := e.write(repo, "", "label", "create", decisions.Label, "-R", repo,
				"--description", decisions.LabelDescription, "--color", decisions.LabelColor); err != nil {
				return e.writeFailed(repo, done, "create the "+decisions.Label+" label", out, err)
			}
			done = append(done, "created the "+decisions.Label+" label")
		}
		out, err := e.write(repo, decisions.IssueBody, "issue", "create", "-R", repo,
			"--title", decisions.IssueTitle, "--body-file", "-", "--label", decisions.Label)
		if err != nil {
			return e.writeFailed(repo, done, "create the decisions issue", out, err)
		}
		n, url, ok := issueFromURL(repo, out)
		if !ok {
			return fail(done, fmt.Sprintf("created an issue, but gh did not say which (it printed %q), so the decision was not posted; find it in %s and retry", clean(strings.TrimSpace(out)), repo), false)
		}
		issue = decisions.Issue{Number: n, URL: url}
		done = append(done, "created issue "+repo+"#"+strconv.Itoa(n))
	}

	body := decisions.Body(decisions.Record{Text: c.Text, Basis: c.Basis, From: e.refLink(c.Ref), At: e.now()})
	_, out, err := e.postGated(fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number), body)
	if err != nil {
		var to *PostTimeoutError
		if errors.As(err, &to) {
			return fail(done, err.Error(), true)
		}
		return fail(done, "posting the comment on "+repo+"#"+strconv.Itoa(issue.Number)+" failed: "+err.Error(), false)
	}
	url := strings.TrimSpace(out)
	if !strings.HasPrefix(url, "https://github.com/") || strings.ContainsAny(url, " \t\r\n") {
		url = issue.URL
		if url == "" {
			url = fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number)
		}
	}
	note := "recorded in " + repo + "#" + strconv.Itoa(issue.Number) + ": " + clean(url)
	if len(done) > 0 {
		note += " (also " + strings.Join(done, "; ") + ")"
	}
	return DecidedEvent{OK: true, Note: note, URL: url}
}

// hasLabel says whether the repository already has a decisions label, from
// gh's own listing (a read).
func (e Env) hasLabel(repo string) (bool, error) {
	out, err := e.gh("label", "list", "-R", repo, "--search", decisions.Label, "--json", "name", "--limit", "100")
	if err != nil {
		return false, err
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return false, fmt.Errorf("bad JSON from gh: %w", err)
	}
	for _, r := range rows {
		if strings.EqualFold(r.Name, decisions.Label) {
			return true, nil
		}
	}
	return false, nil
}

// write runs a gh command that changes GitHub in repo, with stdin on stdin and a
// deadline. It asks the gate itself, whatever its caller did first, so that a
// write added later cannot forget to.
func (e Env) write(repo, stdin string, args ...string) (string, error) {
	if err := e.gate(repo); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeBackTimeout)
	defer cancel()
	out, err := e.Cmd.RunContext(ctx, stdin, "gh", args...)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, errTimedOut
	}
	return out, err
}

var errTimedOut = errors.New("gh timed out")

func (e Env) writeFailed(repo string, done []string, what, out string, err error) Event {
	if errors.Is(err, errTimedOut) {
		return DecidedEvent{Unsure: true, Note: fmt.Sprintf(
			"gh timed out after %s trying to %s in %s and was stopped; it may or may not have finished, so check the repository before retrying. The decision was NOT posted",
			writeBackTimeout, what, clean(repo))}
	}
	note := "NOT recorded in " + clean(repo) + ": could not " + what + ": " + clean(err.Error())
	if len(done) > 0 {
		note += ". Already done: " + strings.Join(done, "; ") + ". The decision was not posted; retrying will pick up from there."
	} else {
		note += ". Nothing was posted."
	}
	return DecidedEvent{Note: note}
}

// issueFromURL reads the issue `gh issue create` printed. It must name an issue
// in repo: anything else is not trusted as where to post.
func issueFromURL(repo, out string) (n int, url string, ok bool) {
	url = strings.TrimSpace(out)
	rest, found := strings.CutPrefix(url, "https://github.com/"+repo+"/issues/")
	if !found {
		return 0, "", false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || strconv.Itoa(n) != rest {
		return 0, "", false
	}
	return n, url, true
}
