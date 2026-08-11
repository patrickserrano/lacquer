// Package suppress finds inline lint suppressions in project source.
//
// Every other exemption in this tool is accountable. A [baseline.relax] entry
// needs a reason and an expiry and is printed on every audit. A
// [project].exclude entry is reported until it carries a reason. An inline
// `// swiftlint:disable` needs nothing, expires never, and appears in no report
// — and it is, by a wide margin, the most-used exemption mechanism in this
// fleet. A survey found 86 of them in project source against a handful of
// manifest-level exclusions, 45 suppressing a single rule.
//
// That asymmetry is the same one this tool has already closed twice, and the
// argument is unchanged: an exemption nobody can see is one nobody reviews.
//
// Two distinctions do the real work, because the raw count misleads in both
// directions.
//
// SCOPE. `disable:next` suppresses one line. A bare `disable` suppresses the
// rest of the FILE until a matching `enable`, so a single one can hide any
// number of violations — and the survey found twelve unbounded suppressions of
// force_try in one project, which is a very different fact from "twelve
// force_try suppressions".
//
// REASON. A suppression carrying "vendor SDK, no public init; injected below"
// is a decision. One carrying nothing is a mystery that the next reader has to
// re-derive from the code, and usually will not.
package suppress

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/skipdirs"
)

// Scope is how far a suppression reaches.
type Scope string

const (
	// Line suppresses a single line (`:next`, `:this`, `:previous`).
	Line Scope = "line"
	// File suppresses until a matching `enable`, or the end of the file.
	// Unbounded: one of these can hide any number of violations.
	File Scope = "file"
)

// Suppression is one inline exemption.
type Suppression struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Rules  []string `json:"rules"`
	Scope  Scope    `json:"scope"`
	Reason string   `json:"reason,omitempty"`
}

// Summary is the per-project rollup carried in a fleet report.
//
// A summary rather than every suppression: the fleet snapshot is diffed between
// runs, and 86 entries of churn would bury the two numbers that matter.
type Summary struct {
	Total int `json:"total"`
	// FileScoped counts unbounded suppressions.
	FileScoped int `json:"file_scoped"`
	// Unattributed counts suppressions carrying no reason.
	Unattributed int            `json:"unattributed"`
	ByRule       map[string]int `json:"by_rule,omitempty"`
}

// linter matches one tool's inline-suppression comment.
type linter struct {
	name string
	exts map[string]bool
	re   *regexp.Regexp
}

// linters is deliberately a list rather than one pattern. Every ecosystem has
// this mechanism under a different name, and the scope/reason distinctions are
// the same in each — adding one is a row here, not a new concept.
var linters = []linter{
	{
		name: "swiftlint",
		exts: map[string]bool{".swift": true},
		// Group 1: the scope suffix (next/this/previous), empty for file scope.
		// Group 2: the comma-separated rule list.
		// Group 3: whatever follows, which is where a reason lives.
		re: regexp.MustCompile(`swiftlint:disable(?::(next|this|previous))?\s+([A-Za-z0-9_]+(?:\s*,\s*[A-Za-z0-9_]+)*)\s*(.*)$`),
	},
	{
		name: "biome",
		exts: map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".css": true},
		// biome-ignore is always single-line, and its syntax REQUIRES an
		// explanation after the colon — so a reason is usually present, and its
		// absence is worth reporting when it is not.
		re: regexp.MustCompile(`biome-ignore\s+([A-Za-z0-9/]+)\s*:?\s*(.*)$`),
	},
}

// Scan walks root and returns every inline suppression found in project source.
//
// Vendored, build and worktree directories are skipped via internal/skipdirs:
// a naive walk of one project returned 5,634 hits, almost all of them inside
// dependency checkouts and DerivedData. Counting other people's suppressions as
// your own makes the number useless, and alarming.
func Scan(root string) ([]Suppression, error) {
	var out []Suppression
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not worth failing a whole sweep over.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipdirs.Skip(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, l := range linters {
			if !l.exts[ext] {
				continue
			}
			found, err := scanFile(path, root, l)
			if err != nil {
				return nil // unreadable file: skip it, do not abort the walk
			}
			out = append(out, found...)
			break
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func scanFile(path, root string, l linter) ([]Suppression, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	var out []Suppression
	for i, line := range strings.Split(string(data), "\n") {
		m := l.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		s := Suppression{Path: rel, Line: i + 1, Scope: File}
		var rules, trailing string
		if l.name == "swiftlint" {
			if m[1] != "" {
				s.Scope = Line
			}
			rules, trailing = m[2], m[3]
		} else {
			// biome-ignore is inherently single-line.
			s.Scope = Line
			rules, trailing = m[1], m[2]
		}
		for _, r := range strings.Split(rules, ",") {
			if r = strings.TrimSpace(r); r != "" {
				s.Rules = append(s.Rules, r)
			}
		}
		s.Reason = cleanReason(trailing)
		out = append(out, s)
	}
	return out, nil
}

// cleanReason strips the punctuation a reason is usually introduced with, so
// "- vendor SDK" and ": vendor SDK" and "vendor SDK" all read the same.
func cleanReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "-–—:/ \t")
	return strings.TrimSpace(s)
}

// Summarise rolls suppressions up for a fleet report.
func Summarise(ss []Suppression) Summary {
	sum := Summary{ByRule: map[string]int{}}
	for _, s := range ss {
		sum.Total++
		if s.Scope == File {
			sum.FileScoped++
		}
		if s.Reason == "" {
			sum.Unattributed++
		}
		for _, r := range s.Rules {
			sum.ByRule[r]++
		}
	}
	if len(sum.ByRule) == 0 {
		sum.ByRule = nil
	}
	return sum
}

// TopRules returns the most-suppressed rules, most first, for a report line.
func (s Summary) TopRules(n int) []string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(s.ByRule))
	for k, v := range s.ByRule {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	out := make([]string, 0, n)
	for i, e := range all {
		if i == n {
			break
		}
		out = append(out, e.k)
	}
	return out
}
