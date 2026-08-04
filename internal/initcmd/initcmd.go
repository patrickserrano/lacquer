// Package initcmd implements `lacquer init`: detect a project's components and
// write a .lacquer.toml stub for the operator to complete.
package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/archetype"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/safepath"
	"github.com/patrickserrano/lacquer/internal/skillsuggest"
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
//
// stack names an archetype under lacquerRoot/archetypes (see internal/archetype)
// and may be empty. It adds the components a project of that kind has but does
// not have yet, so the stack a brief/PCD decided on is the stack the manifest
// declares from the first commit — rather than whichever half of it happened to
// be written before someone ran init.
func Run(lacquerRoot, root, stack string) (string, error) {
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

	// Fold in the declared stack before the ships-filter below, so an archetype
	// naming a profile the lacquer doesn't ship is reported the same way a
	// detected one is.
	var notices []string
	fromStack := map[string]bool{}
	if stack != "" {
		a, err := archetype.Load(lacquerRoot, stack)
		if err != nil {
			return "", err
		}
		before := map[string]bool{}
		for _, c := range comps {
			before[c.Path] = true
		}
		comps = archetype.Merge(comps, a)
		for _, c := range comps {
			if !before[c.Path] {
				fromStack[c.Path] = true
			}
		}
	}

	// Keep only profiles the lacquer actually ships; collect a notice for each
	// dropped one so the operator knows why a detected stack isn't wired up.
	for i := range comps {
		kept := comps[i].Profiles[:0:0]
		for _, p := range comps[i].Profiles {
			if detect.ProfileShips(lacquerRoot, p) {
				kept = append(kept, p)
				continue
			}
			notices = append(notices, fmt.Sprintf(
				"NOTE: component %q is %q — no lacquer profile ships for it yet, so nothing gates it. "+
					"`lacquer audit` will keep reporting this until profiles/%s/ exists.",
				comps[i].Path, p, p))
		}
		comps[i].Profiles = kept
	}

	name := derived.ProjectName
	if name == "" {
		name = filepath.Base(root)
	}

	// swift_version is ASSERTED, not observed. It used to be whatever the project
	// already declared, which meant onboarding a project on a stale language mode
	// recorded the stale mode and then fed it to swiftformat's --swiftversion
	// forever. Detection now answers only "does the project already agree?" — a
	// diagnostic printed for the operator, never the value written.
	swiftVersion := derived.SwiftVersion
	if asserted, note := assertedSwiftVersion(lacquerRoot, comps, derived.SwiftVersion); asserted != "" {
		swiftVersion = asserted
		if note != "" {
			notices = append(notices, note)
		}
	}

	// Suggest third-party skill packages by scanning each component's actual
	// Swift imports (see internal/skillsuggest) — a starting point for
	// [project].skills, not a decision; trim or extend it freely.
	var suggested []string
	seen := map[string]bool{}
	for _, c := range comps {
		found, err := skillsuggest.Suggest(filepath.Join(root, c.Path))
		if err != nil {
			continue // best-effort: a suggestion failure never blocks init
		}
		for _, s := range found {
			if !seen[s] {
				seen[s] = true
				suggested = append(suggested, s)
			}
		}
	}
	sort.Strings(suggested)

	var b strings.Builder
	b.WriteString("[project]\n")
	fmt.Fprintf(&b, "name = %q\n", name)
	fmt.Fprintf(&b, "project_name = %q\n", derived.ProjectName)
	fmt.Fprintf(&b, "scheme = %q\n", derived.Scheme)
	fmt.Fprintf(&b, "xcodeproj = %q\n", derived.Xcodeproj)
	fmt.Fprintf(&b, "swift_version = %q\n", swiftVersion)
	b.WriteString("bundle_id = \"\"\n")
	b.WriteString("asc_app_id = \"\"\n")
	b.WriteString("github_org = \"\"\n")
	if stack != "" {
		// Provenance: which archetype this project started from. Nothing reads it
		// to decide behaviour — the [[component]] blocks below are what sync acts
		// on — but it records that the shape was chosen rather than inferred.
		fmt.Fprintf(&b, "stack = %q\n", stack)
	}
	// Agent tools to provision skills for. New projects default to all supported
	// tools; trim this list to opt out (an omitted field means claude-only).
	b.WriteString("tools = [\"claude\", \"codex\", \"antigravity\"]\n")
	if len(suggested) > 0 {
		// Third-party skill packages, installed via `lacquer skills` (the
		// `skills` CLI, https://github.com/vercel-labs/skills). Suggested from
		// this project's actual imports — review before running `lacquer skills`.
		fmt.Fprintf(&b, "skills = [%s]\n", quoteList(suggested))
	}
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
		s.WriteString("No components detected (no .xcodeproj / Package.swift / package.json / Cargo.toml / go.mod found).\n")
	} else {
		s.WriteString("Components:\n")
		for _, c := range comps {
			// Mark the ones the archetype added, so "this directory does not exist
			// yet" is visible rather than looking like a detection result.
			origin := ""
			if fromStack[c.Path] {
				origin = fmt.Sprintf("  (from --stack %s; not on disk yet)", stack)
			}
			if len(c.Profiles) == 0 {
				fmt.Fprintf(&s, "  %s -> (no shipping profile)%s\n", c.Path, origin)
			} else {
				fmt.Fprintf(&s, "  %s -> %s%s\n", c.Path, strings.Join(c.Profiles, ", "), origin)
			}
		}
	}
	fmt.Fprintf(&s, "Wrote %s\n", manifest)
	if briefWritten {
		s.WriteString("Wrote docs/brief.md (stub) — paste the project brief there.\n")
	}
	if len(suggested) > 0 {
		fmt.Fprintf(&s, "Suggested %d skill(s) from actual imports (review, then `lacquer skills`):\n", len(suggested))
		for _, sk := range suggested {
			fmt.Fprintf(&s, "  %s\n", sk)
		}
	}
	for _, n := range notices {
		s.WriteString(n)
		s.WriteString("\n")
	}
	s.WriteString("Fill any blank [project] values (e.g. bundle_id, asc_app_id), then run `lacquer sync`.")
	return s.String(), nil
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

## Stack

<The archetype this is — run ` + "`lacquer init --list-stacks`" + ` — e.g. "ios-supabase".
Name it here, before the repo exists: it decides what gates the code from the
first commit, and a stack that arrives later arrives ungated.>

## Success metrics

<Leading and lagging signals that tell you it's working.>

## Risks & mitigations

<What could sink it and how you de-risk each.>

## Open questions

<Unknowns to resolve before/while building.>

## Roadmap

<v1 / v1.5 / v2 phasing.>
`

// assertedSwiftVersion returns the Swift language mode the lacquer asserts for
// this project, plus a notice when the project currently declares something else.
//
// It returns "" when no detected profile asserts a baseline, in which case the
// caller keeps the detected value — a profile that asserts nothing must behave
// exactly as it did before this existed.
func assertedSwiftVersion(lacquerRoot string, comps []config.Component, detected string) (asserted, notice string) {
	for _, c := range comps {
		for _, p := range c.Profiles {
			spec, ok, err := baseline.LoadSpec(lacquerRoot, p)
			if err != nil || !ok || spec.SwiftVersion == "" {
				continue
			}
			if detected != "" && detected != spec.SwiftVersion {
				notice = fmt.Sprintf(
					"NOTE: this project declares Swift %s but the lacquer baseline asserts Swift %s — "+
						"the manifest records %s. Migrate the project, or add a time-boxed "+
						"[baseline.relax].swift_version entry with a reason. See `lacquer audit`.",
					detected, spec.SwiftVersion, spec.SwiftVersion)
			}
			return spec.SwiftVersion, notice
		}
	}
	return "", ""
}
