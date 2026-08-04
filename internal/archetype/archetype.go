// Package archetype loads the lacquer's named project archetypes — the baseline
// toolset for a kind of app.
//
// Detection can only see what already exists, which makes it useless at the one
// moment the stack decision is actually being made: while the idea is still a
// brief and a PCD. A project that says "an iOS app with a Supabase backend" in
// its PCD should be able to say the same thing to `lacquer init` and get the
// hooks, configs, CI, and CLAUDE regions for both halves on day zero — rather
// than onboarding whatever half happened to be written first and hoping someone
// re-runs detection later. (Nobody re-ran it. See internal/detect.Drift.)
package archetype

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Dir is the archetype directory within a lacquer checkout.
const Dir = "archetypes"

// nameRe restricts archetype names to a strict lowercase-kebab allowlist. The
// name is joined into a filesystem path, so anything outside this set is
// rejected rather than cleaned.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Archetype is a named starting shape: a description and the components a
// project of that kind has.
type Archetype struct {
	Name        string             `toml:"-"`
	Description string             `toml:"description"`
	Components  []config.Component `toml:"component"`
}

// Load reads one archetype by name from lacquerRoot.
func Load(lacquerRoot, name string) (Archetype, error) {
	if !nameRe.MatchString(name) {
		return Archetype{}, fmt.Errorf("invalid stack name %q (expected lowercase-kebab, e.g. \"ios-supabase\")", name)
	}
	path := filepath.Join(lacquerRoot, Dir, name+".toml")
	a := Archetype{Name: name}
	if _, err := toml.DecodeFile(path, &a); err != nil {
		if os.IsNotExist(err) {
			known, lerr := Names(lacquerRoot)
			if lerr == nil && len(known) > 0 {
				return Archetype{}, fmt.Errorf("unknown stack %q (known stacks: %s)", name, strings.Join(known, ", "))
			}
		}
		return Archetype{}, fmt.Errorf("load stack %q: %w", name, err)
	}
	a.Name = name
	if len(a.Components) == 0 {
		return Archetype{}, fmt.Errorf("stack %q declares no components", name)
	}
	return a, nil
}

// All returns every archetype the lacquer ships, sorted by name. A missing
// archetypes/ directory is not an error — it yields no archetypes.
func All(lacquerRoot string) ([]Archetype, error) {
	names, err := Names(lacquerRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Archetype, 0, len(names))
	for _, n := range names {
		a, err := Load(lacquerRoot, n)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Names lists the archetype names available in lacquerRoot, sorted.
func Names(lacquerRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(lacquerRoot, Dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		n := strings.TrimSuffix(e.Name(), ".toml")
		if nameRe.MatchString(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Merge unions an archetype's components with the ones detection found.
//
// Detected components win: a project that already has code is described by that
// code, and the archetype only fills in the halves that do not exist yet. Where
// both name the same path, the profile lists are unioned so an archetype can add
// a stack to a directory detection already knows about.
//
// An archetype profile that detection already placed somewhere else is dropped
// rather than added at the archetype's path. A manifest may declare a profile
// only once (config.Load), so emitting both would write a manifest that fails to
// load — `lacquer init --stack ios-supabase` on a repo whose Xcode project sits
// at the root must not produce an `ios` component at both "." and "ios".
func Merge(detected []config.Component, a Archetype) []config.Component {
	byPath := map[string][]string{}
	var order []string
	seen := func(p string) bool { _, ok := byPath[p]; return ok }
	claimed := map[string]bool{} // profile -> already placed by detection
	for _, c := range detected {
		if !seen(c.Path) {
			order = append(order, c.Path)
		}
		byPath[c.Path] = union(byPath[c.Path], c.Profiles)
		for _, p := range c.Profiles {
			claimed[p] = true
		}
	}
	for _, c := range a.Components {
		var add []string
		for _, p := range c.Profiles {
			if !claimed[p] {
				add = append(add, p)
			}
		}
		if len(add) == 0 {
			continue
		}
		if !seen(c.Path) {
			order = append(order, c.Path)
		}
		byPath[c.Path] = union(byPath[c.Path], add)
	}
	sort.Strings(order)
	out := make([]config.Component, 0, len(order))
	for _, p := range order {
		profs := byPath[p]
		sort.Strings(profs)
		out = append(out, config.Component{Path: p, Profiles: profs})
	}
	return out
}

func union(a, b []string) []string {
	has := map[string]bool{}
	for _, s := range a {
		has[s] = true
	}
	for _, s := range b {
		if !has[s] {
			has[s] = true
			a = append(a, s)
		}
	}
	return a
}
