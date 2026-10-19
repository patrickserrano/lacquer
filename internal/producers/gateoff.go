package producers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/gateoff"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/lock"
)

// The gate-off harvest (#576). A PR can turn a lacquer gate off in ways its own
// CI cannot be trusted to catch: on pull_request the workflow and the lacquer
// binary both come from the PR's head. This harvest runs from the fleet's own
// lacquer after the merge, where the PR cannot reach, and raises ONE UNREAD
// entry per merge that turned anything off, listing every finding of
// internal/gateoff with its reason. Nothing is blocked: a legitimate downgrade
// or exemption is surfaced, and that is its path.
//
// Cost. A merge whose file list leaves .lacquer.lock, .lacquer.toml and every
// managed path alone, and renames or deletes nothing, costs nothing once the
// harvest knows the managed paths of its base branch (cursors.Managed). Any
// other merge costs one commit read, two recursive tree reads, and one blob
// read per distinct blob it needs: the lock (one when unchanged, else two), the
// manifest (both sides when it changed; the merge's alone when a managed file
// finding wants an exclusion's reason), and each managed file the merge changed
// (both sides when edited, for the action-bump check). A hand edit of one
// workflow in a project with a manifest is seven calls.
//
// Known limits. A rebase merge is compared as its last commit against that
// commit's parent, so a gate turned off by an earlier commit of the same PR is
// missed. Removing a [[component]] or a profile turns that stack's gates off
// with no finding: the lock and the managed files change as a sync of the new
// manifest would change them.

// touchesGates reports whether a merge's file list may include a gate: the
// lock, the manifest, a path the lock manages, or a rename or deletion (gh
// lists a rename by its NEW path only, so the old one may have been managed).
// gh lists at most 100 files a PR, so a list that long may have cut one off and
// counts. Until the managed paths are known, any file may be one, so only an
// empty list is clear.
func touchesGates(f prFiles, managed []string, known bool) bool {
	if len(f.Files) >= 100 || (!known && len(f.Files) > 0) {
		return true
	}
	set := make(map[string]bool, len(managed))
	for _, p := range managed {
		set[p] = true
	}
	for _, file := range f.Files {
		if file.Path == lock.Name || file.Path == gateoff.ManifestName || set[file.Path] ||
			file.ChangeType == "RENAMED" || file.ChangeType == "DELETED" {
			return true
		}
	}
	return false
}

// harvestGatesOff raises one UNREAD entry, ref `<PR url>#gate-off`, listing
// every gate the merged PR turned off, and returns the paths the merge's lock
// manages (nil when that could not be learned).
//
// A transient failure (gh failed, an answer that does not decode) is returned
// with nothing written, so the PR stays unmarked and the next harvest retries
// it. A permanent one becomes one UNREAD entry asking for a look by hand,
// beside whatever could be checked, and the harvest moves on: a file that does
// not parse, a merge commit gh does not name or GitHub does not have, under
// `<PR url>#gate-off-unreadable`; a tree too large for GitHub to list whole,
// which will be as large on every merge, once per repository under
// `<repo url>#gate-off-tree-truncated`.
func harvestGatesOff(o HarvestOptions, r harvestRepo, pr mergedPR, existing map[string]bool) ([]inbox.Entry, []string, error) {
	merge := pr.MergeCommit.Oid
	var res gateoff.Result
	var err error
	if merge == "" {
		err = &unreadableError{"gh reported no merge commit for the PR, so its base cannot be read"}
	} else {
		res, err = checkMerge(o, r.repo, merge, gateoff.Options{ByDependabot: pr.byDependabot()})
	}
	var stuck *unreadableError
	var big *truncatedError
	switch {
	case errors.As(err, &big):
		res = gateoff.Result{}
	case errors.As(err, &stuck):
		res = gateoff.Result{Unreadable: []string{stuck.why}}
	case err != nil:
		return nil, nil, err
	}

	var added []inbox.Entry
	addOnce := func(ref string, e inbox.Entry) error {
		if existing[ref] {
			return nil
		}
		e.Type, e.Ref, e.Project = inbox.Unread, ref, r.name
		got, err := inbox.Add(o.InboxPath, e)
		if err != nil {
			return err
		}
		existing[ref] = true
		added = append(added, got)
		return nil
	}
	if len(res.Findings) > 0 {
		whats := make([]string, 0, len(res.Findings))
		lines := make([]string, 0, len(res.Findings))
		for _, f := range res.Findings {
			whats = append(whats, f.What)
			lines = append(lines, fmt.Sprintf("- %s %s — %s", f.Kind, f.Subject, f.ReasonText()))
		}
		if err := addOnce(pr.URL+"#gate-off", inbox.Entry{
			Title: fmt.Sprintf("%s#%d turned gates off: %s", r.repo, pr.Number, strings.Join(whats, "; ")),
			Body:  strings.Join(lines, "\n"),
		}); err != nil {
			return added, nil, err
		}
	}
	if len(res.Unreadable) > 0 {
		body := "merge " + merge
		if merge == "" {
			body = "no merge commit"
		}
		if err := addOnce(pr.URL+"#gate-off-unreadable", inbox.Entry{
			Title: fmt.Sprintf("couldn't check %s#%d for gates turned off: %s; check by hand", r.repo, pr.Number, strings.Join(res.Unreadable, "; ")),
			Body:  body,
		}); err != nil {
			return added, nil, err
		}
	}
	if big != nil {
		if err := addOnce("https://github.com/"+r.repo+"#gate-off-tree-truncated", inbox.Entry{
			Title: fmt.Sprintf("couldn't check %s for gates turned off: %s (first on #%d), so its merges go unchecked; check them by hand", r.repo, big.why, pr.Number),
			Body:  "merge " + merge,
		}); err != nil {
			return added, nil, err
		}
	}
	if !res.ManagedKnown {
		return added, nil, nil
	}
	return added, res.Managed, nil
}

// truncatedError is a tree GitHub would not list whole. It is a property of
// the repository, not of one merge.
type truncatedError struct{ why string }

func (e *truncatedError) Error() string { return e.why }

// gateJSON is ghJSON for the gate-off reads, where every object is addressed
// by a commit or blob id gh just named: a 404 or 422 for one is not going to
// heal, so it is unreadable rather than retried forever.
func gateJSON(run ciwait.Runner, v any, what string, args ...string) error {
	err := ghJSON(run, v, args...)
	if err != nil && (strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 422")) {
		return &unreadableError{fmt.Sprintf("%s: %v", what, err)}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// checkMerge lists the merge commit's tree and its first parent's, and runs the
// check over them, reading blobs through gh.
func checkMerge(o HarvestOptions, repo, merge string, opts gateoff.Options) (gateoff.Result, error) {
	var commit struct {
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := gateJSON(o.Run, &commit, "merge commit "+merge, "api", fmt.Sprintf("repos/%s/git/commits/%s", repo, merge)); err != nil {
		return gateoff.Result{}, err
	}
	if len(commit.Parents) == 0 || commit.Parents[0].SHA == "" {
		return gateoff.Result{}, &unreadableError{fmt.Sprintf("merge commit %s has no parent to compare with", merge)}
	}
	base, err := snapshot(o, repo, commit.Parents[0].SHA)
	if err != nil {
		return gateoff.Result{}, err
	}
	head, err := snapshot(o, repo, merge)
	if err != nil {
		return gateoff.Result{}, err
	}
	read := func(id string) ([]byte, error) {
		var blob struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := gateJSON(o.Run, &blob, "blob "+id, "api", fmt.Sprintf("repos/%s/git/blobs/%s", repo, id)); err != nil {
			return nil, err
		}
		if blob.Encoding != "base64" {
			return nil, &unreadableError{fmt.Sprintf("blob %s came back as %q, not base64", id, blob.Encoding)}
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
		if err != nil {
			return nil, &unreadableError{fmt.Sprintf("blob %s is not valid base64: %v", id, err)}
		}
		return data, nil
	}
	return gateoff.Check(base, head, read, opts)
}

// snapshot lists every file in commit. A tree GitHub truncated would read
// every path it left out as deleted, so it is a truncatedError instead.
func snapshot(o HarvestOptions, repo, commit string) (gateoff.Snapshot, error) {
	var t struct {
		Truncated *bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := gateJSON(o.Run, &t, "tree of "+commit, "api", fmt.Sprintf("repos/%s/git/trees/%s?recursive=1", repo, commit)); err != nil {
		return gateoff.Snapshot{}, err
	}
	if t.Truncated == nil {
		return gateoff.Snapshot{}, fmt.Errorf("tree of %s: gh answered without a truncated flag", commit)
	}
	if *t.Truncated {
		return gateoff.Snapshot{}, &truncatedError{"its tree is too large for GitHub to list whole (truncated)"}
	}
	s := gateoff.Snapshot{Blobs: map[string]string{}}
	for _, e := range t.Tree {
		if e.Type == "blob" {
			s.Blobs[e.Path] = e.SHA
		}
	}
	return s, nil
}
