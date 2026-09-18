package audit

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/gitguard"
)

// PinFinding is one result of comparing a committed Package.resolved with the
// package requirements the project declares.
//
// The lockfile is not what a build uses when the two disagree. xcodebuild and
// swift build resolve against the REQUIREMENTS, and when the committed pin does
// not satisfy one they quietly re-resolve and rewrite Package.resolved in the
// build's own checkout. So CI builds what the requirement says, stays green, and
// the committed lockfile goes on stating a version that never ships. That is how
// momfriend's sentry-cocoa sat at "9.28.0" in the lockfile while its pbxproj
// pins exactVersion 9.26.0 for privacy verification: Dependabot bumps only
// Package.resolved (9.26 → 9.27 → 9.28), each bump merged green, and each was a
// no-op.
//
// Reported, not gated: nothing in the lacquer's iOS workflows builds from the
// resolved file (no -disableAutomaticPackageResolution or
// -onlyUsePackageVersionsFromResolvedFile), and making CI enforce it is a
// separate decision, taken once the fleet is clean.
type PinFinding struct {
	// Kind is one of the Pin* constants.
	Kind string
	// Resolved is the repo-relative Package.resolved this is about.
	Resolved string
	// Line is the pin's line in Resolved, for PinViolates and PinUnrequired.
	// For PinUnchecked it is File's line (0 when the whole file is meant).
	Line int
	// Package is the package's identity.
	Package string
	// Pinned is what the lockfile pins it to.
	Pinned string
	// Req is the requirement: the one violated (PinViolates), the one with no
	// pin (PinUnpinned), or project.yml's (PinDisagree).
	Req *PackageRequirement
	// Other is, for PinDisagree, the pbxproj requirement that wins.
	Other *PackageRequirement
	// File is, for PinUnchecked, the file that was not checked.
	File string
	// Why is, for PinUnchecked, the reason.
	Why string
	// Pins, Reqs, Violations and Incomparable are, for PinChecked, how much was
	// compared, how much of it failed, and how many pin/requirement pairs could
	// not be compared at all. Unread is how many declarations were found and not
	// read (see the PinUnchecked notes).
	Pins, Reqs, Violations, Incomparable, Unread int
}

const (
	// PinViolates is the finding: the committed pin does not satisfy a declared
	// requirement, so the build does not use it.
	PinViolates = "violates"
	// PinUnpinned is a note: a declared requirement with no pin in the lockfile.
	// The lockfile predates the requirement; the build resolves it anyway.
	PinUnpinned = "unpinned"
	// PinUnrequired is a note: a pin no declaration here names. Nearly always a
	// transitive dependency — which this audit cannot confirm without the
	// dependency graph — and otherwise a pin left behind by a removed package.
	PinUnrequired = "unrequired"
	// PinDisagree is a note: an XcodeGen project.yml asks for something other
	// than the committed pbxproj it generates. The build reads the pbxproj; the
	// next `xcodegen generate` replaces it.
	PinDisagree = "project.yml-disagrees"
	// PinUnchecked is a note: something the audit found and could not read.
	PinUnchecked = "not-checked"
	// PinChecked is the summary for every lockfile that was compared, so
	// "checked and fine" is not silent — silence would look the same as "found
	// no lockfile" or "read no requirements".
	PinChecked = "checked"
)

// pinRequirements is what one lockfile is checked against.
type pinRequirements struct {
	reqs      []PackageRequirement
	unchecked []uncheckedDecl
	// yml holds project.yml's requirements when a committed pbxproj won over it.
	yml []PackageRequirement
}

// PackagePinFindings compares every committed Package.resolved in the project
// with the requirements declared for it:
//
//   - X.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved: the
//     project's pbxproj, or its XcodeGen project.yml when no pbxproj is
//     committed, plus every local package it references;
//   - X.xcworkspace/xcshareddata/swiftpm/Package.resolved: every project and
//     package the workspace names;
//   - dir/Package.resolved beside dir/Package.swift: that manifest and its local
//     path dependencies.
//
// Only tracked files are read (git, not a walk): the subject is what the
// repository says ships, and a walk would also find DerivedData, .build and
// worktree copies.
func PackagePinFindings(projectRoot string) []PinFinding {
	files, err := gitguard.Tracked(projectRoot, "*Package.resolved", "*Package.swift", "*project.pbxproj", "*project.yml", "*contents.xcworkspacedata")
	if err != nil {
		return []PinFinding{{Kind: PinUnchecked, File: ".", Why: "could not list tracked files: " + err.Error()}}
	}
	ix := pinIndex{root: projectRoot, tracked: map[string]bool{}}
	var lockfiles []string
	for _, f := range files {
		ix.tracked[f] = true
		switch path.Base(f) {
		case "Package.resolved":
			lockfiles = append(lockfiles, f)
		case "project.yml":
			ix.ymls = append(ix.ymls, f)
		}
	}
	sort.Strings(lockfiles)
	var out []PinFinding
	for _, lf := range lockfiles {
		out = append(out, ix.check(lf)...)
	}
	return out
}

type pinIndex struct {
	root    string
	tracked map[string]bool
	ymls    []string
}

func (ix pinIndex) read(p string) (string, error) {
	b, err := os.ReadFile(filepath.Join(ix.root, filepath.FromSlash(p)))
	return string(b), err
}

func unchecked(file string, line int, why string) PinFinding {
	return PinFinding{Kind: PinUnchecked, File: file, Line: line, Why: why}
}

// check compares one lockfile with its requirements.
func (ix pinIndex) check(lockfile string) []PinFinding {
	body, err := ix.read(lockfile)
	if err != nil {
		return []PinFinding{unchecked(lockfile, 0, err.Error())}
	}
	pins, err := parseResolved([]byte(body))
	if err != nil {
		return []PinFinding{unchecked(lockfile, 0, "unreadable Package.resolved: "+err.Error())}
	}
	pr, fail := ix.requirementsFor(lockfile)
	if fail != nil {
		return fail
	}

	var out []PinFinding
	for _, u := range pr.unchecked {
		out = append(out, unchecked(u.File, u.Line, u.Why))
	}
	incomparable := 0
	byURL := map[string][]int{}
	for i, r := range pr.reqs {
		n := normalizePackageURL(r.URL)
		byURL[n] = append(byURL[n], i)
	}
	pinned := map[string]bool{}
	for _, p := range pins {
		n := normalizePackageURL(p.Location)
		pinned[n] = true
		idx := byURL[n]
		if len(idx) == 0 {
			out = append(out, PinFinding{Kind: PinUnrequired, Resolved: lockfile, Line: p.Line, Package: p.Identity, Pinned: p.State.describe()})
			continue
		}
		for _, i := range idx {
			r := pr.reqs[i]
			ok, checked := r.satisfiedBy(p.State)
			switch {
			case !checked:
				incomparable++
				out = append(out, unchecked(r.File, r.Line, fmt.Sprintf("%s: cannot compare the requirement (%s) with the pin in %s (%s)", p.Identity, r.describe(), lockfile, p.State.describe())))
			case !ok:
				out = append(out, PinFinding{Kind: PinViolates, Resolved: lockfile, Line: p.Line, Package: p.Identity, Pinned: p.State.describe(), Req: &r})
			}
		}
	}
	for _, r := range pr.reqs {
		if !pinned[normalizePackageURL(r.URL)] {
			r := r
			out = append(out, PinFinding{Kind: PinUnpinned, Resolved: lockfile, Package: packageName(r.URL), Req: &r})
		}
	}
	for _, y := range pr.yml {
		for _, i := range byURL[normalizePackageURL(y.URL)] {
			if w := pr.reqs[i]; !sameRequirement(y, w) && strings.HasSuffix(w.File, ".pbxproj") {
				y, w := y, w
				out = append(out, PinFinding{Kind: PinDisagree, Resolved: lockfile, Package: packageName(y.URL), Req: &y, Other: &w})
			}
		}
	}
	violations := 0
	for _, f := range out {
		if f.Kind == PinViolates {
			violations++
		}
	}
	summary := PinFinding{Kind: PinChecked, Resolved: lockfile, Pins: len(pins), Reqs: len(pr.reqs),
		Violations: violations, Incomparable: incomparable, Unread: len(pr.unchecked)}
	return append([]PinFinding{summary}, out...)
}

// requirementsFor finds the declarations a lockfile answers to. A non-nil fail
// means they could not be established, and is the whole result for the lockfile:
// comparing against a partial set would report every missing requirement's
// pins as unrequired.
func (ix pinIndex) requirementsFor(lockfile string) (pinRequirements, []PinFinding) {
	var pr pinRequirements
	seen := map[string]bool{}
	segs := strings.Split(lockfile, "/")
	for i, s := range segs {
		switch path.Ext(s) {
		case ".xcodeproj":
			return pr, ix.addProject(&pr, path.Join(segs[:i+1]...), seen)
		case ".xcworkspace":
			return pr, ix.addWorkspace(&pr, path.Join(segs[:i+1]...), seen)
		}
	}
	dir := dirOf(lockfile)
	if !ix.tracked[path.Join(dir, "Package.swift")] {
		return pr, []PinFinding{unchecked(lockfile, 0, "no committed Package.swift, .xcodeproj or .xcworkspace declares what this lockfile resolves")}
	}
	return pr, ix.addManifest(&pr, dir, seen)
}

// addProject adds an .xcodeproj's requirements. The committed pbxproj wins over
// an XcodeGen project.yml, because it is what xcodebuild reads; project.yml is
// only the requirement when no pbxproj is committed (it is then what the next
// generate writes).
func (ix pinIndex) addProject(pr *pinRequirements, proj string, seen map[string]bool) []PinFinding {
	if seen[proj] {
		return nil
	}
	seen[proj] = true
	yml, spec, ymlErr := ix.specFor(proj)
	pbx := path.Join(proj, "project.pbxproj")
	if ix.tracked[pbx] {
		body, err := ix.read(pbx)
		if err != nil {
			return []PinFinding{unchecked(pbx, 0, err.Error())}
		}
		remote, local, err := parsePbxprojPackages(body)
		if err != nil {
			return []PinFinding{unchecked(pbx, 0, "unreadable project.pbxproj: "+err.Error())}
		}
		for _, r := range remote {
			r.File = pbx
			pr.reqs = append(pr.reqs, r)
		}
		if yml != "" && ymlErr == nil {
			for _, r := range spec.Remote {
				r.File = yml
				pr.yml = append(pr.yml, r)
			}
		}
		return ix.addLocals(pr, dirOf(proj), local, seen)
	}
	if yml == "" {
		return []PinFinding{unchecked(proj, 0, "neither project.pbxproj nor an XcodeGen project.yml for it is committed")}
	}
	if ymlErr != nil {
		return []PinFinding{unchecked(yml, 0, "unreadable project.yml: "+ymlErr.Error())}
	}
	for _, r := range spec.Remote {
		r.File = yml
		pr.reqs = append(pr.reqs, r)
	}
	for _, u := range spec.Unchecked {
		u.File = yml
		pr.unchecked = append(pr.unchecked, u)
	}
	return ix.addLocals(pr, dirOf(yml), spec.Local, seen)
}

// specFor finds the committed XcodeGen spec that generates proj: a project.yml
// whose `name:` is proj's name, in proj's directory.
func (ix pinIndex) specFor(proj string) (string, xcodeGenSpec, error) {
	for _, y := range ix.ymls {
		body, err := ix.read(y)
		if err != nil {
			continue
		}
		spec, err := parseXcodeGenPackages([]byte(body))
		if err != nil {
			if dirOf(y) == dirOf(proj) {
				return y, spec, err
			}
			continue
		}
		if spec.Name != "" && path.Join(dirOf(y), spec.Name+".xcodeproj") == proj {
			return y, spec, nil
		}
	}
	return "", xcodeGenSpec{}, nil
}

// addLocals adds the requirements of local packages, given relative to base.
func (ix pinIndex) addLocals(pr *pinRequirements, base string, rels []string, seen map[string]bool) []PinFinding {
	var fail []PinFinding
	for _, rel := range rels {
		fail = append(fail, ix.addManifest(pr, path.Join(base, rel), seen)...)
	}
	return fail
}

// addManifest adds a Package.swift's requirements and, recursively, those of its
// local path dependencies: all of them resolve into the same lockfile.
func (ix pinIndex) addManifest(pr *pinRequirements, dir string, seen map[string]bool) []PinFinding {
	manifest := path.Join(dir, "Package.swift")
	if seen[manifest] {
		return nil
	}
	seen[manifest] = true
	if !ix.tracked[manifest] {
		// Its requirements are unknown, so its pins will show as unrequired; say
		// why, rather than leave that looking like a transitive dependency.
		pr.unchecked = append(pr.unchecked, uncheckedDecl{File: dir, Why: "local package with no committed Package.swift; its requirements are not checked"})
		return nil
	}
	body, err := ix.read(manifest)
	if err != nil {
		return []PinFinding{unchecked(manifest, 0, err.Error())}
	}
	m := parsePackageSwift(body)
	for _, r := range m.Remote {
		r.File = manifest
		pr.reqs = append(pr.reqs, r)
	}
	for _, u := range m.Unchecked {
		u.File = manifest
		pr.unchecked = append(pr.unchecked, u)
	}
	return ix.addLocals(pr, dir, m.Local, seen)
}

// addWorkspace adds the requirements of every project and package a standalone
// workspace names.
func (ix pinIndex) addWorkspace(pr *pinRequirements, ws string, seen map[string]bool) []PinFinding {
	data := path.Join(ws, "contents.xcworkspacedata")
	body, err := ix.read(data)
	if err != nil || !ix.tracked[data] {
		return []PinFinding{unchecked(data, 0, "the workspace's contents.xcworkspacedata is not committed, so what it builds is unknown")}
	}
	refs, err := workspaceRefs(body, dirOf(ws))
	if err != nil {
		return []PinFinding{unchecked(data, 0, err.Error())}
	}
	var fail []PinFinding
	for _, ref := range refs {
		switch {
		case path.Ext(ref) == ".xcodeproj":
			fail = append(fail, ix.addProject(pr, ref, seen)...)
		case ix.tracked[path.Join(ref, "Package.swift")]:
			fail = append(fail, ix.addManifest(pr, ref, seen)...)
		}
	}
	return fail
}

// workspaceRefs returns the repo-relative paths a workspace's FileRefs name.
// group: and container: locations are relative to the enclosing group, the
// outermost being the workspace's own directory. Absolute and other location
// kinds are outside the repository and skipped.
func workspaceRefs(body, base string) ([]string, error) {
	dec := xml.NewDecoder(strings.NewReader(body))
	stack := []string{base}
	var refs []string
	resolve := func(loc string) (string, bool) {
		kind, p, ok := strings.Cut(loc, ":")
		if !ok {
			return "", false
		}
		switch kind {
		case "group":
			return path.Join(stack[len(stack)-1], p), true
		case "container":
			return path.Join(base, p), true
		}
		return "", false
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return refs, nil
			}
			return nil, fmt.Errorf("unreadable contents.xcworkspacedata: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			loc := ""
			for _, a := range t.Attr {
				if a.Name.Local == "location" {
					loc = a.Value
				}
			}
			switch t.Name.Local {
			case "Group":
				p, ok := resolve(loc)
				if !ok {
					p = stack[len(stack)-1]
				}
				stack = append(stack, p)
			case "FileRef":
				if p, ok := resolve(loc); ok {
					refs = append(refs, p)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "Group" && len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

// dirOf is path.Dir with the root spelled ".".
func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" || d == "/" {
		return "."
	}
	return d
}

func at(file string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return file
}

// FormatPackagePins renders the report, or "" when there is no committed
// Package.resolved to check.
func FormatPackagePins(fs []PinFinding) string {
	if len(fs) == 0 {
		return ""
	}
	byKind := map[string][]PinFinding{}
	for _, f := range fs {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}
	var b strings.Builder
	b.WriteString("\ncommitted Package.resolved checked against the declared package requirements:\n")
	for _, f := range byKind[PinChecked] {
		// "all satisfied" only when every pair was actually compared: a pair the
		// comparator could not read is not a pass.
		var parts []string
		if f.Violations > 0 {
			parts = append(parts, fmt.Sprintf("%d not satisfied", f.Violations))
		}
		if f.Incomparable > 0 {
			parts = append(parts, fmt.Sprintf("%d could not be compared", f.Incomparable))
		}
		verdict := strings.Join(parts, ", ")
		if verdict == "" {
			verdict = "all satisfied"
		}
		if f.Unread > 0 {
			verdict += fmt.Sprintf("; %s not read (see notes)", plural(f.Unread, "declaration"))
		}
		fmt.Fprintf(&b, "  %s  %s against %s: %s\n", f.Resolved, plural(f.Pins, "pin"), plural(f.Reqs, "requirement"), verdict)
	}
	if v := byKind[PinViolates]; len(v) > 0 {
		b.WriteString("pins that do not match the declared requirement:\n")
		for _, f := range v {
			fmt.Fprintf(&b, "  %s  %s is pinned to %s, but %s requires %s\n",
				at(f.Resolved, f.Line), f.Package, f.Pinned, at(f.Req.File, f.Req.Line), f.Req.describe())
		}
		b.WriteString("SwiftPM resolves to the requirement at build time, not to the lockfile: when a pin does\n" +
			"not satisfy it, xcodebuild / swift build re-resolve and rewrite Package.resolved in the\n" +
			"build's own checkout, and stay green. So the committed lockfile misstates what ships, and a\n" +
			"Dependabot PR that bumps only Package.resolved is a no-op that merges green. Fix whichever\n" +
			"side is wrong: move the requirement if the newer version is wanted, or restore the pin.\n")
	}
	notes := len(byKind[PinUnpinned]) + len(byKind[PinUnrequired]) + len(byKind[PinDisagree]) + len(byKind[PinUnchecked])
	if notes == 0 {
		return b.String()
	}
	b.WriteString("notes (not findings):\n")
	for _, f := range byKind[PinUnpinned] {
		fmt.Fprintf(&b, "  %s  %s is required (%s, %s) but has no pin; the lockfile predates the requirement\n",
			f.Resolved, f.Package, f.Req.describe(), at(f.Req.File, f.Req.Line))
	}
	for _, f := range byKind[PinDisagree] {
		fmt.Fprintf(&b, "  %s  %s: project.yml asks for %s, the committed %s for %s; the build reads the pbxproj, and the next xcodegen generate will change it\n",
			at(f.Req.File, f.Req.Line), f.Package, f.Req.describe(), at(f.Other.File, f.Other.Line), f.Other.describe())
	}
	// One line per lockfile: these are usually transitive dependencies, and a
	// line each would bury everything else in the section.
	var order []string
	unreq := map[string][]string{}
	for _, f := range byKind[PinUnrequired] {
		if _, ok := unreq[f.Resolved]; !ok {
			order = append(order, f.Resolved)
		}
		unreq[f.Resolved] = append(unreq[f.Resolved], f.Package+" "+f.Pinned)
	}
	for _, r := range order {
		fmt.Fprintf(&b, "  %s  pinned but not declared here (transitive, most likely): %s\n", r, strings.Join(unreq[r], ", "))
	}
	for _, f := range byKind[PinUnchecked] {
		fmt.Fprintf(&b, "  %s  not checked: %s\n", at(f.File, f.Line), f.Why)
	}
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
