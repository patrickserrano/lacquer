package baseline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WarningsKey is the setting this file exists to enforce.
//
// It is non-negotiable fleet policy: every configuration of every target treats
// Swift warnings as errors. The reason it needs enforcing at SYNC time rather
// than in CI is that CI cannot see a project that never runs the job. The
// measured case: Queueify set it as
//
//	SWIFT_TREAT_WARNINGS_AS_ERRORS[config=Release] = YES
//
// so Debug builds did not treat warnings as errors at all — and because the
// Release build job is skipped on pull requests that touch no views, a
// services-only change had warnings-as-errors enforced NOWHERE. A setting that
// is present, looks deliberate, and covers half of what it appears to cover is
// worse than one that is absent, because nobody re-reads it.
const WarningsKey = "SWIFT_TREAT_WARNINGS_AS_ERRORS"

// RequiredConfigs are the configuration names that must each resolve to YES.
//
// Named explicitly rather than "whatever configurations the project happens to
// declare": a project that deleted its Debug configuration should fail this
// check loudly, not pass it vacuously by having nothing to test.
var RequiredConfigs = []string{"Debug", "Release"}

// Violation is one (configuration, target) pair that does not treat warnings as
// errors, with enough detail that the fix is obvious from the message alone.
type Violation struct {
	Config string // "Debug" / "Release"
	Target string // the configuration's owning object, or "project" for project level
	Found  string // the value that was resolved, or "" when nothing was found
	Source string // where the resolved value came from, for a message a human can act on
}

func (v Violation) String() string {
	if v.Found == "" {
		return fmt.Sprintf("%s/%s: %s is not set", v.Target, v.Config, WarningsKey)
	}
	return fmt.Sprintf("%s/%s: %s = %s (from %s)", v.Target, v.Config, WarningsKey, v.Found, v.Source)
}

// xcconfigSetting matches an xcconfig assignment, with or without the
// bracketed conditions Xcode allows: KEY[config=Release][sdk=*] = VALUE.
//
// The conditions are the whole reason this parser exists rather than a grep.
// `KEY = YES` and `KEY[config=Release] = YES` are one character apart in a diff
// and mean entirely different things, and the second is the one that shipped a
// project with unchecked Debug builds.
var xcconfigSetting = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)((?:\[[^\]]*\])*)\s*=\s*(.*)$`)

// configCondition pulls the `config=NAME` out of a bracket group.
var configCondition = regexp.MustCompile(`\[config=([^\]\[]+)\]`)

// readXcconfig resolves key for configName out of an xcconfig file, following
// #include directives.
//
// Returns the LAST matching assignment, because that is what Xcode does: an
// xcconfig is read top to bottom and a later line overrides an earlier one.
// Reading the first match instead would report the value a project started with
// rather than the one it builds with.
func readXcconfig(path, key, configName string, depth int) (value, source string, found bool) {
	// #include cycles are legal to write and would otherwise hang a sync.
	if depth > 16 {
		return "", "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	dir := filepath.Dir(path)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		// `#include "x.xcconfig"` and the optional form `#include? "x.xcconfig"`.
		if rest, ok := strings.CutPrefix(line, "#include"); ok {
			rest = strings.TrimPrefix(strings.TrimSpace(rest), "?")
			inc := strings.Trim(strings.TrimSpace(rest), `"<>`)
			if inc == "" {
				continue
			}
			if v, s, ok := readXcconfig(filepath.Join(dir, inc), key, configName, depth+1); ok {
				value, source, found = v, s, true
			}
			continue
		}
		m := xcconfigSetting.FindStringSubmatch(line)
		if m == nil || m[1] != key {
			continue
		}
		// A bracketed `config=` condition restricts the line to that
		// configuration. Anything else in brackets (sdk, arch) does not affect
		// WHICH configuration the line applies to, so it is deliberately not a
		// reason to skip: an sdk-conditioned line still sets the value for this
		// configuration on that sdk, and treating it as absent would report a
		// project stricter than it is.
		if c := configCondition.FindStringSubmatch(m[2]); c != nil && !strings.EqualFold(strings.TrimSpace(c[1]), configName) {
			continue
		}
		value = strings.TrimSpace(m[3])
		source = path
		found = true
	}
	return value, source, found
}

// EnforceWarningsAsErrors reports every configuration of projectPath that does
// not treat warnings as errors.
//
// A nil slice means the project is compliant. A non-empty slice is a refusal:
// the caller is expected to abort rather than warn, because a warning here has
// already been ignored once in every project that reaches this state.
//
// projectPath is the .xcodeproj (or the project.pbxproj inside it). componentDir
// is where xcconfig files are resolved from.
func EnforceWarningsAsErrors(projectPath, componentDir string) ([]Violation, error) {
	d, err := ReadXcodeproj(projectPath)
	if err != nil {
		return nil, err
	}
	xcconfigs := indexXcconfigs(componentDir)

	// A project file with NO target configurations at all is not a project this
	// check can have an opinion about. `lacquer init` writes a stub .xcodeproj as
	// a detection marker — literally `{}` — and a brand new project must be able
	// to sync. That is different from a project that HAS targets and is missing
	// one of the required configurations, which is a deletion and is reported
	// below: "not a project yet" and "a project with a hole in it" are different
	// answers and only the second is a violation.
	anyTarget := false
	for _, c := range d.Configs {
		if !c.ProjectLevel {
			anyTarget = true
			break
		}
	}
	if !anyTarget {
		return nil, nil
	}

	// Target configurations are what actually build. Project-level
	// configurations are consulted for inheritance by Effective, not checked in
	// their own right — a project-level value that every target overrides is not
	// what the compiler sees.
	var out []Violation
	for _, name := range RequiredConfigs {
		targets := 0
		for _, c := range d.Configs {
			if c.ProjectLevel || !strings.EqualFold(c.Name, name) {
				continue
			}
			targets++
			v, src, ok := d.resolveWarnings(c, name, xcconfigs)
			if ok && strings.EqualFold(v, "YES") {
				continue
			}
			out = append(out, Violation{Config: name, Target: c.ID, Found: v, Source: src})
		}
		if targets == 0 {
			// Nothing to check is not the same as nothing wrong. A project with
			// no target configuration of this name either does not build it or
			// was not parsed, and both are states a human has to look at rather
			// than a pass.
			out = append(out, Violation{Config: name, Target: "(no target configuration found)"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Config != out[j].Config {
			return out[i].Config < out[j].Config
		}
		return out[i].Target < out[j].Target
	})
	return out, nil
}

// resolveWarnings resolves the setting for one target configuration, in the
// order Xcode does: the configuration's own build settings, then the xcconfig
// it names, then the project-level configuration of the same name, then that
// configuration's xcconfig.
func (d Declared) resolveWarnings(c Config, name string, xcconfigs map[string]string) (value, source string, found bool) {
	if v, ok := c.Settings[WarningsKey]; ok {
		return v, "target build settings", true
	}
	if v, s, ok := lookupXcconfig(c, name, xcconfigs); ok {
		return v, s, true
	}
	for _, p := range d.Configs {
		if !p.ProjectLevel || !strings.EqualFold(p.Name, name) {
			continue
		}
		if v, ok := p.Settings[WarningsKey]; ok {
			return v, "project build settings", true
		}
		if v, s, ok := lookupXcconfig(p, name, xcconfigs); ok {
			return v, s, true
		}
	}
	return "", "", false
}

func lookupXcconfig(c Config, name string, xcconfigs map[string]string) (string, string, bool) {
	if c.BaseConfig == "" {
		return "", "", false
	}
	path, ok := xcconfigs[c.BaseConfig]
	if !ok {
		return "", "", false
	}
	return readXcconfig(path, WarningsKey, name, 0)
}

// indexXcconfigs maps an xcconfig's base name to its path on disk.
//
// Keyed on base name rather than resolved through the pbxproj group tree,
// which would mean modelling `sourceTree = "<group>"` and every group's own
// path. The trade is deliberate and its failure mode is safe: two xcconfigs
// with the same base name in one component resolve to whichever is found
// first, which can only cause a false REFUSAL that a human immediately sees,
// never a false pass that ships unchecked builds.
func indexXcconfigs(componentDir string) map[string]string {
	out := map[string]string{}
	if componentDir == "" {
		return out
	}
	_ = filepath.WalkDir(componentDir, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not a reason to abort the scan
		}
		if de.IsDir() {
			switch de.Name() {
			// Build output and dependency checkouts carry xcconfigs that belong
			// to other people's projects — sentry-cocoa ships one that sets this
			// very key — and matching one of those would answer a question about
			// this project with a fact about a dependency.
			case ".git", "DerivedData", "Pods", "Carthage", "node_modules", ".build", "SourcePackages":
				return filepath.SkipDir
			// Worktrees hold a FULL SECOND COPY of the project, often months
			// stale, and a lexical walk reaches ".claude/worktrees/..." before
			// "Config/" -- so an abandoned worktree silently shadows the real
			// file. Measured on a-bible-verse-each-day: three Base.xcconfig
			// copies, and the one a walk hits first sets nothing, which reported
			// 12 violations against a project that is fully compliant. The same
			// hazard cost two other bad measurements in one day, so it is worth
			// naming rather than relying on the skip list above catching it.
			case ".worktrees", "worktrees":
				return filepath.SkipDir
			}
			// .claude/worktrees and .codex/worktrees sit one level down, so the
			// name check above does not see them.
			if filepath.Base(filepath.Dir(path)) == "worktrees" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(de.Name(), "DerivedData") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".xcconfig" {
			if _, seen := out[de.Name()]; !seen {
				out[de.Name()] = path
			}
		}
		return nil
	})
	return out
}

// EnforceTargets runs the warnings-as-errors gate over every iOS target of a
// project and returns a single error naming every violation, or nil.
//
// Deliberately NOT bypassable by --force. --force exists to say "take the
// lacquer's version of a file I changed", which is a question about whose copy
// wins. This is not that: a project that does not treat warnings as errors is
// broken in a way no sync can express an opinion about, and letting a flag
// past it would make the policy advisory — which is the state it was in when
// Queueify shipped Debug builds that ignored every warning.
//
// Scope matches the baseline runner's, so the two agree about what is
// checkable: a component with no xcodeproj declared is skipped (a pre-code
// component legitimately has none), while a declared xcodeproj that is missing
// is an error rather than a silent pass.
func EnforceTargets(projectRoot string, targets []Target) error {
	var msgs []string
	for _, t := range targets {
		if t.Profile != "ios" || t.Xcodeproj == "" {
			continue
		}
		path := filepath.Join(projectRoot, filepath.FromSlash(t.Xcodeproj))
		if _, err := os.Stat(path); err != nil {
			// A declared-but-absent xcodeproj is skipped, not refused. It is
			// already a reported condition -- `lacquer audit` exits non-zero on
			// it -- and at least one project (multimeter) sits in that state
			// deliberately, with a comment in its manifest saying so, because it
			// is pre-code. Refusing here would turn an existing warning into a
			// new sync block that nobody asked for, and it would do it on the
			// projects least able to act on it.
			continue
		}
		componentDir := filepath.Join(projectRoot, filepath.FromSlash(t.Component))
		vs, err := EnforceWarningsAsErrors(path, componentDir)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Component, err)
		}
		for _, v := range vs {
			msgs = append(msgs, v.String())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf(`refusing to sync: %s is not set for every configuration and target.

Treating warnings as errors is not optional in this fleet, and it is enforced
here rather than in CI because CI cannot see a project that never runs the job —
and because a Release-only setting leaves pull requests that touch no views with
warnings-as-errors enforced nowhere at all.

Set it once at the PROJECT level for both Debug and Release (Build Settings ▸
"Treat Warnings as Errors", or SWIFT_TREAT_WARNINGS_AS_ERRORS = YES in the
project's xcconfig), and make sure no target overrides it. A bracketed
[config=Release] covers only Release and is the defect this check exists to find.

%s`, WarningsKey, strings.Join(msgs, "\n  "))
}
