package inboxwatch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// maxPlanShown bounds what the plan view reads: past it the text is cut and the
// view says so.
const maxPlanShown = 1 << 20

// PlanTruncated is the line the plan view ends with when the file was cut.
const PlanTruncated = "[truncated: only the first 1 MB of this file is shown]"

// planPath is the path of a `file:` ref, before any check on it.
func planPath(ref string) (string, bool) {
	p, ok := strings.CutPrefix(ref, "file:")
	if !ok || p == "" {
		return "", false
	}
	return p, true
}

// planRoots are the only places a plan or brief may be read from, relative to
// $HOME: the fleet's working trees (briefs, docs/plans, worktrees) and Claude
// Code's plan-mode files. It is a constant on purpose: nothing an agent can write
// (an entry, a ref, an environment variable, a flag) adds a root.
var planRoots = []string{"Developer", filepath.Join(".claude", "plans")}

// PlanRootsWhy names the roots in a refusal.
const PlanRootsWhy = "outside the plan roots (~/Developer and ~/.claude/plans)"

// dotOK reports whether the component comps[i], below a root, may start with a
// dot. Almost none may (.ssh, .env, .git are where secrets live). The exceptions
// are where the fleet's own briefs live: a .worktrees directory, and .claude/worktrees.
// A different case of either stays refused, which is the safe direction.
func dotOK(comps []string, i int) bool {
	switch comps[i] {
	case ".worktrees":
		return true
	case ".claude":
		return i+1 < len(comps) && comps[i+1] == "worktrees"
	}
	return false
}

// underRoot finds which plan root the resolved path real is under, and returns the
// components of real below it. It is by identity, not by spelling: each ancestor
// directory of real is compared with each root, stat'ed now, using os.SameFile
// (same device and inode), so a path spelled ~/developer/x (APFS folds case) or
// with a differently normalised Unicode name still matches the directory it is, and
// a path that only looks like a root (~/Developer-evil) does not.
func underRoot(home, real string) ([]string, error) {
	var roots []os.FileInfo
	for _, r := range planRoots {
		if fi, err := os.Stat(filepath.Join(home, r)); err == nil && fi.IsDir() {
			roots = append(roots, fi)
		}
	}
	for dir := filepath.Dir(real); ; dir = filepath.Dir(dir) {
		if fi, err := os.Stat(dir); err == nil {
			for _, r := range roots {
				if os.SameFile(fi, r) {
					comps := strings.Split(strings.TrimPrefix(real[len(dir):], string(filepath.Separator)), string(filepath.Separator))
					for i, c := range comps[:len(comps)-1] {
						if strings.HasPrefix(c, ".") && !dotOK(comps, i) {
							return nil, fmt.Errorf("%s is a dot-directory, where secrets live", c)
						}
					}
					if c := comps[len(comps)-1]; strings.HasPrefix(c, ".") {
						return nil, fmt.Errorf("%s is a dot-file, where secrets live", c)
					}
					return comps, nil
				}
			}
		}
		if dir == filepath.Dir(dir) {
			return nil, errors.New(PlanRootsWhy)
		}
	}
}

// readPlan reads the file a `file:` ref names and returns it sanitised. Only a
// regular file whose resolved path, every symlink followed, lies under a plan
// root; see underRoot. A link inside a root to a file outside is refused as its
// target is. What it returns has been through cleanText.
//
// Known limits, accepted: the check and the open are two steps, so a path swapped
// between them is read; and a hard link put under a root to a file elsewhere passes,
// since the link's own ancestors are inside the root. Each takes an agent that can
// already write under $HOME, which could read the target directly.
func readPlan(home, ref string) (text string, cut bool, err error) {
	p, ok := planPath(ref)
	if !ok {
		return "", false, errors.New("not a file: ref")
	}
	if home == "" {
		return "", false, errors.New("no home directory to find the plan roots under")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", false, errors.New("the path is not absolute (use file:/abs/path or file:~/path)")
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", false, err
	}
	if _, err := underRoot(home, real); err != nil {
		return "", false, err
	}
	// NONBLOCK so a FIFO put there after the check cannot hold the popup, and
	// NOFOLLOW so the last component cannot become a link between the check and
	// the open. The type is asked of the open file, not the name.
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if !st.Mode().IsRegular() {
		return "", false, errors.New("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxPlanShown+1))
	if err != nil {
		return "", false, err
	}
	s, cut := capLines(string(b), maxPlanShown)
	return cleanText(s), cut, nil
}

func (e Env) home() string {
	if e.Home != "" {
		return e.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

func (e Env) plan(ref string) Event {
	p, _ := planPath(ref)
	text, cut, err := readPlan(e.home(), ref)
	if err != nil {
		return PlanEvent{Path: cleanText(p), Err: "can't show this file: " + clean(err.Error())}
	}
	return PlanEvent{Path: cleanText(p), Text: text, Cut: cut}
}
