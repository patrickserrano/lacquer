package inboxwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxDiffShown bounds the diff the popup holds, so one enormous PR cannot fill
	// the popup's memory. The cache file has its own, smaller cap.
	maxDiffShown = 1 << 20
	// maxDiffCache is the most a cache file holds, marker included.
	maxDiffCache = 256 << 10
	// DiffCacheMarker is the one line that ends a cache file whose diff was cut
	// short. It is fixed text, it is never followed by anything, and it is the only
	// line in a cache file that did not come from the diff.
	DiffCacheMarker = "[lacquer: diff truncated at 256 KB]"
	// maxFilesListed bounds the file list above the diff.
	maxFilesListed = 40
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// DiffFile is one changed file as `gh pr view --json files` reports it.
type DiffFile struct {
	Path                 string
	Additions, Deletions int
}

// DiffEvent answers CmdDiff. Err is why there is no diff; it is never left empty
// on a failure, and an empty Text with no Err is a diff that really has no lines.
type DiffEvent struct {
	Ref    GitHubRef
	Head   string
	Files  []DiffFile
	Text   string // sanitised, at most maxDiffShown
	Cut    bool   // Text is not the whole diff
	Reused bool   // the diff was already held for this head, and was not fetched again
	Err    string
	// CacheErr is why the phone's copy was not written; the view is unaffected.
	CacheErr string
}

// PlanEvent answers CmdPlan.
type PlanEvent struct {
	Path string // the ref's path, as written
	Text string // sanitised
	Cut  bool
	Err  string
}

// DiffMemo holds the diffs this process has fetched, by PR and head sha. A popup
// is its own process, so this is what makes closing the diff and opening it again
// cost no second `gh pr diff`; a different popup starts empty on purpose, since
// nothing on disk is trusted to say what a diff was.
type DiffMemo struct {
	mu sync.Mutex
	m  map[string]DiffEvent
}

func memoKey(g GitHubRef, head string) string { return g.String() + "@" + head }

func (d *DiffMemo) get(g GitHubRef, head string) (DiffEvent, bool) {
	if d == nil {
		return DiffEvent{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ev, ok := d.m[memoKey(g, head)]
	return ev, ok
}

func (d *DiffMemo) put(ev DiffEvent) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]DiffEvent{}
	}
	d.m[memoKey(ev.Ref, ev.Head)] = ev
}

// isIssueURL is whether ref is the URL of an issue, which is never a PR.
func isIssueURL(ref string) bool {
	return strings.HasPrefix(ref, "https://github.com/") && strings.Contains(ref, "/issues/")
}

// diff loads the pull request the ref names: its files and head sha (one gh
// call), and, unless this process already holds that head, its diff (a second).
// The repository must be one the watcher covers, and is checked before gh runs.
func (e Env) diff(ref string) Event {
	g, ok := ParseGitHubRef(ref)
	if !ok || isIssueURL(ref) {
		return DiffEvent{Err: "this item's ref is not a pull request"}
	}
	g.PR = true
	if err := e.gate(g.Repo); err != nil {
		return DiffEvent{Ref: g, Err: err.Error()}
	}
	n := strconv.Itoa(g.Number)
	out, err := e.gh("pr", "view", n, "-R", g.Repo, "--json", "files,headRefOid")
	if err != nil {
		return DiffEvent{Ref: g, Err: "couldn't load the diff: " + clean(err.Error())}
	}
	head, files, err := parsePRFiles(out)
	if err != nil {
		return DiffEvent{Ref: g, Err: "couldn't load the diff: " + clean(err.Error())}
	}
	if ev, ok := e.Diffs.get(g, head); ok {
		ev.Reused = true
		return ev
	}
	raw, err := e.gh("pr", "diff", n, "-R", g.Repo, "--color=never")
	if err != nil {
		return DiffEvent{Ref: g, Head: head, Err: "couldn't load the diff: " + clean(err.Error())}
	}
	text := cleanText(string(raw))
	shown, cut := capLines(text, maxDiffShown)
	ev := DiffEvent{Ref: g, Head: head, Files: files, Text: shown, Cut: cut}
	if err := e.writeDiffCache(g, head, text); err != nil {
		ev.CacheErr = err.Error()
	}
	e.Diffs.put(ev)
	return ev
}

// parsePRFiles reads `gh pr view --json files,headRefOid`. A head that is not a
// sha is an error: it becomes part of a file name.
func parsePRFiles(out []byte) (head string, files []DiffFile, err error) {
	var v struct {
		HeadRefOid string `json:"headRefOid"`
		Files      []struct {
			Path      string `json:"path"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", nil, fmt.Errorf("bad JSON from gh: %w", err)
	}
	if !shaRe.MatchString(v.HeadRefOid) {
		return "", nil, fmt.Errorf("gh reported no usable head commit (%q)", clean(v.HeadRefOid))
	}
	for _, f := range v.Files {
		files = append(files, DiffFile{Path: cleanText(f.Path), Additions: f.Additions, Deletions: f.Deletions})
	}
	return v.HeadRefOid, files, nil
}

// diffDir is where the phone's copies go, beside the inbox file.
func (e Env) diffDir() string { return filepath.Join(filepath.Dir(e.InboxPath), "diffs") }

// DiffCacheName is a cache file's name: <owner>_<repo>_<n>_<sha>.diff.
func DiffCacheName(g GitHubRef, head string) string {
	return strings.ReplaceAll(g.Repo, "/", "_") + "_" + strconv.Itoa(g.Number) + "_" + head + ".diff"
}

// diffCachePrefix is what every cache file of one PR starts with.
func diffCachePrefix(g GitHubRef) string {
	return strings.ReplaceAll(g.Repo, "/", "_") + "_" + strconv.Itoa(g.Number) + "_"
}

// writeDiffCache writes the phone's copy of a PR's diff, and then removes the
// same PR's copies of other heads, so only the newest is kept. text is the diff
// already sanitised, and nothing else goes in the file: no entry title, no body,
// nothing an agent wrote outside the diff. A diff over maxDiffCache is cut at a
// line and ends with DiffCacheMarker. The write is a temp file renamed into place,
// so a reader never sees half a file.
func (e Env) writeDiffCache(g GitHubRef, head, text string) error {
	if e.InboxPath == "" {
		return errors.New("no inbox path, so no place for the cache")
	}
	if !validRepo(g.Repo) || !shaRe.MatchString(head) {
		return errors.New("refusing to name a cache file after an unvalidated repository or head")
	}
	body := text
	if len(body) > maxDiffCache {
		room := maxDiffCache - len(DiffCacheMarker) - 2 // the newline before the marker and the one after it
		body, _ = capLines(body, room)
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += DiffCacheMarker + "\n"
	}
	dir := e.diffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sweepStaleTemps(dir)
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(body)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	name := DiffCacheName(g, head)
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	// Exact, not a glob: "o_b_5_" also starts o/b_5#1's files.
	old := regexp.MustCompile("^" + regexp.QuoteMeta(diffCachePrefix(g)) + "[0-9a-f]{40,64}\\.diff$")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, en := range ents {
		if n := en.Name(); n != name && old.MatchString(n) {
			if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// staleTemp is a temp file this package's writer leaves behind (".write-<digits>.tmp",
// which is what os.CreateTemp makes of ".write-*.tmp"), and nothing else.
var staleTemp = regexp.MustCompile(`^\.write-[0-9]+\.tmp$`)

// staleTempAge is how long a temp file may sit before it is taken to be from a
// write that never finished.
const staleTempAge = 10 * time.Minute

// sweepStaleTemps removes the writer's own temp files older than staleTempAge. It
// is best effort: a file it cannot remove is left for the next write.
func sweepStaleTemps(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range ents {
		if !staleTemp.MatchString(en.Name()) {
			continue
		}
		if fi, err := en.Info(); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) > staleTempAge {
			os.Remove(filepath.Join(dir, en.Name()))
		}
	}
}
