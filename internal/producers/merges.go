package producers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

const (
	// CursorFile sits next to the inbox file and remembers, per repository, how
	// far the harvest has looked.
	CursorFile = "merge-cursor.json"
	// harvestLimit caps one repository's answer. Hitting it means the cursor is
	// far behind, and the harvest says so instead of pretending it saw it all.
	harvestLimit = 200
	// overlap is how far behind "now" a stored cursor is kept. GitHub's search
	// index lags a merge by seconds to minutes; a cursor at "now" could step
	// over a merge that is not searchable yet. Re-seeing one is harmless (every
	// entry is deduped by ref), missing one is not.
	overlap = 10 * time.Minute
	// repoTimeout bounds one gh call, so one stuck repository cannot hold the
	// console.
	repoTimeout = 20 * time.Second
)

// HarvestOptions is everything HarvestMerges needs. Run is required: the caller
// passes ciwait.GH, and tests pass a scripted runner, so a test can never reach
// the real gh.
type HarvestOptions struct {
	InboxPath string
	Roster    fleet.Roster
	Now       time.Time
	Run       ciwait.Runner
}

// HarvestResult says what one harvest did. Nothing in it is silent: each of
// Notes and Unavailable is meant to be shown to the operator.
type HarvestResult struct {
	Added []inbox.Entry
	// Notes are facts about the harvest that are not failures: a repository's
	// first sighting, or that there was no roster to harvest.
	Notes []string
	// Unavailable is each repository (or the cursor file) that could not be
	// harvested. A failed look is never "no merges".
	Unavailable []string
}

type cursors struct {
	Repos map[string]time.Time `json:"repos"`
}

type mergedPR struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	MergedAt string `json:"mergedAt"`
}

// HarvestMerges adds one UNREAD entry for every PR merged, in a roster
// repository, since that repository's cursor.
//
// The first time a repository is seen it gets a cursor of "now" and nothing
// else: hundreds of historical merges must not flood the inbox. It is
// idempotent by ref (the PR URL), also against resolved entries, so two
// consoles running at once, or one running twice, add each merge once.
//
// The cursor advances only for a repository whose gh call succeeded. The inbox
// is appended before the cursor moves, so a crash in between re-sees a merge
// (and dedupes it) rather than losing one.
func HarvestMerges(o HarvestOptions) HarvestResult {
	var res HarvestResult
	repos := harvestRepos(o.Roster)
	if len(repos) == 0 {
		res.Notes = append(res.Notes, "merge harvest: no roster loaded (or none of its projects names a repo), so PR merges are not being recorded; pass --roster or set LACQUER_ROSTER")
		return res
	}
	if o.InboxPath == "" || o.Run == nil {
		res.Unavailable = append(res.Unavailable, "merge harvest (no inbox file or gh runner configured)")
		return res
	}
	curPath := filepath.Join(filepath.Dir(o.InboxPath), CursorFile)
	cur, err := readCursors(curPath)
	if err != nil {
		// Without the cursor "since when" is unknown; guessing would either
		// flood the inbox or skip merges.
		res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest (%v)", err))
		return res
	}

	existing := map[string]bool{}
	if all, _, err := inbox.ReadAll(o.InboxPath); err == nil {
		for _, e := range all {
			if e.Ref != "" {
				existing[e.Ref] = true
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest (inbox: %v)", err))
		return res
	}

	type answer struct {
		prs []mergedPR
		err error
	}
	answers := map[string]answer{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, r := range repos {
		since, seen := cur.Repos[r.repo]
		if !seen {
			continue // first sighting: no call, no backfill
		}
		wg.Add(1)
		go func(repo string, since time.Time) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), repoTimeout)
			defer cancel()
			out, err := o.Run(ctx, "pr", "list", "-R", repo, "--state", "merged",
				"--search", "merged:>="+since.UTC().Format(time.RFC3339),
				"--json", "number,title,url,mergedAt", "--limit", fmt.Sprint(harvestLimit))
			a := answer{err: err}
			if err == nil {
				if jerr := json.Unmarshal(out, &a.prs); jerr != nil {
					a.err = fmt.Errorf("unreadable gh output: %w", jerr)
				}
			}
			mu.Lock()
			answers[repo] = a
			mu.Unlock()
		}(r.repo, since)
	}
	wg.Wait()

	next := cursors{Repos: map[string]time.Time{}}
	for k, v := range cur.Repos {
		next.Repos[k] = v
	}
	var primed []string
	for _, r := range repos {
		since, seen := cur.Repos[r.repo]
		if !seen {
			next.Repos[r.repo] = o.Now.UTC()
			primed = append(primed, r.repo)
			continue
		}
		a := answers[r.repo]
		if a.err != nil {
			res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest %s (%v)", r.repo, a.err))
			continue
		}
		sort.Slice(a.prs, func(i, j int) bool { return a.prs[i].MergedAt < a.prs[j].MergedAt })
		failed := false
		for _, pr := range a.prs {
			if t, err := time.Parse(time.RFC3339, pr.MergedAt); err == nil && t.Before(since) {
				continue
			}
			if pr.URL == "" || existing[pr.URL] {
				continue
			}
			e, err := inbox.Add(o.InboxPath, inbox.Entry{
				Type:    inbox.Unread,
				Title:   fmt.Sprintf("%s#%d merged: %s", r.repo, pr.Number, pr.Title),
				Body:    "merged " + pr.MergedAt,
				Ref:     pr.URL,
				Project: r.name,
			})
			if err != nil {
				res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest %s (%v)", r.repo, err))
				failed = true
				break
			}
			existing[pr.URL] = true
			res.Added = append(res.Added, e)
		}
		if failed {
			continue
		}
		if len(a.prs) >= harvestLimit {
			res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest %s (%d or more merges since %s; harvested %d, cursor not advanced)", r.repo, harvestLimit, since.Format(time.RFC3339), len(a.prs)))
			continue
		}
		if to := o.Now.UTC().Add(-overlap); to.After(since) {
			next.Repos[r.repo] = to
		}
	}
	if len(primed) > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("merge harvest: first look at %s; recording merges from now on, earlier ones are not backfilled", strings.Join(primed, ", ")))
	}
	if err := writeCursors(curPath, next); err != nil {
		res.Unavailable = append(res.Unavailable, fmt.Sprintf("merge harvest (cursor not saved: %v)", err))
	}
	return res
}

type harvestRepo struct{ name, repo string }

// harvestRepos is the roster's repositories, once each, in roster order.
func harvestRepos(r fleet.Roster) []harvestRepo {
	var out []harvestRepo
	seen := map[string]bool{}
	for _, e := range r.Project {
		if e.Repo == "" || seen[e.Repo] {
			continue
		}
		seen[e.Repo] = true
		out = append(out, harvestRepo{name: e.Name, repo: e.Repo})
	}
	return out
}

// readCursors reads the cursor file; a missing file is an empty one (first run).
func readCursors(path string) (cursors, error) {
	c := cursors{Repos: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return cursors{}, fmt.Errorf("%s is unreadable (%w); delete it to start over from now", path, err)
	}
	if c.Repos == nil {
		c.Repos = map[string]time.Time{}
	}
	return c, nil
}

// writeCursors replaces the cursor file atomically (temp file in the same
// directory, then rename), so a crash leaves the old cursor or the new one.
func writeCursors(path string, c cursors) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), CursorFile+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
