package inboxwatch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// LaterLabel is the label that parks an issue.
const LaterLabel = "later"

// LaterIssue is one parked issue.
type LaterIssue struct {
	Repo      string // owner/name
	Number    int
	Title     string
	CreatedAt time.Time
	URL       string
}

// Ref is "owner/name#number", what identifies the issue everywhere.
func (i LaterIssue) Ref() string { return fmt.Sprintf("%s#%d", i.Repo, i.Number) }

// laterArgs is the gh search that lists the parked issues: every open issue
// labelled `later` in the given owners' repositories. There is always at least
// one owner: with none, `gh search issues` would search all of GitHub.
func laterArgs(owners []string) []string {
	args := []string{"search", "issues", "--label", LaterLabel, "--state", "open",
		"--limit", "300", "--json", "repository,number,title,createdAt,url"}
	for _, o := range owners {
		args = append(args, "--owner", o)
	}
	return args
}

// parseLater reads the output of laterArgs, sorted as foxy-inbox does: by the
// repository's short name, ignoring case, then by number.
func parseLater(out []byte) ([]LaterIssue, error) {
	var data []struct {
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		Number    int    `json:"number"`
		Title     string `json:"title"`
		CreatedAt string `json:"createdAt"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("bad JSON from gh: %w", err)
	}
	issues := make([]LaterIssue, 0, len(data))
	for _, d := range data {
		created, _ := time.Parse(time.RFC3339, d.CreatedAt)
		issues = append(issues, LaterIssue{Repo: d.Repository.NameWithOwner, Number: d.Number, Title: d.Title, CreatedAt: created, URL: d.URL})
	}
	sort.SliceStable(issues, func(a, b int) bool {
		sa, sb := strings.ToLower(shortName(issues[a].Repo)), strings.ToLower(shortName(issues[b].Repo))
		if sa != sb {
			return sa < sb
		}
		return issues[a].Number < issues[b].Number
	})
	return issues, nil
}

func shortName(repo string) string {
	_, after, ok := strings.Cut(repo, "/")
	if !ok {
		return repo
	}
	return after
}

// laterRows groups the issues under their repository, as foxy-inbox's later_rows.
func (m Model) laterRows() []row {
	counts := map[string]int{}
	for _, i := range m.Later.Issues {
		counts[i.Repo]++
	}
	var rows []row
	last := ""
	for _, i := range m.Later.Issues {
		if i.Repo != last {
			rows = append(rows, projectHeader(i.Repo, counts[i.Repo]))
			last = i.Repo
		}
		rows = append(rows, row{key: i.Ref(), l: line{
			{fmt.Sprintf("  #%-5d", i.Number), fg(magenta)},
			{fmt.Sprintf("%4s  ", Age(i.CreatedAt, m.Now)), fg(dim)},
			{clean(i.Title), fg(dim)},
		}})
	}
	return rows
}

func (m Model) selectedLater() (LaterIssue, bool) {
	rows := m.laterRows()
	i, ok := m.Later.at(rows)
	if !ok {
		return LaterIssue{}, false
	}
	for _, it := range m.Later.Issues {
		if it.Ref() == rows[i].key {
			return it, true
		}
	}
	return LaterIssue{}, false
}

// laterStatus is the tab's header text. A failed fetch is never left looking
// like an empty tab: it says so, and whether what is below is the last good read.
func (m Model) laterStatus() seg {
	s := m.Later
	switch {
	case s.Err != "" && len(s.Issues) > 0:
		return seg{" GitHub unavailable (showing the last result)", fgBold(red)}
	case s.Err != "":
		return seg{" GitHub unavailable", fgBold(red)}
	case !s.Answered:
		return seg{" asking GitHub…", fg(dim)}
	}
	repos := map[string]bool{}
	for _, i := range s.Issues {
		repos[i.Repo] = true
	}
	return seg{fmt.Sprintf(" %d parked · %d projects", len(s.Issues), len(repos)), fgBold(magenta)}
}

func (m Model) laterEmpty() string {
	s := m.Later
	switch {
	case s.Err != "" && len(s.Issues) == 0:
		return "could not list the parked issues: " + s.Err
	case !s.Answered:
		return "loading…"
	case len(s.Issues) == 0:
		return "nothing parked (label an issue `later` to park it)"
	}
	return ""
}
