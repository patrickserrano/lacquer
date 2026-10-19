package producers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/gateoff"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/lock"
)

// The gate-off harvest (#576). A PR can turn a lacquer gate off in ways its own
// CI cannot be trusted to catch: on pull_request the workflow and the lacquer
// binary both come from the PR's head. This harvest runs from the fleet's own
// lacquer after the merge, where the PR cannot reach, and raises an UNREAD
// entry for each finding of internal/gateoff, quoting the PR's reason. Nothing
// is blocked: a legitimate downgrade or exemption is surfaced, and that is its
// path.
//
// Cost. A merge whose file list leaves .lacquer.lock, .lacquer.toml and every
// managed path alone costs nothing, once the harvest knows the managed paths
// (cursors.Managed). Any other merge costs one commit read, two recursive tree
// reads, and one blob read per distinct blob it needs: the lock at each side
// (one when unchanged), the manifest at each side (none when unchanged), and
// each managed file the merge changed (at both sides when edited, for the
// action-bump check).

// touchesGates reports whether a merge's file list may include a gate: the
// lock, the manifest, or a path the lock manages. gh lists at most 100 files a
// PR, so a list that long may have cut one off and counts. Until the managed
// paths are known, any file may be one, so only an empty list is clear.
func touchesGates(f prFiles, managed []string, known bool) bool {
	if len(f.Files) >= 100 || (!known && len(f.Files) > 0) {
		return true
	}
	set := make(map[string]bool, len(managed))
	for _, p := range managed {
		set[p] = true
	}
	for _, file := range f.Files {
		if file.Path == lock.Name || file.Path == gateoff.ManifestName || set[file.Path] {
			return true
		}
	}
	return false
}

// harvestGatesOff raises one UNREAD entry per gate the merged PR turned off,
// each deduped by `<PR url>#gate-off-<kind>-<subject>`, and returns the paths
// the merge's lock manages (nil when that could not be learned).
//
// A transient failure (gh failed, an answer that does not decode) is returned
// with nothing written, so the PR stays unmarked and the next harvest retries
// it. A permanent one (a file that does not parse, a tree too large to list)
// becomes one UNREAD entry asking for a look by hand, beside whatever could be
// checked, and the harvest moves on.
func harvestGatesOff(o HarvestOptions, r harvestRepo, pr mergedPR, existing map[string]bool) ([]inbox.Entry, []string, error) {
	merge := pr.MergeCommit.Oid
	if merge == "" {
		return nil, nil, fmt.Errorf("gh reported no merge commit for the PR, so its base cannot be read")
	}
	res, err := checkMerge(o, r.repo, merge)
	var stuck *unreadableError
	if errors.As(err, &stuck) {
		res = gateoff.Result{Unreadable: []string{stuck.why}}
	} else if err != nil {
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
	for _, f := range res.Findings {
		if err := addOnce(pr.URL+"#"+f.Ref(), inbox.Entry{
			Title: fmt.Sprintf("%s#%d %s", r.repo, pr.Number, f.What),
			Body:  f.ReasonText(),
		}); err != nil {
			return added, nil, err
		}
	}
	if len(res.Unreadable) > 0 {
		if err := addOnce(pr.URL+"#gate-off-unreadable", inbox.Entry{
			Title: fmt.Sprintf("couldn't check %s#%d for gates turned off: %s; check by hand", r.repo, pr.Number, strings.Join(res.Unreadable, "; ")),
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

// checkMerge lists the merge commit's tree and its first parent's, and runs the
// check over them, reading blobs through gh.
func checkMerge(o HarvestOptions, repo, merge string) (gateoff.Result, error) {
	var commit struct {
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := ghJSON(o.Run, &commit, "api", fmt.Sprintf("repos/%s/git/commits/%s", repo, merge)); err != nil {
		return gateoff.Result{}, fmt.Errorf("merge commit %s: %w", merge, err)
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
		if err := ghJSON(o.Run, &blob, "api", fmt.Sprintf("repos/%s/git/blobs/%s", repo, id)); err != nil {
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
	return gateoff.Check(base, head, read)
}

// snapshot lists every file in commit. A tree GitHub truncated would read
// every path it left out as deleted, so it is unreadable instead.
func snapshot(o HarvestOptions, repo, commit string) (gateoff.Snapshot, error) {
	var t struct {
		Truncated *bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := ghJSON(o.Run, &t, "api", fmt.Sprintf("repos/%s/git/trees/%s?recursive=1", repo, commit)); err != nil {
		return gateoff.Snapshot{}, fmt.Errorf("tree of %s: %w", commit, err)
	}
	if t.Truncated == nil {
		return gateoff.Snapshot{}, fmt.Errorf("tree of %s: gh answered without a truncated flag", commit)
	}
	if *t.Truncated {
		return gateoff.Snapshot{}, &unreadableError{fmt.Sprintf("the tree of %s is too large for GitHub to list whole (truncated)", commit)}
	}
	s := gateoff.Snapshot{Blobs: map[string]string{}}
	for _, e := range t.Tree {
		if e.Type == "blob" {
			s.Blobs[e.Path] = e.SHA
		}
	}
	return s, nil
}
