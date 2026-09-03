// Package console joins what is TRUE about a fleet with what is IN FLIGHT and
// what is AWAITING REVIEW, so one screen answers "what should I work on".
//
// Three sources, none of which is sufficient alone:
//
//	lacquer fleet   what is true in the code       (this repo, deterministic)
//	claude agents   what is being worked on now    (`claude agents --json`)
//	gh pr list      what is waiting on a human     (the GitHub API)
//
// Truth without in-flight tells you to start work that is already underway.
// In-flight without truth is a list of sessions with no idea why they exist.
// Either without open pull requests hides the work that is finished and simply
// unmerged, which in this fleet has repeatedly been the thing actually blocking.
//
// It answers; it does not decide. Nothing here starts, stops, or modifies
// anything — Dispatch is a separate, explicit call, and even that only ever
// launches a session a human named.
//
// Two properties are deliberate and both are tested:
//
//   - A MISSING TOOL DEGRADES, IT DOES NOT FAIL. No `gh`, no network, no Claude
//     Code — the console still prints what it can and says which source was
//     unavailable. A console that refuses to run without every dependency is
//     useless exactly when something is broken, which is when it is most needed.
//   - NO PROJECT NAME APPEARS HERE. This ships from a public repo; the roster is
//     the operator's. Same boundary as internal/fleet, same test guarding it.
package console

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// Session is one Claude Code session, as `claude agents --json` reports it.
type Session struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
}

// PR is one open pull request.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Draft  bool   `json:"isDraft"`
	// Checks is a rolled-up verdict: pass, fail, pending, or "" when unknown.
	Checks string `json:"-"`
}

// Row is one project's merged view.
type Row struct {
	Name     string
	Repo     string
	Path     string
	Notes    []string // what is true: the fleet report's findings
	Sessions []Session
	PRs      []PR
	Blocking bool
	// Retired marks a project kept for reference but no longer invested in. It
	// audits clean by design, so without this the console shows it as an
	// ordinary healthy row and it competes for attention with live work.
	Retired bool
}

// Result is the whole console view plus whatever could not be gathered.
type Result struct {
	Rows []Row
	// Unavailable names each source that could not be reached, so a thin report
	// is never mistaken for a healthy fleet.
	Unavailable []string
}

// Gather builds the view. It never returns an error: an unreachable source is
// reported in Unavailable rather than failing the whole console.
func Gather(lacquerRoot string, roster fleet.Roster, now time.Time) Result {
	var res Result

	reports := fleet.Run(lacquerRoot, roster, now)

	sessions, err := listSessions()
	if err != nil {
		res.Unavailable = append(res.Unavailable, fmt.Sprintf("claude agents (%v)", err))
	}

	prs, prErr := listPRs(roster)
	if prErr != nil {
		res.Unavailable = append(res.Unavailable, fmt.Sprintf("gh pr list (%v)", prErr))
	}

	archivedRepos := archivedRoster(roster)

	for _, rep := range reports {
		row := Row{Name: rep.Name, Repo: rep.Repo, Path: rep.Path, Blocking: rep.Blocking(), Retired: rep.IsRetired()}
		row.Notes = fleet.Notes(rep)
		if rep.Repo != "" && archivedRepos[rep.Repo] {
			// This is the one place lacquer#227 actually bites: a session
			// dispatched at an archived repo does not crash or exit, it just
			// runs with nothing it can push or open a PR against, and the
			// daemon staying alive is indistinguishable from useful progress
			// anywhere else in this view. Surfaced on every sweep, not only at
			// dispatch time, so an entry going archived after it was already
			// dispatched to still gets noticed.
			row.Notes = append([]string{fmt.Sprintf("%s is archived on GitHub -- dispatch will refuse", rep.Repo)}, row.Notes...)
		}
		for _, s := range sessions {
			if under(s.CWD, rep.Path) {
				row.Sessions = append(row.Sessions, s)
			}
		}
		row.PRs = prs[rep.Repo]
		res.Rows = append(res.Rows, row)
	}
	return res
}

// under reports whether a session's working directory belongs to a project.
//
// Prefix-matching rather than equality is required, not a convenience: a
// background session runs in a git worktree BENEATH the project
// (.claude/worktrees/<id>), so an equality check would show every backgrounded
// agent as belonging to nothing — the sessions most in need of a home.
func under(cwd, root string) bool {
	return cwd == root || strings.HasPrefix(cwd, strings.TrimSuffix(root, "/")+"/")
}

// listSessions reads `claude agents --json`.
func listSessions() ([]Session, error) {
	out, err := exec.Command("claude", "agents", "--json").Output()
	if err != nil {
		return nil, err
	}
	var s []Session
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	return normalize(s), nil
}

// normalize deduplicates by session id and fills in a missing status.
//
// Both cases are observed, not defensive. `claude agents --json` returned two
// entries with different names and the SAME sessionId — one live session listed
// twice — which the console would otherwise report as two, inflating both the
// per-project count and the total. And 11 of 40 sessions carried no `status`
// field at all, which rendered as an empty "()" that reads like a bug in the
// console rather than an absent value upstream.
func normalize(in []Session) []Session {
	seen := map[string]bool{}
	out := make([]Session, 0, len(in))
	for _, s := range in {
		if s.SessionID != "" {
			if seen[s.SessionID] {
				continue
			}
			seen[s.SessionID] = true
		}
		if s.Status == "" {
			s.Status = "unknown"
		}
		out = append(out, s)
	}
	return out
}

// listPRs fetches open pull requests for every roster entry that names a repo.
//
// Concurrent because it is one network round trip per project and a fleet of
// any size makes serial calls the slowest part of the console by an order of
// magnitude. Bounded because a burst of unbounded parallel API calls is how a
// convenience becomes a rate-limit.
func listPRs(roster fleet.Roster) (map[string][]PR, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, err
	}
	out := map[string][]PR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for _, e := range roster.Project {
		if e.Repo == "" {
			continue
		}
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			prs, err := prsFor(repo)
			if err != nil {
				return // a single unreachable repo must not blank the column
			}
			mu.Lock()
			out[repo] = prs
			mu.Unlock()
		}(e.Repo)
	}
	wg.Wait()
	return out, nil
}

// archivedRoster checks every roster entry that names a repo for archived
// status, concurrently and bounded -- the same shape as listPRs, for the same
// reason: one gh call per project, run serially, is the slowest part of a
// sweep otherwise.
//
// An entry whose check is inconclusive (no gh, no network, no access) is
// simply absent from the result. "Could not tell" must never render as
// "confirmed archived", and it must never silently render as "confirmed not
// archived" either -- both are worse than a project this sweep is quiet
// about.
func archivedRoster(roster fleet.Roster) map[string]bool {
	out := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for _, e := range roster.Project {
		if e.Repo == "" {
			continue
		}
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if yes, ok := archived(repo); ok && yes {
				mu.Lock()
				out[repo] = true
				mu.Unlock()
			}
		}(e.Repo)
	}
	wg.Wait()
	return out
}

func prsFor(repo string) ([]PR, error) {
	out, err := exec.Command("gh", "pr", "list", "-R", repo, "--state", "open",
		"--json", "number,title,isDraft,statusCheckRollup", "--limit", "20").Output()
	if err != nil {
		return nil, err
	}
	var raw []struct {
		PR
		Rollup []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		p := r.PR
		p.Checks = rollup(r.Rollup)
		prs = append(prs, p)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
	return prs, nil
}

// rollup reduces a check list to one word.
//
// Failure outranks pending outranks pass, because the reader is deciding where
// to spend attention: a PR with one failed check and nine passing needs a human
// more than one still running, and reporting the majority verdict would bury it.
func rollup(checks []struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}) string {
	if len(checks) == 0 {
		return ""
	}
	var pending bool
	for _, c := range checks {
		switch {
		case c.Status != "COMPLETED":
			pending = true
		case c.Conclusion == "FAILURE" || c.Conclusion == "TIMED_OUT" || c.Conclusion == "CANCELLED":
			return "fail"
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}
