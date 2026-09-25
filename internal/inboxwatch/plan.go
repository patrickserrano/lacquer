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

// dotOK reports whether a path component that starts with a dot may be read. The
// answer is almost always no: ~/.ssh, ~/.aws, ~/.config/op, ~/.gnupg and the rest
// are where an operator's secrets are, and a `file:` ref is written by an agent.
// The two exceptions are where the fleet's own plans and briefs live: a
// `.worktrees` directory, and .claude/worktrees and .claude/plans (never the rest
// of ~/.claude, which holds credentials).
func dotOK(comps []string, i int) bool {
	switch comps[i] {
	case ".worktrees":
		return true
	case ".claude":
		return i+1 < len(comps) && (comps[i+1] == "worktrees" || comps[i+1] == "plans")
	}
	return false
}

// underHome splits p, which must lie strictly inside home, into the components
// below it, and refuses one that is or reaches a place secrets live.
func underHome(home, p string) ([]string, error) {
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, errors.New("outside your home directory")
	}
	comps := strings.Split(rel, string(filepath.Separator))
	if comps[0] == "Library" {
		return nil, errors.New("~/Library holds keychains and app data")
	}
	for i, c := range comps {
		if strings.HasPrefix(c, ".") && !dotOK(comps, i) {
			return nil, fmt.Errorf("%s is a dot-directory or dot-file, where secrets live", c)
		}
	}
	return comps, nil
}

// readPlan reads the file a `file:` ref names and returns it sanitised. It reads
// only a regular file that lies under home: the path as written, and the path it
// resolves to once every symlink is followed, must both be there and must both
// clear underHome, so a link inside home to ~/.ssh is refused as the target is,
// and a `..` cannot climb out. What it returns has been through cleanText.
func readPlan(home, ref string) (text string, cut bool, err error) {
	p, ok := planPath(ref)
	if !ok {
		return "", false, errors.New("not a file: ref")
	}
	if home == "" {
		return "", false, errors.New("no home directory to keep the file under")
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", false, fmt.Errorf("home directory: %w", err)
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", false, errors.New("the path is not absolute (use file:/abs/path or file:~/path)")
	}
	p = filepath.Clean(p)
	// The lexical path is judged against home as written and as resolved, since
	// $HOME may itself be a symlink, and the resolved one against the resolved.
	if _, err := underHome(filepath.Clean(home), p); err != nil {
		if _, err2 := underHome(realHome, p); err2 != nil {
			return "", false, err
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false, err
	}
	if _, err := underHome(realHome, real); err != nil {
		return "", false, fmt.Errorf("resolves to %s: %w", clean(real), err)
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
