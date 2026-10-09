// Package fragments renders and validates the project-owned additions a
// manifest may make to three managed configs: SwiftLint custom rules, Biome
// per-path overrides and TypeDoc entry points.
//
// Every one of them is an ADDITION. A fragment may tighten what the profile
// enforces and may point the docs build at a project's real public surface; it
// may not switch a profile rule off, reconfigure one, or touch a rule a doctor
// probe asserts by name. The last part is what keeps `lacquer doctor` meaningful:
// a probe that writes known-bad code and asserts the check rejects it says
// nothing once a project fragment can quietly stop the check from rejecting it.
//
// The fragments live in .lacquer.toml rather than in project-owned child files
// beside the configs. Every shipped CI workflow wakes the drift audit when
// .lacquer.toml changes, and the audit renders through here, so an edit that
// tries to loosen the profile is refused on the pull request that makes it. A
// child file outside that path filter would be read by the linter on every run
// and validated by nothing.
package fragments

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/BurntSushi/toml"
)

// Probe patterns that name a rule in a doctor probe's expect_output. SwiftLint
// prints a violation's rule id in parentheses, which a probe's regex escapes
// (`\(line_length\)`); Biome prints `lint/<group>/<rule>`.
var (
	swiftLintProbed = regexp.MustCompile(`\\\(([a-z_]+)\\\)`)
	biomeProbed     = regexp.MustCompile(`lint/([a-zA-Z]+)/([a-zA-Z]+)`)
)

// probedRules returns, sorted, every rule id a profile's doctor.toml asserts by
// name. It fails when there are none: a profile whose probes name no rule would
// make the probed-rule guard below vacuous, and that must be loud rather than
// read as "nothing to protect".
func probedRules(doctorTOML []byte, re *regexp.Regexp, rule func([]string) string) ([]string, error) {
	var file struct {
		Probe []struct {
			ExpectOutput string `toml:"expect_output"`
		} `toml:"probe"`
	}
	if _, err := toml.Decode(string(doctorTOML), &file); err != nil {
		return nil, fmt.Errorf("read doctor probes: %w", err)
	}
	seen := map[string]bool{}
	for _, p := range file.Probe {
		for _, m := range re.FindAllStringSubmatch(p.ExpectOutput, -1) {
			seen[rule(m)] = true
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("the profile's doctor probes name no rule, so nothing would stop a project fragment from hollowing one out")
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out, nil
}

// SwiftLintProbedRules returns the SwiftLint rule ids the ios doctor probes
// assert, read from the profile's doctor.toml.
func SwiftLintProbedRules(doctorTOML []byte) ([]string, error) {
	return probedRules(doctorTOML, swiftLintProbed, func(m []string) string { return m[1] })
}

// BiomeProbedRules returns the Biome rules the web doctor probes assert, as
// "<group>/<rule>", read from the profile's doctor.toml.
func BiomeProbedRules(doctorTOML []byte) ([]string, error) {
	return probedRules(doctorTOML, biomeProbed, func(m []string) string { return m[1] + "/" + m[2] })
}
