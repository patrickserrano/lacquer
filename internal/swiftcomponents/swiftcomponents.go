// Package swiftcomponents answers one question for an iOS project: which of its
// Swift files does a lacquer check actually reach?
//
// Lint used to resolve a single {{COMPONENT_PREFIX}}, and the pre-commit hook's
// `files: ^{{COMPONENT_PREFIX}}` filter hid every other directory from it. Swift
// beside the app component (local packages, tools) was therefore linted by
// nothing and built by nothing, and nothing said so: one project held 57 such
// files gated by nothing until a hand-written script noticed.
//
// The components are read from the manifest, never discovered by scanning for
// .swiftlint.yml files: a vendored checkout's config would become a component,
// and nested test-directory configs are SwiftLint nesting, not components. A
// directory the manifest does not declare is reported (Stray) rather than
// guessed at.
package swiftcomponents

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitguard"
	"github.com/patrickserrano/lacquer/internal/testtargets"
)

// Component is one declared Swift component.
type Component struct {
	// Path is slash-separated and relative to the repository root; "." is the
	// root itself.
	Path string
	// Profile is true for the one component carrying the ios profile: the app,
	// which owns the xcodeproj and whose .swiftlint.yml is rendered. Every other
	// Swift component is a package component with a project-owned config.
	Profile bool
}

// Name is how the component is labelled in output: "ios (app)" or "tools".
func (c Component) Name() string {
	if c.Profile {
		return c.Path + " (app)"
	}
	return c.Path
}

// Components returns the manifest's Swift components: a [[component]] whose
// profiles contain "ios" or whose stack is "ios". The app component comes
// first, then package components in manifest order.
func Components(cfg *config.Config) []Component {
	var app, pkgs []Component
	for _, c := range cfg.Components {
		p := path.Clean(strings.TrimSuffix(c.Path, "/"))
		switch {
		case hasIOSProfile(c):
			app = append(app, Component{Path: p, Profile: true})
		case c.Stack == "ios":
			pkgs = append(pkgs, Component{Path: p})
		}
	}
	return append(app, pkgs...)
}

func hasIOSProfile(c config.Component) bool {
	for _, p := range c.Profiles {
		if p == "ios" {
			return true
		}
	}
	return false
}

// covers reports whether a component directory contains a slash-separated file
// path. It compares against comp + "/": a component is a directory, so `ios`
// must not cover `ios-tools/x.swift`.
func covers(comp, file string) bool {
	return comp == "." || strings.HasPrefix(file, comp+"/")
}

// Grouped is the files assigned to one component.
type Grouped struct {
	Component Component
	Files     []string
}

// Group assigns each path to the longest component that covers it, so a
// component nested in another (the root and a package beneath it) takes its own
// files. Components with no files are omitted; the order is the components'
// order. unmatched are the paths no component covers.
//
// The pre-commit wrapper does the same grouping in bash; its tests check it
// against this.
func Group(paths []string, comps []Component) (groups []Grouped, unmatched []string) {
	byComp := make([][]string, len(comps))
	for _, f := range paths {
		best := -1
		for i, c := range comps {
			if covers(c.Path, f) && (best < 0 || depth(c.Path) > depth(comps[best].Path)) {
				best = i
			}
		}
		if best < 0 {
			unmatched = append(unmatched, f)
			continue
		}
		byComp[best] = append(byComp[best], f)
	}
	for i, files := range byComp {
		if len(files) > 0 {
			groups = append(groups, Grouped{Component: comps[i], Files: files})
		}
	}
	return groups, unmatched
}

// depth orders components for longest-match: "." is shallower than any path.
func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// files lists the repository's files matching pathspec, committed or merely on
// disk, minus what .gitignore excludes.
//
// --others because a local check must see the file someone is about to commit;
// CI's checkout holds only committed files, so there the two are the same.
// git does not descend into a nested repository, so the lacquer CI clones into
// .lacquer-checkout is never listed.
//
// A git failure is an error, never an empty list: "could not look" must not
// read as "nothing there".
func files(root string, pathspec string) ([]string, error) {
	inTree, err := gitguard.InWorkTree(root)
	if err != nil {
		return nil, fmt.Errorf("%w: git is unavailable: %v", ErrCannotList, err)
	}
	if !inTree {
		return nil, fmt.Errorf("%w: %s is not a git work tree", ErrCannotList, root)
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", pathspec)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list %s files in %s: %v: %s", pathspec, root, err, strings.TrimSpace(stderr.String()))
	}
	seen := map[string]bool{}
	var list []string
	for _, f := range strings.Split(string(out), "\x00") {
		// --cached and --others both list a file that is staged but also
		// modified; one entry is enough.
		if f != "" && !seen[f] {
			seen[f] = true
			list = append(list, f)
		}
	}
	sort.Strings(list)
	return list, nil
}

// ErrCannotList means the repository's files could not be listed: root is not
// in a git work tree, or git is unavailable. It is never an empty answer: the
// caller decides whether that fails (the CI step) or is reported as not checked
// (a local audit, the fleet sweep).
var ErrCannotList = errors.New("cannot list the repository's Swift files")

// Stray lists the repository's .swift files under no Swift component. A project
// with no Swift component has none: there is no lint config for them to have
// escaped.
func Stray(root string, cfg *config.Config) ([]string, error) {
	r, err := Check(root, cfg, time.Time{})
	return r.Stray, err
}

// Package is one SwiftPM package under a package component.
type Package struct {
	Dir string
	// IOSOnly is true when its platforms list no macOS. `swift build` compiles
	// for the host, macOS, so the Lint job cannot build it and says so instead
	// (building it for iOS is a follow-up to #522).
	IOSOnly bool
	// Listed is true when a Swift component's `packages` names it (#522 U4b),
	// whether or not Packages would also have found it by depth.
	Listed bool
}

// Packages lists the SwiftPM packages the Lint job builds: every directory
// holding a Package.swift directly inside a package component, or one level
// below it, plus every package a Swift component lists in its `packages`
// (#522 U4b). A package found both ways is returned once, marked Listed.
//
// Packages under the app component are built only when listed: otherwise the
// Xcode project builds them in the Test job, which compiles the package but not
// its tests. A listed package is not a component, so nothing here changes what
// is linted: the declaring component still lints its files.
func Packages(root string, cfg *config.Config) ([]Package, error) {
	var comps []Component
	for _, c := range Components(cfg) {
		if !c.Profile {
			comps = append(comps, c)
		}
	}
	listed := listedDirs(cfg)
	if len(comps) == 0 && len(listed) == 0 {
		return nil, nil
	}
	byDir := map[string]*Package{}
	read := func(rel string) (Package, error) {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), "Package.swift"))
		if err != nil {
			return Package{}, err
		}
		return Package{Dir: rel, IOSOnly: !BuildsOnMacOS(string(src))}, nil
	}
	if len(comps) > 0 {
		manifests, err := files(root, "*Package.swift")
		if err != nil {
			return nil, err
		}
		for _, m := range manifests {
			if path.Base(m) != "Package.swift" {
				continue
			}
			dir := path.Dir(m)
			for _, c := range comps {
				if dir == c.Path || path.Dir(dir) == c.Path {
					p, err := read(dir)
					if err != nil {
						return nil, err
					}
					byDir[dir] = &p
					break
				}
			}
		}
	}
	for _, dir := range listed {
		p, ok := byDir[dir]
		if !ok {
			q, err := read(dir)
			if err != nil {
				return nil, fmt.Errorf("package %s listed in a component's packages: %w", dir, err)
			}
			p = &q
			byDir[dir] = p
		}
		p.Listed = true
	}
	pkgs := make([]Package, 0, len(byDir))
	for _, p := range byDir {
		pkgs = append(pkgs, *p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Dir < pkgs[j].Dir })
	return pkgs, nil
}

// listedDirs is every `packages` entry of every Swift component as a
// slash-separated path from the repository root, in manifest order.
func listedDirs(cfg *config.Config) []string {
	var dirs []string
	for _, c := range cfg.Components {
		if !isSwift(c) {
			continue
		}
		base := path.Clean(strings.TrimSuffix(c.Path, "/"))
		for _, e := range c.Packages {
			dirs = append(dirs, path.Join(base, path.Clean(e)))
		}
	}
	return dirs
}

func isSwift(c config.Component) bool { return c.Stack == "ios" || hasIOSProfile(c) }

// PackageDirs is the directories of Packages, built or not.
func PackageDirs(root string, cfg *config.Config) ([]string, error) {
	pkgs, err := Packages(root, cfg)
	var dirs []string
	for _, p := range pkgs {
		dirs = append(dirs, p.Dir)
	}
	return dirs, err
}

var (
	lineComment  = regexp.MustCompile(`//[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	platformsKey = regexp.MustCompile(`\bplatforms\s*:\s*`)
	macOSEntry   = regexp.MustCompile(`\.macOS\b`)
)

// BuildsOnMacOS reports whether a Package.swift can be built by `swift build`
// on macOS: its `platforms:` list names macOS, or it declares no list at all
// (every platform at its default). Read textually, comments removed, because
// the workflow is also rendered by the drift audit on Linux, where there is no
// Swift toolchain to ask.
//
// A list it cannot read (`platforms: shared`) counts as buildable: the build
// then runs and fails loudly if it must, rather than being skipped unseen.
func BuildsOnMacOS(manifest string) bool {
	src := lineComment.ReplaceAllString(blockComment.ReplaceAllString(manifest, ""), "")
	loc := platformsKey.FindStringIndex(src)
	if loc == nil {
		return true
	}
	rest := src[loc[1]:]
	if !strings.HasPrefix(rest, "[") {
		return true
	}
	depth := 0
	for i, r := range rest {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return macOSEntry.MatchString(rest[:i])
			}
		}
	}
	return true
}

// GateFrom is the first day stray Swift fails a check; before it, the same
// finding is a warning that prints this date.
//
// It is the watch gate's date by ruling: one date, the release that ships these
// gates plus fourteen days, so both switch together and moving one moves both.
func GateFrom() time.Time { return testtargets.WatchGateFrom }

// GateDate is GateFrom as a manifest-style date.
func GateDate() string { return GateFrom().Format("2006-01-02") }

// Report is the stray-Swift check's result.
type Report struct {
	// Components are the Swift components the manifest declares.
	Components []Component
	// Checked is how many .swift files were compared against them, so a run
	// that looked at nothing says so.
	Checked int
	// Stray are the files no component covers.
	Stray []string
	// Now decides whether Stray blocks: on or after GateFrom.
	Now time.Time
}

// Check compares every .swift file in the repository with the manifest's Swift
// components.
func Check(root string, cfg *config.Config, now time.Time) (Report, error) {
	r := Report{Components: Components(cfg), Now: now}
	if len(r.Components) == 0 {
		return r, nil
	}
	all, err := files(root, "*.swift")
	if err != nil {
		return r, err
	}
	r.Checked = len(all)
	_, r.Stray = Group(all, r.Components)
	return r, nil
}

// Blocks reports whether the report fails the check today.
func (r Report) Blocks() bool { return len(r.Stray) > 0 && !r.Now.Before(GateFrom()) }

// Blocking is the number of findings that fail the check today: zero, or the
// number of stray files.
func (r Report) Blocking() int {
	if !r.Blocks() {
		return 0
	}
	return len(r.Stray)
}

// Remedy is the fix for a set of stray files: declare Dir as a package
// component, or, when no directory can be one, move the files listed in Move.
type Remedy struct {
	Dir   string
	Files int
	Move  []string
}

// Remedies groups the strays by the directory to declare: the shallowest
// directory holding the file that neither contains a declared component nor is
// the repository root. A directory containing a component would lint that
// component a second time, so a file with no such directory is to be moved.
// Directories come first, sorted; the files to move last.
func Remedies(r Report) []Remedy {
	counts := map[string]int{}
	var move []string
	for _, f := range r.Stray {
		if d := remedyDir(f, r.Components); d != "" {
			counts[d]++
		} else {
			move = append(move, f)
		}
	}
	var out []Remedy
	for d, n := range counts {
		out = append(out, Remedy{Dir: d, Files: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	if len(move) > 0 {
		out = append(out, Remedy{Files: len(move), Move: move})
	}
	return out
}

func remedyDir(file string, comps []Component) string {
	segs := strings.Split(path.Dir(file), "/")
	if segs[0] == "." {
		return ""
	}
	for i := range segs {
		dir := strings.Join(segs[:i+1], "/")
		holdsComponent := false
		for _, c := range comps {
			if covers(dir, c.Path) {
				holdsComponent = true
				break
			}
		}
		if !holdsComponent {
			return dir
		}
	}
	return ""
}

// Summary is the finding in one line, for a CI annotation.
func Summary(r Report) string {
	var dirs []string
	moves := 0
	for _, m := range Remedies(r) {
		if m.Dir != "" {
			dirs = append(dirs, fmt.Sprintf("%s (%d)", m.Dir, m.Files))
		} else {
			moves = m.Files
		}
	}
	var fix []string
	if len(dirs) > 0 {
		fix = append(fix, fmt.Sprintf(`declare %s in .lacquer.toml as [[component]] path = "<dir>" stack = "ios", each with a .swiftlint.yml`, strings.Join(dirs, ", ")))
	}
	if moves > 0 {
		fix = append(fix, fmt.Sprintf("move %d file(s) at no declarable directory under a component", moves))
	}
	s := fmt.Sprintf("%d Swift file(s) are under no declared Swift component, so nothing lints or builds them. Fix: %s.",
		len(r.Stray), strings.Join(fix, "; "))
	if r.Blocks() {
		return s + fmt.Sprintf(" Blocking since %s.", GateDate())
	}
	return s + fmt.Sprintf(" This becomes a failure on %s.", GateDate())
}

// Format renders the report, empty when there is nothing stray.
func Format(r Report) string {
	if len(r.Stray) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nSwift files under no declared Swift component (%d of %d), so nothing lints or builds them:\n", len(r.Stray), r.Checked)
	for _, f := range r.Stray {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	names := make([]string, len(r.Components))
	for i, c := range r.Components {
		names[i] = c.Name()
	}
	fmt.Fprintf(&b, "  Swift components declared: %s\n", strings.Join(names, ", "))
	for _, m := range Remedies(r) {
		if m.Dir == "" {
			for _, f := range m.Move {
				fmt.Fprintf(&b, "  %s: move it under a component, or out of the repository.\n", f)
			}
			continue
		}
		fmt.Fprintf(&b, "  %s (%d file(s)): add to .lacquer.toml, and commit %s/.swiftlint.yml:\n\n", m.Dir, m.Files, m.Dir)
		fmt.Fprintf(&b, "      [[component]]\n      path = %q\n      stack = \"ios\"\n\n", m.Dir)
	}
	if r.Blocks() {
		fmt.Fprintf(&b, "  BLOCKING since %s: the Lint job and `lacquer audit` (exit 6) fail until each one is declared or moved.\n", GateDate())
	} else {
		fmt.Fprintf(&b, "  Not blocking yet: from %s this fails the Lint job and `lacquer audit` (exit 6).\n", GateDate())
	}
	return b.String()
}
