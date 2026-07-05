// Package initcmd implements `harness init`: detect a project's components and
// write a .harness.toml stub for the operator to complete.
package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/detect"
	"github.com/patrickserrano/harness/internal/safepath"
)

// Run detects components under root and writes a .harness.toml. It refuses to
// overwrite an existing manifest. It returns a human-readable summary of what it
// wrote and which [project] values still need filling.
func Run(root string) (string, error) {
	manifest, err := safepath.Resolve(root, ".harness.toml")
	if err != nil {
		return "", fmt.Errorf("resolve .harness.toml: %w", err)
	}
	// Lstat (not Stat): a dangling symlink must read as "present" so os.WriteFile
	// can never follow it and create a file outside the project root.
	if fi, err := os.Lstat(manifest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink: %s", manifest)
		}
		return "", fmt.Errorf(".harness.toml already exists at %s; refusing to overwrite", manifest)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	comps, derived, err := detect.Components(root)
	if err != nil {
		return "", fmt.Errorf("detect components: %w", err)
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
		fmt.Fprintf(&b, "profiles = [%q]\n", c.Profiles[0])
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
			fmt.Fprintf(&s, "  %s -> %s\n", c.Path, c.Profiles[0])
		}
	}
	fmt.Fprintf(&s, "Wrote %s\n", manifest)
	if briefWritten {
		s.WriteString("Wrote docs/brief.md (stub) — paste the project brief there.\n")
	}
	s.WriteString("Fill any blank [project] values (e.g. bundle_id, asc_app_id), then run `harness sync`.")
	return s.String(), nil
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
