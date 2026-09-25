package inboxwatch

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// cleanText is clean for a multi-line text that is shown or stored as lines: the
// same C0/DEL/C1 rules (control → ^X, DEL → ^?, C1 → ?), but a newline and a tab
// are kept. A diff is written by whoever opened the PR; an ESC in it must not
// reach a terminal, and a CR must not hide the text before it.
func cleanText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r != '\n' && r != '\t' && isControl(r) }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString("^" + string(r+0x40))
		case r == 0x7f:
			b.WriteString("^?")
		case r >= 0x80 && r <= 0x9f:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// capLines keeps the longest prefix of s that is at most max bytes and ends at a
// line boundary (or, for one enormous line, at a rune boundary), and says whether
// it cut anything.
func capLines(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1], true
	}
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

// expandTabs turns each tab into spaces up to the next multiple of four cells.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += runeCells(r)
	}
	return b.String()
}

// hardWrap cuts s into rows of at most w cells and changes nothing else: unlike
// wrap it keeps every space, so a plan or a diff line reads exactly as written and
// nothing hides past the edge.
func hardWrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var rows []string
	var cur strings.Builder
	n := 0
	for _, r := range s {
		c := runeCells(r)
		if n+c > w && n > 0 {
			rows = append(rows, cur.String())
			cur.Reset()
			n = 0
		}
		cur.WriteRune(r)
		n += c
	}
	return append(rows, cur.String())
}

type diffKind byte

const (
	dkMeta diffKind = iota // index, ---, +++, mode and rename lines, "\ No newline", the file list
	dkFile                 // diff --git
	dkHunk                 // @@
	dkAdd
	dkDel
	dkCtx
)

// diffLine is one line of a unified diff. Old and New are the line's number in
// the old and the new file; 0 means it has none there.
type diffLine struct {
	Kind diffKind
	Text string
	// Path is the file's name in the new tree and OldPath its name in the old one:
	// they differ for a rename, and a removed line's number is counted in OldPath.
	Path, OldPath string
	Old, New      int
}

// respondable reports whether "not this line" can point at it.
func (l diffLine) respondable() bool {
	return l.Path != "" && (l.Kind == dkAdd || l.Kind == dkDel || l.Kind == dkCtx)
}

// target is what "not this line" names for l: the file and the number in it. A
// removed line has only an old-file number, so it goes with the old path; every
// other line has a new-file one.
func (l diffLine) target() (path string, n int, side string) {
	if l.Kind == dkDel {
		return l.OldPath, l.Old, "old"
	}
	return l.Path, l.New, "new"
}

// gitPath is a path as git prints it in a header: possibly C-quoted, and with the
// a/ or b/ prefix that marks which tree it is in.
func gitPath(tok, prefix string) string {
	if strings.HasPrefix(tok, `"`) {
		if u, err := strconv.Unquote(tok); err == nil {
			tok = u
		}
	}
	return cleanText(strings.TrimPrefix(tok, prefix))
}

// splitGitHeader reads the two names off "diff --git a/X b/Y". They are only a
// fallback: the ---/+++ and rename lines that follow are read as the truth, since
// a name may itself hold " b/".
func splitGitHeader(rest string) (oldp, newp string) {
	if strings.HasPrefix(rest, `"`) {
		if q, err := strconv.QuotedPrefix(rest); err == nil {
			return gitPath(q, "a/"), gitPath(strings.TrimSpace(rest[len(q):]), "b/")
		}
	}
	if mid := (len(rest) - 1) / 2; len(rest)%2 == 1 && rest[mid] == ' ' && strings.HasPrefix(rest, "a/") && rest[mid+1:mid+3] == "b/" && rest[2:mid] == rest[mid+3:] {
		return gitPath(rest[:mid], "a/"), gitPath(rest[mid+1:], "b/") // the same name on both sides
	}
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return gitPath(rest[:i], "a/"), gitPath(rest[i+1:], "b/")
	}
	return gitPath(rest, ""), gitPath(rest, "")
}

// headerName is the name on a "--- " or "+++ " line, without the tab git may add.
func headerName(l, prefix, tree string) (string, bool) {
	p := strings.TrimPrefix(l, prefix)
	if i := strings.IndexByte(p, '\t'); i >= 0 && !strings.HasPrefix(p, `"`) {
		p = p[:i]
	}
	if p == "/dev/null" {
		return "", false
	}
	return gitPath(p, tree), true
}

type parsedDiff struct {
	Lines []diffLine
	Files []int // the index of each file's "diff --git" line
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseDiff reads the unified diff `gh pr diff` prints. Inside a hunk a line is
// told by the counts in the hunk header, not by its first characters, so an added
// line that reads "++ b/x" is an added line and not a file header.
func parseDiff(text string) parsedDiff {
	var pd parsedDiff
	if text == "" {
		return pd
	}
	var path, oldPath string
	var oldRem, newRem, oldN, newN int
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if (oldRem > 0 || newRem > 0) && !strings.HasPrefix(l, `\`) {
			d := diffLine{Text: l, Path: path, OldPath: oldPath}
			switch {
			case strings.HasPrefix(l, "+"):
				d.Kind, d.New = dkAdd, newN
				newN++
				newRem--
			case strings.HasPrefix(l, "-"):
				d.Kind, d.Old = dkDel, oldN
				oldN++
				oldRem--
			default:
				d.Kind, d.Old, d.New = dkCtx, oldN, newN
				oldN++
				newN++
				oldRem--
				newRem--
			}
			pd.Lines = append(pd.Lines, d)
			continue
		}
		d := diffLine{Kind: dkMeta, Text: l, Path: path, OldPath: oldPath}
		switch {
		case strings.HasPrefix(l, "diff --git "):
			oldPath, path = splitGitHeader(strings.TrimPrefix(l, "diff --git "))
			d.Kind, d.Path, d.OldPath = dkFile, path, oldPath
			pd.Files = append(pd.Files, len(pd.Lines))
		case strings.HasPrefix(l, "rename from "):
			oldPath = gitPath(strings.TrimPrefix(l, "rename from "), "")
			d.OldPath = oldPath
		case strings.HasPrefix(l, "rename to "):
			path = gitPath(strings.TrimPrefix(l, "rename to "), "")
			d.Path = path
		case strings.HasPrefix(l, "--- "):
			if p, ok := headerName(l, "--- ", "a/"); ok {
				oldPath = p
			}
			d.OldPath = oldPath
		case strings.HasPrefix(l, "+++ "):
			if p, ok := headerName(l, "+++ ", "b/"); ok {
				path = p
			}
			d.Path = path
		case strings.HasPrefix(l, "@@"):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				oldN, newN = atoi(m[1]), atoi(m[3])
				oldRem, newRem = hunkCount(m[2]), hunkCount(m[4])
				d.Kind = dkHunk
			}
		}
		pd.Lines = append(pd.Lines, d)
	}
	return pd
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// hunkCount is a hunk's line count, which the header leaves out when it is 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
