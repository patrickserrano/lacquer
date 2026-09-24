package ratchet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Measure counts current rendered root/component CLAUDE.md files (once per
// destination, including project prose), and directives in tracked source.
// It does not render, run tools from the project, or write to its checkout.
func Measure(root string, cfg *config.Config) (map[string]int, error) {
	values := map[string]int{ClaudeLines: 0, Suppressions: 0}
	paths := map[string]bool{"CLAUDE.md": true}
	for _, c := range cfg.Components {
		if len(c.Profiles) > 0 {
			paths[filepath.Join(c.Path, "CLAUDE.md")] = true
		}
	}
	var ordered []string
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		data, err := readSource(root, path)
		if err != nil {
			return nil, fmt.Errorf("ratchet: measure %s: %w", path, err)
		}
		n := strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		values[ClaudeLines] += n
	}
	cmd := exec.Command("git", "ls-files", "-z", "--stage")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ratchet: list tracked source: %w", err)
	}
	for _, entry := range strings.Split(string(raw), "\x00") {
		if entry == "" {
			continue
		}
		header, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || fields[2] != "0" {
			return nil, fmt.Errorf("ratchet: unresolved or invalid Git index entry %q", entry)
		}
		// Git symlink blobs contain a path, not source. Count their tracked target
		// once, if present, rather than counting each alias to that target again.
		if fields[0] == "120000" || fields[0] == "160000" {
			continue
		}

		switch strings.ToLower(filepath.Ext(path)) {
		case ".swift", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts", ".vue", ".svelte", ".css", ".jsonc":
		default:
			continue
		}
		data, err := readSource(root, path)
		// An unstaged deletion is not a verified improvement. Stage deletions before
		// tightening; otherwise a missing/unreadable file must not lower the ceiling.
		if err != nil {
			return nil, fmt.Errorf("ratchet: measure %s: %w", path, err)
		}
		values[Suppressions] += countSuppressions(string(data))
	}
	return values, nil
}

func readSource(root, rel string) ([]byte, error) {
	path, err := safepath.Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", rel)
	}
	return os.ReadFile(path)
}

var directive = regexp.MustCompile(`^(swiftlint:disable(?::(?:next|this|previous))?|biome-ignore(?:-all|-start|-end)?|eslint-disable(?:-next-line|-line)?)(?:\s|$)`)

// Count only directive comments, not examples in strings or explanatory prose.
// Justifications use the linters' usual delimiters: Biome ':', ESLint '--',
// SwiftLint '//'. SwiftLint also accepts '--' for consistency with ESLint.
func countSuppressions(source string) int {
	n := 0
	for i := 0; i < len(source); {
		if source[i] == '"' || source[i] == '\'' || source[i] == '`' {
			quote := source[i]
			i++
			for i < len(source) {
				if source[i] == '\\' {
					i += 2
					continue
				}
				if source[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			end := strings.IndexByte(source[i:], '\n')
			if end < 0 {
				end = len(source) - i
			}
			n += unjustified(source[i+2 : i+end])
			i += end
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				end = len(source) - i - 2
			}
			n += unjustified(source[i+2 : i+2+end])
			i += 2 + end
			if i < len(source) {
				i += 2
			}
			continue
		}
		i++
	}
	return n
}

func unjustified(comment string) int {
	n := 0
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "*"))
		match := directive.FindStringSubmatch(line)
		if match == nil || match[1] == "biome-ignore-end" {
			continue
		}
		rest := line[len(match[1]):]
		delimiters := []string{"--"}
		if strings.HasPrefix(match[1], "biome-") {
			delimiters = []string{":"}
		}
		if strings.HasPrefix(match[1], "swiftlint:") {
			delimiters = append(delimiters, "//", "-", ":", "–", "—")
		}
		justified := false
		for _, d := range delimiters {
			if _, reason, ok := strings.Cut(rest, d); ok && strings.Trim(reason, "-–—:/ \t*\r") != "" {
				justified = true
			}
		}
		if !justified {
			n++
		}
	}
	return n
}
