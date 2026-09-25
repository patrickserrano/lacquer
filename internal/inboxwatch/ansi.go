package inboxwatch

import (
	"fmt"
	"strings"
	"unicode"
)

// The palette is foxy-inbox's, index for index: accents are the terminal's own
// palette (0-7 and 8 for dim) so they follow the operator's theme, and the
// 256-colour indexes are the ones the tmux bar uses (Catppuccin Mocha, roughly).
const (
	def     = -1
	red     = 1
	green   = 2
	yellow  = 3
	blue    = 4
	magenta = 5
	cyan    = 6
	dim     = 8

	peach   = 216 // the active tab
	surface = 237 // a pill's label cell
	crust   = 234 // ink on an accent
	text    = 189 // a pill's label
	rule    = 240 // the rule under the tabs
	selBG   = 236 // the selected row
)

// Nerd Font glyphs the operator's terminal font carries.
const (
	capL = ""
	capR = ""

	iconAction  = ""
	iconClear   = ""
	iconInfo    = ""
	iconReplied = "↩"
)

// style is one SGR state. fg and bg use def for "the terminal's default".
type style struct {
	fg, bg  int
	bold    bool
	reverse bool
}

func fgCode(n int) string {
	switch {
	case n < 0:
		return "39"
	case n < 8:
		return fmt.Sprint(30 + n)
	case n < 16:
		return fmt.Sprint(90 + n - 8)
	}
	return fmt.Sprintf("38;5;%d", n)
}

func bgCode(n int) string {
	switch {
	case n < 0:
		return "49"
	case n < 8:
		return fmt.Sprint(40 + n)
	case n < 16:
		return fmt.Sprint(100 + n - 8)
	}
	return fmt.Sprintf("48;5;%d", n)
}

// sgr is the escape that sets exactly s, whatever came before it.
func (s style) sgr() string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	if s.bold {
		b.WriteString(";1")
	}
	if s.reverse {
		b.WriteString(";7")
	}
	b.WriteString(";" + fgCode(s.fg) + ";" + bgCode(s.bg) + "m")
	return b.String()
}

func fg(n int) style     { return style{fg: n, bg: def} }
func fgBold(n int) style { return style{fg: n, bg: def, bold: true} }

const reset = "\x1b[0m"

// seg is a run of text in one style.
type seg struct {
	text string
	st   style
}

// line is one screen row, built from runs.
type line []seg

func (l line) add(t string, st style) line { return append(l, seg{t, st}) }

// width is the row's width in cells.
func (l line) width() int {
	n := 0
	for _, s := range l {
		n += cells(s.text)
	}
	return n
}

// render cuts the row to w cells. selected paints every cell's background with
// the selection colour, foregrounds intact, and fills the tail: the terminal
// counterpart of foxy-inbox's mirrored colour pairs (reverse video inverted each
// run separately, which read as several unrelated highlights).
func (l line) render(w int, selected bool) string {
	var b strings.Builder
	used := 0
	for _, s := range l {
		if used >= w {
			break
		}
		t := truncate(s.text, w-used)
		if t == "" {
			continue
		}
		st := s.st
		if selected && st.bg == def {
			st.bg = selBG
		}
		b.WriteString(st.sgr() + t)
		used += cells(t)
	}
	if selected && used < w {
		b.WriteString(style{fg: def, bg: selBG}.sgr() + strings.Repeat(" ", w-used))
	}
	b.WriteString(reset)
	return b.String()
}

// cells is a string's width in terminal cells: wide (CJK, emoji) runes count 2,
// combining and zero-width ones 0. It is deliberately small; the titles here
// are English prose with the odd emoji.
func cells(s string) int {
	n := 0
	for _, r := range s {
		n += runeCells(r)
	}
	return n
}

func runeCells(r rune) int {
	switch {
	case r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200b || r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f):
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) || (r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

// truncate keeps the longest prefix of s that fits in w cells.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := 0
	for i, r := range s {
		c := runeCells(r)
		if n+c > w {
			return s[:i]
		}
		n += c
	}
	return s
}

// pill is a rounded pill: a cap, an accent icon cell, a label on surface, a cap.
// With no icon it is the tab form, a solid accent with bold ink.
func pill(label string, accent int, icon string) line {
	if icon == "" {
		return line{
			{capL, fg(accent)},
			{label, style{fg: crust, bg: accent, bold: true}},
			{capR, fg(accent)},
		}
	}
	return line{
		{capL, fg(accent)},
		{icon + " ", style{fg: crust, bg: accent}},
		{" " + label, style{fg: text, bg: surface}},
		{capR, fg(surface)},
	}
}

func pillWidth(label, icon string) int {
	w := cells(label) + 2
	if icon != "" {
		w += cells(icon) + 2
	}
	return w
}

// hint draws a key-hint row: each "key what · key what" segment has its key
// bright and its description dim, so the keys can be found at a glance.
func hint(h string) line {
	l := line{{" ", fg(dim)}}
	for n, s := range strings.Split(h, " · ") {
		key, what, _ := strings.Cut(s, " ")
		if n > 0 {
			l = l.add("  ·  ", fg(dim))
		}
		l = l.add(key, fgBold(cyan))
		if what != "" {
			l = l.add(" "+what, fg(dim))
		}
	}
	return l
}

// wrap breaks s into lines of at most w cells the way Python's textwrap does for
// the popup's body: whitespace runs are kept inside a line and dropped at its
// ends, and a word longer than a line is broken. Blank input is one empty line.
func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	var chunks []string
	for i := 0; i < len(s); {
		j := i
		sp := s[i] == ' '
		for j < len(s) && (s[j] == ' ') == sp {
			j++
		}
		chunks = append(chunks, s[i:j])
		i = j
	}
	var lines []string
	cur := ""
	emit := func() {
		if strings.TrimSpace(cur) != "" {
			lines = append(lines, strings.TrimRight(cur, " "))
		}
		cur = ""
	}
	for _, c := range chunks {
		if c[0] == ' ' {
			switch {
			case cur == "" && len(lines) > 0: // indentation is dropped on a continuation line
			case cells(cur)+cells(c) > w:
				emit()
			default:
				cur += c
			}
			continue
		}
		if cur != "" && cells(cur)+cells(c) > w {
			emit()
		}
		for cells(c) > w {
			head := truncate(c, w)
			if head == "" {
				head = string([]rune(c)[:1])
			}
			lines = append(lines, head)
			c = c[len(head):]
		}
		cur += c
	}
	emit()
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}
