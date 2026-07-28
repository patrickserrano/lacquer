// Package baseline asserts project-level invariants — Swift language mode,
// warnings-as-errors, strict concurrency — that the lacquer requires of a
// project, as opposed to merely recording what a project already declares.
//
// It exists because lacquer previously had three buckets (content it renders,
// tokens it substitutes, drift it detects) and no way to say "this project is
// wrong". Language mode lived in the token bucket, which is descriptive by
// design: `swift_version` was scraped from the project and its only consumer was
// swiftformat's --swiftversion, so a project could sit on an old language mode
// indefinitely, and could move to a new one without the manifest noticing.
//
// The standard is lacquer-owned (profiles/<profile>/baseline.toml) so every
// project inherits it by default. A project may only *relax* it, with a required
// reason and expiry, which keeps the debt visible and greppable instead of
// letting a per-project value quietly redefine the standard.
package baseline

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Spec is the asserted standard for one profile.
type Spec struct {
	SwiftVersion      string `toml:"swift_version"`
	WarningsAsErrors  bool   `toml:"warnings_as_errors"`
	StrictConcurrency string `toml:"strict_concurrency"`
}

// specFile is the on-disk shape of profiles/<profile>/baseline.toml.
type specFile struct {
	Baseline Spec `toml:"baseline"`
}

// LoadSpec reads the standard a profile asserts. ok is false when the profile
// ships no baseline.toml at all — a profile without one (web, supabase) runs no
// baseline checks, which must not be an error.
func LoadSpec(lacquerRoot, profile string) (Spec, bool, error) {
	path := filepath.Join(lacquerRoot, "profiles", profile, "baseline.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Spec{}, false, nil
	}
	if err != nil {
		return Spec{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var f specFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return Spec{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Baseline, true, nil
}
