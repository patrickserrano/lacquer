package inboxwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// ghTimeout bounds one gh call, so one stuck repository cannot hold a tab.
const ghTimeout = 45 * time.Second

// prWorkers bounds how many repositories are asked at once: the fleet has
// sixteen, each a separate gh process.
const prWorkers = 8

// doneLimit is how many resolved entries the Done tab keeps, as foxy-inbox does.
const doneLimit = 300

// noReposWhy is why a tab with no repository to look at says so.
const noReposWhy = "no repository to look at: the roster names none (pass --roster or set $LACQUER_ROSTER)"

// repos is every repository the Later and PRs tabs cover: the roster's, in its
// order, then the extras it does not list.
func (e Env) repos() []string {
	var out []string
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, p := range e.Roster.Project {
		add(p.Repo)
	}
	for _, r := range e.ExtraRepos {
		add(r)
	}
	return out
}

// owners are the distinct owners of repos, in first-seen order.
func (e Env) owners() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range e.repos() {
		if o, _, ok := strings.Cut(r, "/"); ok && o != "" && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

func (e Env) gh(args ...string) ([]byte, error) {
	if e.Run == nil {
		return nil, errors.New("no gh runner configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	out, err := e.Run(ctx, args...)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("gh timed out after %s", ghTimeout)
	}
	return out, err
}

func (e Env) later() Event {
	at := e.now()
	owners := e.owners()
	if len(owners) == 0 {
		return LaterEvent{Err: noReposWhy, At: at}
	}
	out, err := e.gh(laterArgs(owners)...)
	if err != nil {
		return LaterEvent{Err: err.Error(), At: at}
	}
	issues, err := parseLater(out)
	if err != nil {
		return LaterEvent{Err: err.Error(), At: at}
	}
	return LaterEvent{Issues: issues, At: at}
}

func (e Env) prs() Event {
	at := e.now()
	repos := e.repos()
	if len(repos) == 0 {
		return PRsEvent{Err: noReposWhy, At: at}
	}
	type result struct {
		prs []PR
		err error
	}
	results := make([]result, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, prWorkers)
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := e.gh(prArgs(repo)...)
			if err != nil {
				results[i].err = err
				return
			}
			results[i].prs, results[i].err = parsePRs(repo, out)
		}()
	}
	wg.Wait()
	ev := PRsEvent{At: at}
	for i, r := range results { // repositories in roster order, each one's PRs as gh listed them
		if r.err != nil {
			ev.Errors = append(ev.Errors, PRError{Repo: repos[i], Err: r.err.Error()})
			continue
		}
		ev.PRs = append(ev.PRs, r.prs...)
		if len(r.prs) >= prLimit {
			ev.Full = append(ev.Full, repos[i])
		}
	}
	if len(ev.Errors) == len(repos) {
		ev.Err = fmt.Sprintf("all %d repositories failed (first: %s: %s)", len(repos), ev.Errors[0].Repo, ev.Errors[0].Err)
	}
	return ev
}

// parseRef splits "owner/name#number". Anything else is refused before it is
// given to gh as arguments.
func parseRef(ref string) (repo string, num int, ok bool) {
	repo, n, found := strings.Cut(ref, "#")
	owner, name, slash := strings.Cut(repo, "/")
	if !found || !slash || owner == "" || name == "" || strings.Contains(name, "/") ||
		strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, " \t\r\n\x00") {
		return "", 0, false
	}
	num, err := strconv.Atoi(n)
	if err != nil || num < 1 || strconv.Itoa(num) != n {
		return "", 0, false
	}
	return repo, num, true
}

func (e Env) issue(ref string) Event {
	repo, num, ok := parseRef(ref)
	if !ok {
		return IssueEvent{Err: fmt.Sprintf("%q is not owner/name#number", clean(ref))}
	}
	out, err := e.gh("issue", "view", strconv.Itoa(num), "-R", repo, "--json",
		"title,body,labels,url,createdAt,state,comments,assignees")
	if err != nil {
		return IssueEvent{Err: err.Error()}
	}
	d, err := parseIssue(out)
	if err != nil {
		return IssueEvent{Err: err.Error()}
	}
	return IssueEvent{Data: d, OK: true}
}

// unpark takes an issue off Later: it removes the label, and only that. It is
// the one write this tool makes to GitHub, and it is asked for only by the
// operator's second d.
func (e Env) unpark(ref string) Event {
	repo, num, ok := parseRef(ref)
	if !ok {
		return DoneEvent{Kind: CmdUnpark, ID: ref, Note: fmt.Sprintf("cannot un-park %q: not owner/name#number", clean(ref))}
	}
	if _, err := e.gh("issue", "edit", "-R", repo, strconv.Itoa(num), "--remove-label", LaterLabel); err != nil {
		return DoneEvent{Kind: CmdUnpark, ID: ref, Note: "un-park failed: " + err.Error()}
	}
	return DoneEvent{Kind: CmdUnpark, ID: ref, OK: true, Note: "un-parked " + ref}
}

func (e Env) popupIssue(ref string) Event {
	if e.IssueArgv == nil {
		return DoneEvent{Kind: CmdPopupIssue, ID: ref, Note: "the issue view is not configured"}
	}
	return e.showPopup(CmdPopupIssue, ref, e.IssueArgv,
		fmt.Sprintf(" later %s  (o open · r note · d un-park · c copy · q close) ", clean(ref)))
}

// done is every resolved entry, newest resolution first, at most doneLimit: what
// foxy-inbox's completed_items shows. Its own tab because the popups carry the
// operator's own words, and the Inbox shows only what is still live.
func (e Env) done(replies map[string]Reply) []DoneItem {
	all, _, err := inbox.ReadAll(e.InboxPath)
	if err != nil {
		return nil
	}
	latest := map[string]inbox.Entry{}
	var order []string
	for _, en := range all { // the latest record for an id wins; ids keep first-seen order
		if _, ok := latest[en.ID]; !ok {
			order = append(order, en.ID)
		}
		latest[en.ID] = en
	}
	type resolved struct {
		item DoneItem
		at   time.Time
	}
	var res []resolved
	for _, id := range order {
		en := latest[id]
		if en.ResolvedAt == nil {
			continue
		}
		_, replied := replies[id]
		res = append(res, resolved{DoneItem{ID: id, Type: strings.ToUpper(string(en.Type)), CreatedAt: en.CreatedAt, Title: en.Title, Ref: en.Ref, Replied: replied}, *en.ResolvedAt})
	}
	sort.SliceStable(res, func(a, b int) bool { return res[a].at.After(res[b].at) })
	out := make([]DoneItem, 0, min(len(res), doneLimit))
	for _, r := range res[:min(len(res), doneLimit)] {
		out = append(out, r.item)
	}
	return out
}

// IssueData is a GitHub issue as the popup shows it.
type IssueData struct {
	Title     string
	Body      string
	URL       string
	State     string
	CreatedAt time.Time
	Labels    []string
	Assignees []string
	Comments  int
}

func parseIssue(out []byte) (IssueData, error) {
	var d struct {
		Title     string `json:"title"`
		Body      string `json:"body"`
		URL       string `json:"url"`
		State     string `json:"state"`
		CreatedAt string `json:"createdAt"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return IssueData{}, fmt.Errorf("bad JSON from gh: %w", err)
	}
	created, _ := time.Parse(time.RFC3339, d.CreatedAt)
	id := IssueData{Title: d.Title, Body: d.Body, URL: d.URL, State: d.State, CreatedAt: created, Comments: len(d.Comments)}
	for _, l := range d.Labels {
		id.Labels = append(id.Labels, l.Name)
	}
	for _, a := range d.Assignees {
		id.Assignees = append(id.Assignees, a.Login)
	}
	return id, nil
}
