// Package adoptcmd implements `lacquer adopt`: record stacks that appeared in a
// project after `lacquer init` ran into its .lacquer.toml.
//
// This is the repair half of internal/detect.Drift. Detection ran once, at
// onboarding, and a project that grew a stack afterwards stayed unmanaged for it
// with nothing reporting the gap. Drift reports it; adopt closes it.
package adoptcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Run re-detects components under projectRoot and records every stack the
// lacquer ships a profile for that the manifest does not yet declare. It returns
// a human-readable summary and whether the manifest was changed.
//
// It only ever ADDS. Removing a profile is a real decision with real
// consequences (it orphans synced files and silently drops a stack from every
// gate), so a stack that has genuinely gone away is left for a human to delete.
func Run(lacquerRoot, projectRoot string) (string, bool, error) {
	manifestPath, err := safepath.Resolve(projectRoot, ".lacquer.toml")
	if err != nil {
		return "", false, fmt.Errorf("resolve .lacquer.toml: %w", err)
	}
	if fi, err := os.Lstat(manifestPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("refusing to write through symlink: %s", manifestPath)
	}
	cfg, err := config.Load(manifestPath)
	if err != nil {
		return "", false, fmt.Errorf("load manifest: %w", err)
	}
	findings, err := detect.Drift(lacquerRoot, projectRoot, cfg)
	if err != nil {
		return "", false, fmt.Errorf("re-detect components: %w", err)
	}

	var s strings.Builder
	for _, f := range detect.Unsupported(findings) {
		fmt.Fprintf(&s, "skipped: %s -> %s (no lacquer profile ships for it; nothing gates this stack)\n", f.Path, f.Profile)
	}

	adoptable := detect.Adoptable(findings)
	if len(adoptable) == 0 {
		s.WriteString("nothing to adopt: every stack on disk is already declared.\n")
		return s.String(), false, nil
	}

	// Group by component path so a component gaining two profiles is one edit.
	byPath := map[string][]string{}
	var paths []string
	for _, f := range adoptable {
		if _, seen := byPath[f.Path]; !seen {
			paths = append(paths, f.Path)
		}
		byPath[f.Path] = append(byPath[f.Path], f.Profile)
	}
	sort.Strings(paths)

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", false, err
	}
	text := string(data)
	for _, p := range paths {
		profs := byPath[p]
		sort.Strings(profs)
		updated, err := addProfiles(text, p, profs)
		if err != nil {
			return "", false, err
		}
		text = updated
		fmt.Fprintf(&s, "adopted: %s -> %s\n", p, strings.Join(profs, ", "))
	}

	// Re-parse before writing: the edit is textual, so this is what proves it
	// produced a manifest that still loads (and still satisfies the one-component-
	// per-profile rule) rather than one that fails on the next command.
	tmp, err := os.CreateTemp(filepath.Dir(manifestPath), ".lacquer.toml.adopt-*")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if _, err := config.Load(tmpName); err != nil {
		return "", false, fmt.Errorf("the adopted manifest would not load (%w); .lacquer.toml left unchanged — add the components by hand", err)
	}
	if err := os.WriteFile(manifestPath, []byte(text), 0o644); err != nil {
		return "", false, err
	}
	return s.String(), true, nil
}

// componentHeaderRe matches a `[[component]]` table header at the start of a line.
var componentHeaderRe = regexp.MustCompile(`^\s*\[\[component\]\]\s*$`)

// tableHeaderRe matches any TOML table header, used to find where a component
// block ends.
var tableHeaderRe = regexp.MustCompile(`^\s*\[`)

// keyRe extracts a `key = value` assignment's key and value halves.
var keyRe = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(.*)$`)

// addProfiles returns text with profs added to the component at path: either by
// rewriting that component's `profiles` line, or by appending a new
// `[[component]]` block when no component has that path.
//
// It edits the TOML as text rather than decode-and-re-encode because a manifest
// carries hand-written comments explaining every non-obvious choice in it, and
// BurntSushi/toml cannot round-trip them. A rewrite would silently delete the
// reasoning — which, in a repo whose whole thesis is that decisions must stay
// attributable, is not an acceptable trade for tidier code.
func addProfiles(text, path string, profs []string) (string, error) {
	lines := strings.Split(text, "\n")
	start, end := findComponent(lines, path)
	if start < 0 {
		return appendComponent(text, path, profs), nil
	}
	for i := start; i < end; i++ {
		m := keyRe.FindStringSubmatch(lines[i])
		if m == nil || m[1] != "profiles" {
			continue
		}
		val := strings.TrimSpace(stripComment(m[2]))
		if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
			// A multi-line array. Rare, and getting it wrong corrupts the manifest,
			// so refuse and say exactly what to type instead.
			return "", fmt.Errorf("component %q has a multi-line `profiles` array; add %s to it by hand, then re-run",
				path, strings.Join(quoted(profs), ", "))
		}
		existing := parseArray(val)
		merged := append(existing, profs...)
		lines[i] = fmt.Sprintf("profiles = [%s]", strings.Join(quoted(merged), ", "))
		return strings.Join(lines, "\n"), nil
	}
	// The component exists but declares no `profiles` key at all (`init` writes
	// one for a stack with no shipping profile). Insert one.
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:end]...)
	out = append(out, fmt.Sprintf("profiles = [%s]", strings.Join(quoted(profs), ", ")))
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

// findComponent returns the [start, end) line range of the `[[component]]` block
// whose `path` is the given one, or (-1, -1).
func findComponent(lines []string, path string) (int, int) {
	for i, ln := range lines {
		if !componentHeaderRe.MatchString(ln) {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if tableHeaderRe.MatchString(lines[j]) {
				end = j
				break
			}
		}
		for j := i + 1; j < end; j++ {
			m := keyRe.FindStringSubmatch(lines[j])
			if m != nil && m[1] == "path" && unquote(strings.TrimSpace(stripComment(m[2]))) == path {
				return i + 1, end
			}
		}
	}
	return -1, -1
}

// appendComponent adds a whole new component block at the end of the manifest.
func appendComponent(text, path string, profs []string) string {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + fmt.Sprintf("\n[[component]]\npath = %q\nprofiles = [%s]\n",
		path, strings.Join(quoted(profs), ", "))
}

// stripComment drops a trailing `# ...` comment from a value. Values written by
// `init` (and by hand in this fleet) are simple quoted strings and flat arrays
// with no `#` inside them, so a naive cut is safe here; findComponent's match is
// confirmed against the PARSED manifest by the caller's re-load before write.
func stripComment(v string) string {
	if i := strings.Index(v, "#"); i >= 0 {
		return v[:i]
	}
	return v
}

func unquote(v string) string { return strings.Trim(v, `"'`) }

// parseArray splits a flat TOML string array body into its elements.
func parseArray(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]")
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, unquote(part))
	}
	return out
}

// quoted renders items as sorted, deduplicated, TOML-quoted strings.
func quoted(items []string) []string {
	seen := map[string]bool{}
	var uniq []string
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	sort.Strings(uniq)
	out := make([]string, len(uniq))
	for i, s := range uniq {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
