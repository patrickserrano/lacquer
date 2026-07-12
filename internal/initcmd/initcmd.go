// Package initcmd implements `lacquer init`: detect a project's components and
// write a .lacquer.toml stub for the operator to complete.
package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Run detects components under root and writes a .lacquer.toml. It refuses to
// overwrite an existing manifest. It returns a human-readable summary of what it
// wrote and which [project] values still need filling.
//
// lacquerRoot is the lacquer checkout: a detected profile is only written into
// the manifest when it actually ships there (profiles/<p>/CLAUDE.<p>.md exists).
// A detected stack with no shipping profile (e.g. rust/go today) would otherwise
// make the next `lacquer sync` fail with an opaque "no such file" — so its
// component is still recorded (with an empty profiles list) and a notice is
// printed instead.
func Run(lacquerRoot, root string) (string, error) {
	manifest, err := safepath.Resolve(root, ".lacquer.toml")
	if err != nil {
		return "", fmt.Errorf("resolve .lacquer.toml: %w", err)
	}
	// Lstat (not Stat): a dangling symlink must read as "present" so os.WriteFile
	// can never follow it and create a file outside the project root.
	if fi, err := os.Lstat(manifest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink: %s", manifest)
		}
		return "", fmt.Errorf(".lacquer.toml already exists at %s; refusing to overwrite", manifest)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	comps, derived, err := detect.Components(root)
	if err != nil {
		return "", fmt.Errorf("detect components: %w", err)
	}

	// Keep only profiles the lacquer actually ships; collect a notice for each
	// dropped one so the operator knows why a detected stack isn't wired up.
	var notices []string
	for i := range comps {
		kept := comps[i].Profiles[:0:0]
		for _, p := range comps[i].Profiles {
			if profileShips(lacquerRoot, p) {
				kept = append(kept, p)
				continue
			}
			notices = append(notices, fmt.Sprintf(
				"NOTE: component %q detected as %q — no lacquer profile ships for it yet; add one under profiles/%s/.",
				comps[i].Path, p, p))
		}
		comps[i].Profiles = kept
	}

	name := derived.ProjectName
	if name == "" {
		name = filepath.Base(root)
	}

	var b strings.Builder
	b.WriteString("[project]\n")
	fmt.Fprintf(&b, "name = %q\n", name)
	fmt.Fprintf(&b, "project_name = %q\n", derived.ProjectName)
	fmt.Fprintf(&b, "scheme = %q\n", derived.Scheme)
	fmt.Fprintf(&b, "xcodeproj = %q\n", derived.Xcodeproj)
	fmt.Fprintf(&b, "swift_version = %q\n", derived.SwiftVersion)
	b.WriteString("bundle_id = \"\"\n")
	b.WriteString("asc_app_id = \"\"\n")
	b.WriteString("github_org = \"\"\n")
	// Agent tools to provision skills for. New projects default to all supported
	// tools; trim this list to opt out (an omitted field means claude-only).
	b.WriteString("tools = [\"claude\", \"codex\", \"antigravity\"]\n")
	for _, c := range comps {
		b.WriteString("\n[[component]]\n")
		fmt.Fprintf(&b, "path = %q\n", c.Path)
		fmt.Fprintf(&b, "profiles = [%s]\n", quoteList(c.Profiles))
	}

	if err := os.WriteFile(manifest, []byte(b.String()), 0o644); err != nil {
		return "", err
	}

	briefWritten, err := writeBriefStub(root, name)
	if err != nil {
		return "", err
	}

	var s strings.Builder
	if len(comps) == 0 {
		s.WriteString("No components detected (no .xcodeproj / package.json / Cargo.toml / go.mod found).\n")
	} else {
		s.WriteString("Detected components:\n")
		for _, c := range comps {
			if len(c.Profiles) == 0 {
				fmt.Fprintf(&s, "  %s -> (no shipping profile)\n", c.Path)
			} else {
				fmt.Fprintf(&s, "  %s -> %s\n", c.Path, strings.Join(c.Profiles, ", "))
			}
		}
	}
	fmt.Fprintf(&s, "Wrote %s\n", manifest)
	if briefWritten {
		s.WriteString("Wrote docs/brief.md (stub) — paste the project brief there.\n")
	}
	for _, n := range notices {
		s.WriteString(n)
		s.WriteString("\n")
	}
	s.WriteString("Fill any blank [project] values (e.g. bundle_id, asc_app_id), then run `lacquer sync`.")
	return s.String(), nil
}

// profileShips reports whether lacquerRoot actually ships profile p — i.e. the
// CLAUDE body that sync/audit read (profiles/<p>/CLAUDE.<p>.md) exists. This is
// the exact file whose absence makes a later `lacquer sync` fail, so it is the
// precise gate for whether a detected profile should be written into the manifest.
func profileShips(lacquerRoot, p string) bool {
	body := filepath.Join(lacquerRoot, "profiles", p, "CLAUDE."+p+".md")
	if _, err := os.Stat(body); err == nil {
		return true
	}
	return false
}

// quoteList renders a string slice as the body of a TOML array: `"a", "b"`, or
// the empty string for an empty slice (yielding `profiles = []`).
func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(quoted, ", ")
}

// writeBriefStub creates docs/brief.md with a starter template when it does not
// already exist. It reports whether it wrote the file. An existing brief is never
// overwritten — the brief is project-owned, human-authored content.
func writeBriefStub(root, name string) (bool, error) {
	// safepath.Resolve refuses a docs/ symlink that escapes the project root;
	// the Lstat checks below refuse any remaining symlink at the final elements.
	brief, err := safepath.Resolve(root, filepath.Join("docs", "brief.md"))
	if err != nil {
		return false, fmt.Errorf("resolve docs/brief.md: %w", err)
	}
	if fi, err := os.Lstat(brief); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("refusing to write through symlink: %s", brief)
		}
		return false, nil // already present — leave it alone
	} else if !os.IsNotExist(err) {
		return false, err
	}
	// The docs dir itself must not be a symlink either: MkdirAll/WriteFile would
	// follow it into whatever it points at.
	dir := filepath.Dir(brief)
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refusing to write through symlink: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	stub := fmt.Sprintf(briefTemplate, name)
	if err := os.WriteFile(brief, []byte(stub), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// briefTemplate is the starter brief scaffold. %s is the project name. It mirrors
// the doc taxonomy in CLAUDE.core.md: the brief is the source of truth the PRD is
// derived from.
const briefTemplate = `# %s — Product Brief

*Draft v0.1*

## One-liner

<One sentence: what it is and why it matters.>

## The problem

<What's broken today and for whom.>

## Who it's for

<Primary user, and any secondary/monetization persona.>

## Goals

<User goals and business goals.>

## Non-goals (for v1)

<What you are deliberately NOT building yet.>

## The product

<The hero experience and the must-have (P0) requirements.>

## Success metrics

<Leading and lagging signals that tell you it's working.>

## Risks & mitigations

<What could sink it and how you de-risk each.>

## Open questions

<Unknowns to resolve before/while building.>

## Roadmap

<v1 / v1.5 / v2 phasing.>
`
