// Package fakegh is a stateful stand-in for the slice of `gh` that `lacquer
// ci-round` uses, for tests. It is not a _test file because two test suites
// share it: internal/cirounds calls Run in-process, and cmd/lacquer runs it as a
// real `gh` on PATH (a shim that re-executes the test binary), so a fresh
// PROCESS sees exactly the state the last one left. That is the point of the
// counter living on the PR, and a fake whose state lived in memory could not
// prove it.
//
// State is a directory:
//
//	rollup.json     the `pr view --json state,headRefOid,statusCheckRollup` reply
//	comments/N.json one PR comment each, in creation order
//	calls           every invocation's arguments, one line each
//
// It understands only the calls the tool makes and fails loudly on anything else
// rather than answering it with something plausible.
package fakegh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Comment is one PR comment as `gh pr view --json comments` reports it.
type Comment struct {
	ID     string `json:"id"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	AuthorAssociation string `json:"authorAssociation"`
	Body              string `json:"body"`
	CreatedAt         string `json:"createdAt"`
	URL               string `json:"url"`
}

// Check is a check to plant in the rollup.
type Check struct {
	Name, Status, Conclusion string
	RunID                    int
}

// Init creates an empty, open PR whose head is head and has no checks.
func Init(dir, head string) error {
	if err := os.MkdirAll(filepath.Join(dir, "comments"), 0o755); err != nil {
		return err
	}
	return SetRollup(dir, "OPEN", head)
}

// SetRollup replaces the PR's state, head and checks.
func SetRollup(dir, state, head string, checks ...Check) error {
	nodes := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		n := map[string]any{
			"__typename": "CheckRun", "workflowName": "CI", "name": c.Name,
			"status": c.Status, "conclusion": c.Conclusion,
			"startedAt": "2026-09-20T03:00:00Z", "completedAt": "2026-09-20T03:01:00Z",
		}
		if c.RunID != 0 {
			n["detailsUrl"] = fmt.Sprintf("https://github.com/o/r/actions/runs/%d/job/1", c.RunID)
		}
		nodes = append(nodes, n)
	}
	b, err := json.Marshal(map[string]any{"state": state, "headRefOid": head, "statusCheckRollup": nodes})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "rollup.json"), b, 0o644)
}

// SetCommit plants the REST commit metadata for a head.
func SetCommit(dir, head, email string, parents ...string) error {
	ps := make([]map[string]string, 0, len(parents))
	for _, p := range parents {
		ps = append(ps, map[string]string{"sha": p})
	}
	b, err := json.Marshal(map[string]any{
		"sha": head, "commit": map[string]any{"committer": map[string]string{"email": email}}, "parents": ps,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "commit-"+head+".json"), b, 0o644)
}

// Failed, Passed and Running are shorthand for planted checks.
func Failed(name string) Check  { return Check{Name: name, Status: "COMPLETED", Conclusion: "FAILURE"} }
func Passed(name string) Check  { return Check{Name: name, Status: "COMPLETED", Conclusion: "SUCCESS"} }
func Running(name string) Check { return Check{Name: name, Status: "IN_PROGRESS"} }

// AddComment plants a comment as a given author association would leave it.
func AddComment(dir, association, body string) (Comment, error) {
	entries, _ := os.ReadDir(filepath.Join(dir, "comments"))
	n := len(entries) + 1
	c := Comment{ID: fmt.Sprintf("IC_%04d", n), AuthorAssociation: association, Body: body,
		CreatedAt: time.Date(2026, 9, 20, 12, 0, n, 0, time.UTC).Format(time.RFC3339),
		URL:       fmt.Sprintf("https://github.com/o/r/pull/7#issuecomment-%d", n)}
	c.Author.Login = "someone"
	b, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	return c, os.WriteFile(filepath.Join(dir, "comments", fmt.Sprintf("%04d.json", n)), b, 0o644)
}

// Comments reads every comment in creation order.
func Comments(dir string) ([]Comment, error) {
	files, err := filepath.Glob(filepath.Join(dir, "comments", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := make([]Comment, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var c Comment
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Calls is every invocation made so far, one space-joined argument list each.
func Calls(dir string) []string {
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Run answers one gh invocation against the state in dir and returns its stdout.
func Run(dir string, args ...string) ([]byte, error) {
	f, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, strings.ReplaceAll(strings.Join(args, " "), "\n", `\n`))
		f.Close()
	}
	if len(args) == 2 && args[0] == "api" && strings.Contains(args[1], "/commits/") {
		head := args[1][strings.LastIndex(args[1], "/")+1:]
		b, err := os.ReadFile(filepath.Join(dir, "commit-"+head+".json"))
		if os.IsNotExist(err) {
			return json.Marshal(map[string]any{"sha": head, "commit": map[string]any{"committer": map[string]string{"email": "developer@example.com"}}, "parents": []any{}})
		}
		return b, err
	}
	if len(args) < 2 || args[0] != "pr" {
		return nil, fmt.Errorf("fakegh: unhandled call: gh %s", strings.Join(args, " "))
	}
	// Drop `-R owner/name`; every call carries the PR number and flags after it.
	var rest []string
	for i := 2; i < len(args); i++ {
		if args[i] == "-R" {
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	switch args[1] {
	case "view":
		if len(rest) == 3 && rest[1] == "--json" && rest[2] == "comments" {
			cs, err := Comments(dir)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"comments": cs})
		}
		if len(rest) == 3 && rest[1] == "--json" && rest[2] == "state,headRefOid,statusCheckRollup" {
			return os.ReadFile(filepath.Join(dir, "rollup.json"))
		}
	case "comment":
		if len(rest) == 3 && rest[1] == "--body" {
			c, err := AddComment(dir, "OWNER", rest[2])
			if err != nil {
				return nil, err
			}
			return []byte(c.URL + "\n"), nil
		}
	}
	return nil, errors.New("fakegh: unhandled call: gh " + strings.Join(args, " "))
}
