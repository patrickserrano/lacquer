package inboxwatch

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
)

// PR is one open pull request, as foxy-prs reports it.
type PR struct {
	Repo      string
	Number    int
	Title     string
	Author    string
	Draft     bool
	CreatedAt time.Time
	URL       string
	Merge     string // gh's mergeStateStatus
	Passing   int
	Failing   int
	Pending   int
}

// Key identifies the PR across refreshes.
func (p PR) Key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// PRError is a repository whose PRs could not be listed. It is reported, never
// dropped: a repository that failed must not look like one with nothing open.
type PRError struct {
	Repo, Err string
}

// prLimit is how many PRs one repository is asked for. It is above gh's default
// of 30, which would cut a busy repository short without saying so; a repository
// that returns exactly this many may still be cut short, and the tab says so.
const prLimit = 100

// prArgs lists one repository's open PRs.
func prArgs(repo string) []string {
	return []string{"pr", "list", "-R", repo, "--state", "open", "--limit", strconv.Itoa(prLimit), "--json",
		"number,title,author,isDraft,createdAt,url,mergeStateStatus,statusCheckRollup"}
}

// parsePRs reads one repository's gh output, keeping gh's order: foxy-prs never
// re-sorted within a repository.
func parsePRs(repo string, out []byte) ([]PR, error) {
	var data []struct {
		Number           int    `json:"number"`
		Title            string `json:"title"`
		IsDraft          bool   `json:"isDraft"`
		CreatedAt        string `json:"createdAt"`
		URL              string `json:"url"`
		MergeStateStatus string `json:"mergeStateStatus"`
		Author           *struct {
			Login string `json:"login"`
		} `json:"author"`
		Rollup json.RawMessage `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("bad JSON from gh: %w", err)
	}
	prs := make([]PR, 0, len(data))
	for _, d := range data {
		login := ""
		if d.Author != nil {
			login = strings.TrimPrefix(d.Author.Login, "app/") // a bot reads "dependabot", not "app/dependabot"
		}
		created, _ := time.Parse(time.RFC3339, d.CreatedAt)
		merge := d.MergeStateStatus
		if merge == "" {
			merge = "UNKNOWN"
		}
		p := PR{Repo: repo, Number: d.Number, Title: d.Title, Author: login, Draft: d.IsDraft,
			CreatedAt: created, URL: d.URL, Merge: merge}
		counts, err := ciwait.Tally(d.Rollup)
		if err != nil {
			return nil, fmt.Errorf("PR #%d: %w", d.Number, err)
		}
		p.Passing, p.Failing, p.Pending = counts.Passing, counts.Failing, counts.Pending
		prs = append(prs, p)
	}
	return prs, nil
}

// Stale is whether the PR has been open past the operator's 24h rule.
func (p PR) Stale(now time.Time) bool {
	return !p.CreatedAt.IsZero() && now.Sub(p.CreatedAt) >= StalePR
}

func (m Model) prRows() []row {
	counts := map[string]int{}
	for _, p := range m.PRs.PRs {
		counts[p.Repo]++
	}
	var rows []row
	last := ""
	for _, p := range m.PRs.PRs {
		if p.Repo != last {
			rows = append(rows, projectHeader(p.Repo, counts[p.Repo]))
			last = p.Repo
		}
		as := fg(dim)
		if p.Stale(m.Now) {
			as = fg(magenta)
		}
		title := p.Title
		if p.Draft {
			title += " [draft]"
		}
		rows = append(rows, row{key: p.Key(), l: line{
			{fmt.Sprintf("  #%-5d", p.Number), fg(magenta)},
			{fmt.Sprintf("%4s  ", Age(p.CreatedAt, m.Now)), as},
			{fmt.Sprintf("%-14s ", clean(p.Author)), fg(dim)},
			{fmt.Sprintf("%-9s ", clean(p.Merge)), fg(dim)},
			{fmt.Sprintf("%-13s ", fmt.Sprintf("✓%d ✗%d …%d", p.Passing, p.Failing, p.Pending)), fg(cyan)},
			{clean(title), fg(dim)},
		}})
	}
	return rows
}

func (m Model) selectedPR() (PR, bool) {
	rows := m.prRows()
	i, ok := m.PRs.at(rows)
	if !ok {
		return PR{}, false
	}
	for _, p := range m.PRs.PRs {
		if p.Key() == rows[i].key {
			return p, true
		}
	}
	return PR{}, false
}

func (m Model) prsStatus() seg {
	s := m.PRs
	switch {
	case s.Err != "" && len(s.PRs) > 0:
		return seg{" GitHub unavailable (showing the last result)", fgBold(red)}
	case s.Err != "":
		return seg{" GitHub unavailable", fgBold(red)}
	case !s.Answered:
		return seg{" asking GitHub…", fg(dim)}
	}
	stale := 0
	for _, p := range s.PRs {
		if p.Stale(m.Now) {
			stale++
		}
	}
	errs := ""
	if len(s.Errors) > 0 {
		errs = fmt.Sprintf(" · %d repos errored", len(s.Errors))
	}
	if len(s.Full) > 0 {
		errs += fmt.Sprintf(" · %d repos at the %d-PR limit (may be cut)", len(s.Full), prLimit)
	}
	return seg{fmt.Sprintf(" %d open · %d over 24h%s", len(s.PRs), stale, errs), fgBold(magenta)}
}

func (m Model) prsEmpty() string {
	s := m.PRs
	switch {
	case s.Err != "" && len(s.PRs) == 0:
		return "could not list pull requests: " + s.Err
	case !s.Answered:
		return "loading…"
	case len(s.PRs) == 0 && len(s.Errors) > 0:
		// Not "no open PRs": the repositories that failed may well have some.
		return fmt.Sprintf("no open PRs in the repos that answered (%d errored, first: %s: %s)", len(s.Errors), s.Errors[0].Repo, s.Errors[0].Err)
	case len(s.PRs) == 0:
		return "no open PRs across the fleet"
	}
	return ""
}
