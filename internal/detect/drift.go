package detect

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/patrickserrano/lacquer/internal/config"
)

// Finding is one stack detected in a project that the project's manifest does
// not declare.
//
// This exists because detection ran exactly once per project — at `lacquer
// init` — and never again. A project that grows a stack after onboarding was
// therefore unmanaged for that stack forever, with nothing anywhere reporting
// it: no CLAUDE region, no hooks, no CI, no error. needledrop bootstrapped as
// TypeScript-only on 2 Aug 2025 (correctly — it was a spike), gained a Swift
// package the next day, and still declared `profiles = ["web"]` a year later.
// Its 191 Swift tests were run by nothing at any gate.
type Finding struct {
	// Path is the component path detection assigned to the stack.
	Path string
	// Profile is the profile the stack implies.
	Profile string
	// Ships reports whether this lacquer actually ships that profile. False means
	// the project cannot adopt it yet, so the finding is a standing notice rather
	// than something the project did wrong.
	Ships bool
}

// Drift re-runs detection over root and returns every detected profile the
// manifest does not declare, sorted by path then profile.
//
// A profile already declared by ANY component counts as managed, whatever path
// it sits at. Detection picks a component path heuristically (the Swift config
// dir, the common ancestor of several packages), and re-deriving a path that
// disagrees with a hand-tuned manifest would report drift on projects that are
// perfectly configured. The question Drift answers is "is this stack managed at
// all?" — the question that was never being asked — not "is it managed where I
// would have put it?".
//
// Paths under [project].exclude are skipped: exclude already means "this path is
// not the lacquer's business", and it is the escape hatch for a detected stack a
// project deliberately keeps unmanaged (a fixture tree, a scratch package).
func Drift(lacquerRoot, root string, cfg *config.Config) ([]Finding, error) {
	comps, _, err := Components(root)
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			declared[p] = true
		}
	}
	var out []Finding
	for _, c := range comps {
		if cfg.Project.Excludes(c.Path) {
			continue
		}
		for _, p := range c.Profiles {
			if declared[p] {
				continue
			}
			out = append(out, Finding{Path: c.Path, Profile: p, Ships: ProfileShips(lacquerRoot, p)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Profile < out[j].Profile
	})
	return out, nil
}

// Adoptable returns the findings a project can act on now — the ones whose
// profile this lacquer ships. These are a hard failure: the project is running
// unmanaged code that the lacquer knows how to manage.
func Adoptable(findings []Finding) []Finding { return filterShips(findings, true) }

// Unsupported returns the findings the project cannot act on — a real stack with
// no lacquer profile behind it yet.
//
// These are reported every run and never gate. Gating on them would punish a
// project for the lacquer's gap, and the alternative — recording the component
// with an empty profile list, as `init` does — is worse: it silences the report
// while changing nothing, which is how needledrop's Swift stayed invisible.
func Unsupported(findings []Finding) []Finding { return filterShips(findings, false) }

func filterShips(findings []Finding, ships bool) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Ships == ships {
			out = append(out, f)
		}
	}
	return out
}

// ProfileShips reports whether lacquerRoot actually ships profile p — i.e. the
// CLAUDE body that sync/audit read (profiles/<p>/CLAUDE.<p>.md) exists. This is
// the exact file whose absence makes a later `lacquer sync` fail, so it is the
// precise gate for whether a detected profile can be written into a manifest.
func ProfileShips(lacquerRoot, p string) bool {
	_, err := os.Stat(filepath.Join(lacquerRoot, "profiles", p, "CLAUDE."+p+".md"))
	return err == nil
}
