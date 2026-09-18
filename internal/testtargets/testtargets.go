// Package testtargets compares the test targets an Xcode project actually has
// against the `-only-testing:` selectors the manifest produces, in BOTH
// directions.
//
// Both directions are the same defect wearing opposite signs, and both are
// invisible for the identical reason: `xcodebuild` exits 0 for a
// `-only-testing:` selector that matches nothing.
//
//   - A selector naming a target the project does not have. Measured in `steps`:
//     the manifest derived `test_target` from the product's DISPLAY name
//     ("Steps Lite"), producing `Steps LiteTests`, which exists nowhere. 91 unit
//     tests were selected by a name matching nothing.
//   - A test target the project has that no selector names. Measured in
//     dailybread, three times: a widget suite, a watch suite that did not even
//     compile under Swift 6, and a UI suite that had never run at all. Each was
//     found months later and by accident.
//
// CI's "Verify Test Selectors Matched" step covers only the first half, and only
// at the point a three-minute Mac job has already run. This is a project-shape
// question, so it belongs where it is cheap: `lacquer audit`.
package testtargets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Target is one test bundle the project can select: a native target in
// project.pbxproj, or a test target of a local Swift package it references (see
// packages.go).
type Target struct {
	Name string
	UI   bool // a ui-testing bundle rather than a unit-test one
	// Package is the relativePath of the local Swift package this test target
	// was read from, and "" for a native target of project.pbxproj.
	Package string
	// Unread is set on an entry that is NOT a target: a referenced package the
	// audit could not fully read, and why. It exists so "I could not look" can
	// reach Compare as that, rather than as an absence it would read as "it is
	// not there". Such an entry has no Name.
	Unread string
}

// native reports whether t is a test target declared by project.pbxproj itself.
func (t Target) native() bool { return t.Package == "" && t.Unread == "" }

var (
	nativeTarget = regexp.MustCompile(`isa = PBXNativeTarget;`)
	// Xcode quotes a name only when it needs to, so both forms occur — and the
	// quoted form is not exotic: "DailyBreadWatchApp Watch AppTests" is a real
	// target in this fleet.
	nameLine    = regexp.MustCompile(`^\s*name = (?:"([^"]*)"|([A-Za-z0-9_.\-]+));`)
	productLine = regexp.MustCompile(`^\s*productType = "([^"]*)";`)
)

const (
	unitTest = "com.apple.product-type.bundle.unit-test"
	uiTest   = "com.apple.product-type.bundle.ui-testing"
)

// Parse reads the test targets from a project.pbxproj.
//
// The bool reports whether the project was READ, which is not the same question
// as whether it declares any test targets — and conflating them is how this
// check produced its first false positive. `lacquer audit` runs against
// projects with no .xcodeproj at all (a Swift package, a web component), and
// against manifests that name one which does not exist yet: multimeter declares
// `xcodeproj = "ios/Multimeter.xcodeproj"` and says in its own comment that the
// file is still to be created. Treating "could not read it" as "it contains
// nothing" made every selector look like it named a target the project did not
// have. The absence of a project is not evidence about the selectors.
func Parse(pbxprojPath string) ([]Target, bool, error) {
	b, err := os.ReadFile(pbxprojPath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", pbxprojPath, err)
	}

	text := string(b)
	var out []Target
	var inTarget bool
	var name string
	for _, line := range strings.Split(text, "\n") {
		if nativeTarget.MatchString(line) {
			inTarget, name = true, ""
			continue
		}
		if !inTarget {
			continue
		}
		if m := nameLine.FindStringSubmatch(line); m != nil {
			name = m[1]
			if name == "" {
				name = m[2]
			}
			continue
		}
		// productType closes the block for our purposes: it is the last thing we
		// need, and stopping here avoids a nested block's `name =` overwriting the
		// target's own.
		if m := productLine.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case unitTest:
				out = append(out, Target{Name: name})
			case uiTest:
				out = append(out, Target{Name: name, UI: true})
			}
			inTarget = false
		}
	}
	out = append(out, packageTargets(filepath.Dir(filepath.Dir(pbxprojPath)), text)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Package < out[j].Package
	})
	return out, true, nil
}

// Report is the two-directional comparison.
type Report struct {
	// Uncovered are test targets the project has that no selector names. Their
	// tests are run by nothing, and xcodebuild says nothing about a target it was
	// never asked to run.
	Uncovered []Target
	// Missing are selectors naming a target the project does not have. Each one
	// renders a `-only-testing:` that matches nothing and exits 0.
	Missing []string
	// Unverified are selectors found in no place the audit could read, while
	// some place a test target could be — a referenced local package — could
	// not be read. Neither missing nor present: reporting them missing would
	// assert what was not checked, and dropping them would pass it.
	Unverified []string
	// Unreadable are the entries that made a selector Unverified, each with its
	// reason. Set only when Unverified is non-empty.
	Unreadable []Target
	// Elsewhere are [[project.covered_elsewhere]] declarations that verified:
	// their target is run by a workflow this lacquer does not manage. Their
	// targets are NOT in Uncovered — and they are printed anyway, because an
	// exception nobody can see is an exception nobody reviews. Set by Apply.
	Elsewhere []Claim
	// Unconfirmed are declarations the repository did not bear out. Their
	// targets are STILL in Uncovered: a claim the audit cannot check must not
	// remove a finding, or the declaration is just a way of writing findings
	// away. Set by Apply.
	Unconfirmed []Claim
	// Stale are declarations that are not doing anything — the target no longer
	// exists, or a managed selector already covers it. Set by Apply.
	Stale []Claim
}

// Compare reports both directions.
//
// Selectors are compared case-sensitively and exactly, because that is how
// `-only-testing:` matches. A near-miss is a miss.
func Compare(project []Target, selectors []string) Report {
	have := map[string]bool{}
	var unreadable []Target
	for _, t := range project {
		if t.Unread != "" {
			unreadable = append(unreadable, t)
			continue
		}
		have[t.Name] = true
	}
	named := map[string]bool{}
	for _, s := range selectors {
		if s != "" {
			named[s] = true
		}
	}

	var r Report
	for _, t := range project {
		// Native targets only. A package's test suite may be run by `swift
		// test` in a workflow of its own, and `-only-testing:` can reach it at
		// all only once the scheme lists it — so no selector naming it is not
		// the evidence that it runs nowhere that it is for a native target.
		if t.native() && !named[t.Name] {
			r.Uncovered = append(r.Uncovered, t)
		}
	}
	for s := range named {
		switch {
		case have[s]:
		case len(unreadable) > 0:
			r.Unverified = append(r.Unverified, s)
		default:
			r.Missing = append(r.Missing, s)
		}
	}
	sort.Strings(r.Missing)
	sort.Strings(r.Unverified)
	if len(r.Unverified) > 0 {
		r.Unreadable = unreadable
	}
	return r
}

// Format renders the report for `lacquer audit`, empty when there is nothing to
// say so the caller can print it unconditionally.
func Format(r Report) string {
	if len(r.Uncovered) == 0 && len(r.Missing) == 0 && len(r.Unverified) == 0 &&
		len(r.Elsewhere) == 0 && len(r.Unconfirmed) == 0 && len(r.Stale) == 0 {
		return ""
	}
	var b strings.Builder

	if len(r.Missing) > 0 {
		b.WriteString("\ntest selectors naming a target this project does not have:\n")
		for _, s := range r.Missing {
			b.WriteString("  " + s + "\n")
		}
		b.WriteString("    `-only-testing:` with a name nothing matches runs no tests AND EXITS 0.\n")
		b.WriteString("    Check test_target / ui_test_target / extra_test_targets against the target\n")
		b.WriteString("    names in the Xcode project — a product's `name` is a display label and is\n")
		b.WriteString("    not required to match its scheme or its targets.\n")
	}

	if len(r.Unverified) > 0 {
		b.WriteString("\ntest selectors this audit could not check:\n")
		for _, s := range r.Unverified {
			b.WriteString("  " + s + "\n")
		}
		b.WriteString("    None is a native target of the Xcode project, and the project references a\n")
		b.WriteString("    local Swift package that could not be read, which may declare it:\n")
		for _, t := range r.Unreadable {
			b.WriteString("      " + t.Unread + "\n")
		}
		b.WriteString("    This is not a finding that the target is missing, and not evidence that it\n")
		b.WriteString("    exists. Make the package readable here and re-run the audit.\n")
	}

	if len(r.Uncovered) > 0 {
		unconfirmed := map[string]Claim{}
		for _, c := range r.Unconfirmed {
			unconfirmed[c.Target] = c
		}
		b.WriteString("\ntest targets no selector covers:\n")
		for _, t := range r.Uncovered {
			kind := "unit"
			if t.UI {
				kind = "UI"
			}
			b.WriteString("  " + t.Name + "  (" + kind + " tests)\n")
			// The declaration does not get to be silent about failing. It is
			// printed on the target's own line, with the check that failed,
			// because the author of that entry believes this finding is gone.
			if c, ok := unconfirmed[t.Name]; ok {
				b.WriteString("    DECLARED covered_elsewhere, NOT CONFIRMED:\n")
				for _, p := range c.Problems {
					b.WriteString("      " + p + "\n")
				}
			}
		}
		// Removal is a legitimate resolution and the report has to say so.
		// Implying that wiring is the only fix pushes somebody toward
		// rehabilitating a suite that tests an app which no longer exists — a real
		// case in this fleet, where one uncovered suite asserted
		// XCTAssertTrue(true) and queried UI that had been deleted.
		b.WriteString("    These run nowhere. Wire each into the manifest (extra_test_targets, or\n")
		b.WriteString("    test_target / ui_test_target) — OR DELETE IT. Removing a target that should\n")
		b.WriteString("    not exist is a correct resolution, not a failure to act; a suite nothing has\n")
		b.WriteString("    run in months is as likely to be testing an app that changed under it.\n")
		if len(r.Unconfirmed) > 0 {
			b.WriteString("    A [[project.covered_elsewhere]] entry does not remove a target from this list\n")
			b.WriteString("    by being written; it removes it by checking out against the repository. Fix\n")
			b.WriteString("    the entry or the workflow — the declaration is currently claiming something\n")
			b.WriteString("    the files do not show.\n")
		}
	}

	if len(r.Elsewhere) > 0 {
		b.WriteString("\ntest targets covered by a workflow this lacquer does not manage:\n")
		for _, c := range r.Elsewhere {
			b.WriteString("  " + c.Target + "  <- " + c.Workflow + "\n")
			b.WriteString("    " + c.Reason + "\n")
		}
		// The limits go next to the claim, not in a doc nobody opens. Every
		// sentence here is what the check DID, so nobody reads this section as
		// "these tests pass".
		b.WriteString("    Checked: that file exists, is not one the lacquer writes, names the target\n")
		b.WriteString("    outside a comment, contains a test invocation, and is triggered by a code\n")
		b.WriteString("    change. NOT checked, and not claimed: that the tests ran, that they passed,\n")
		b.WriteString("    that the mention is the -only-testing: selector, or that the workflow's result\n")
		b.WriteString("    is required to merge. This is evidence the arrangement is real and current,\n")
		b.WriteString("    not proof it works — read the workflow, or require its check on the branch.\n")
	}

	if len(r.Stale) > 0 {
		b.WriteString("\ncovered_elsewhere declarations that are not doing anything:\n")
		for _, c := range r.Stale {
			b.WriteString("  " + c.Target + " — " + c.Stale + "\n")
		}
		b.WriteString("    Each one reads as a live exception and is not one. Remove it, or correct the\n")
		b.WriteString("    target name — a declaration nobody has looked at since the target was renamed\n")
		b.WriteString("    is how a project ends up believing something is covered that nothing runs.\n")
	}
	return b.String()
}
