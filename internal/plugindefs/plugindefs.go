// Package plugindefs checks the agent, skill and command definitions lacquer
// ships (and renders into a project) for the defects that make Claude Code skip
// one SILENTLY: a broken agent does not error, it just never gets used.
//
// # Why this exists next to `claude plugin validate`
//
// The CLI is the authority on what loads, and this repo runs it (see cli.go).
// But measured on 2.1.278 it has real blind spots:
//
//   - pointed at a bare directory it reads nothing and prints "Validation
//     passed" (an empty agents/ passes too);
//   - given a plugin wrapper it reports everything as a WARNING, so it needs
//     --strict to fail at all;
//   - it passes an agent with no `name` and one whose `name` contains a colon,
//     clean.
//
// It does catch a missing or misplaced opening `---`, a missing description, and
// several ways the YAML fails to parse (bad indentation, a tab, an unclosed
// quote, a description that wraps onto an unindented line), and it says what
// that costs: the agent loads with its name taken from the filename and every
// other field dropped. It tolerates other YAML that is not strictly valid (an
// unclosed flow sequence, a duplicate key, `: ` inside a plain scalar). So a CI
// job built on it alone would go green over the missing-name and colon-name
// defects that motivated it. This package covers those, and the frontmatter
// shape, without needing the binary.
//
// # What is deliberately NOT flagged
//
// Only defects that are certainly fatal to loading. Two things that look like
// candidates are left out on evidence:
//
//   - YAML that is not strictly valid. Six shipped agents carry an unquoted
//     `including: designing` inside their description; strict YAML rejects them
//     and Claude Code loads them, so strict-YAML failure is not "fatal" and a
//     check built on it reports working agents as broken.
//   - A missing `name` on a skill or command: the directory or file name is the
//     fallback, so it loads. Only agents require it.
//
// Nor is this a parser. It looks at the shape of the frontmatter block and at
// one field.
package plugindefs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind is what a definition file defines.
type Kind string

const (
	Agent   Kind = "agent"
	Skill   Kind = "skill"
	Command Kind = "command"
)

// File is one definition file and what it defines.
type File struct {
	Path string
	Kind Kind
}

// Finding is one defect in one file.
type Finding struct {
	Path string
	// Line is 1-based; the line the problem is on, or the opening delimiter's line.
	Line    int
	Kind    Kind
	Problem string
}

// components are the directories under a root that hold definitions.
var components = []struct {
	dir  string
	kind Kind
}{{"agents", Agent}, {"skills", Skill}, {"commands", Command}}

// Files lists every definition under root, which is a directory holding
// agents/, skills/ and/or commands/ (a `.claude` directory, `core/`, a profile).
// Agents and commands are any .md file beneath their directory; a skill is a
// direct subdirectory holding a SKILL.md, and only that file is the definition
// (its references/ are not). Symlinked files are followed; sorted for stable output.
func Files(root string) []File {
	var out []File
	for _, c := range components {
		dir := filepath.Join(root, c.dir)
		if c.kind == Skill {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := filepath.Join(dir, e.Name(), "SKILL.md")
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					out = append(out, File{p, Skill})
				}
			}
			continue
		}
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				out = append(out, File{p, c.kind})
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ShippedRoots returns every directory under repo that lacquer ships
// definitions from: core/ and each profiles/<name>/ that has an agents/,
// skills/ or commands/ directory. Found by walking, not listed, so a new profile
// is covered the day it is added.
func ShippedRoots(repo string) ([]string, error) {
	var candidates []string
	candidates = append(candidates, filepath.Join(repo, "core"))
	profiles, err := os.ReadDir(filepath.Join(repo, "profiles"))
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	for _, p := range profiles {
		if p.IsDir() {
			candidates = append(candidates, filepath.Join(repo, "profiles", p.Name()))
		}
	}
	var roots []string
	for _, c := range candidates {
		for _, comp := range components {
			if fi, err := os.Stat(filepath.Join(c, comp.dir)); err == nil && fi.IsDir() {
				roots = append(roots, c)
				break
			}
		}
	}
	return roots, nil
}

// Check checks every file and returns the findings in file order.
func Check(files []File) []Finding {
	var out []Finding
	for _, f := range files {
		out = append(out, CheckFile(f.Path, f.Kind)...)
	}
	return out
}

var (
	// A top-level key only: indented lines belong to some other key's value.
	nameLine = regexp.MustCompile(`^name:[ \t]*(.*?)[ \t]*$`)
	// Keys that identify a block as frontmatter rather than a horizontal rule
	// in a body, used only to recognise a frontmatter block that starts late.
	metaLine = regexp.MustCompile(`^(name|description):`)
)

// CheckFile reports the fatal-to-load defects in one definition file.
func CheckFile(path string, kind Kind) []Finding {
	b, err := os.ReadFile(path)
	if err != nil {
		return []Finding{{path, 1, kind, "cannot be read: " + err.Error()}}
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	find := func(line int, format string, a ...any) []Finding {
		return []Finding{{path, line, kind, fmt.Sprintf(format, a...)}}
	}

	// Frontmatter must open on line 1. Elsewhere it is invisible to the loader,
	// which reads the whole block as body text.
	if strings.TrimRight(lines[0], " \t") != "---" {
		if open := lateFrontmatter(lines); open > 0 {
			return find(open, "frontmatter opens on line %d, not line 1, so it is read as body text and none of it applies", open)
		}
		if kind == Agent {
			return find(1, "agent has no frontmatter, so it has no `name` and is skipped")
		}
		return nil // a skill or command may legitimately have none
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return find(1, "frontmatter is opened on line 1 and never closed with `---`")
	}
	if kind != Agent {
		return nil
	}

	name, nameAt := "", 0
	for i := 1; i < end; i++ {
		if m := nameLine.FindStringSubmatch(lines[i]); m != nil {
			name, nameAt = unquote(m[1]), i+1
			break
		}
	}
	if name == "" {
		at := 1
		if nameAt > 0 {
			at = nameAt
		}
		return find(at, "agent has no `name` in its frontmatter, so it is skipped")
	}
	if strings.Contains(name, ":") {
		return find(nameAt, "agent `name` %q contains ':', so it is skipped (':' separates a plugin namespace from a name)", name)
	}
	return nil
}

// lateFrontmatter returns the 1-based line of an opening `---` that is not on
// line 1 but does open a block containing name:/description:, or 0.
func lateFrontmatter(lines []string) int {
	open := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") != "---" {
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		for _, inner := range lines[open+1 : i] {
			if metaLine.MatchString(inner) {
				return open + 1
			}
		}
		return 0
	}
	return 0
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
