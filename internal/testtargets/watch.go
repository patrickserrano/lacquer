package testtargets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WatchGateFrom is the first day a watchOS test bundle that nothing runs fails
// `lacquer audit` (exit 4). Before it, the same finding is printed with this
// date and does not change the exit code.
//
// The single switch for the grace period: the release that ships this gate plus
// fourteen days, so every watch project sees the warning in a fleet sweep before
// its CI can go red on it. A var only so tests can put "today" on either side.
//
// It is also the stray-Swift gate's date (swiftcomponents.GateFrom, #522 U4),
// by ruling one date for both: moving it moves both.
var WatchGateFrom = time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

// WatchGateDate is WatchGateFrom as a manifest-style date.
func WatchGateDate() string { return WatchGateFrom.Format("2006-01-02") }

// WatchReport is what the audit says about watchOS targets. Every field is empty
// for a project with no watch app, which is the fleet's overwhelmingly common
// case and must produce no output at all.
type WatchReport struct {
	// Unrun are watchOS unit-test bundles still uncovered after every selector,
	// verified covered_elsewhere entry and in-term not_run_in_ci entry has been
	// applied: nothing runs them.
	Unrun []WatchUnrun
	// NoBundle are watch apps in a project with no watchOS unit-test bundle at
	// all. Nothing to declare; a notice.
	NoBundle []App
	// Products is true when the manifest declares [[product]] blocks, so the
	// table to add is [product.watch_tests] rather than [project.watch_tests].
	Products bool
	// Now decides whether Unrun blocks: on or after WatchGateFrom.
	Now time.Time
}

// WatchUnrun is one watch bundle nothing runs, and the schemes whose
// TestAction lists it (the watch_tests scheme to declare).
type WatchUnrun struct {
	Target  string
	Schemes []string
}

// Watch builds the report from the project read and the finished uncovered
// report (after Apply and Deliberate).
func Watch(p Project, r Report, products bool, now time.Time) WatchReport {
	w := WatchReport{Products: products, Now: now}
	for _, t := range r.Uncovered {
		if IsWatchBundle(t) {
			w.Unrun = append(w.Unrun, WatchUnrun{Target: t.Name, Schemes: p.SchemesTesting(t.Name)})
		}
	}
	hasBundle := false
	for _, t := range p.Targets {
		if IsWatchBundle(t) {
			hasBundle = true
		}
	}
	if !hasBundle {
		w.NoBundle = p.WatchApps()
	}
	return w
}

// Blocks reports whether the unrun bundles fail the audit today.
func (w WatchReport) Blocks() bool { return !w.Now.Before(WatchGateFrom) }

// Blocking is the number of findings that fail the audit today.
func (w WatchReport) Blocking() int {
	if !w.Blocks() {
		return 0
	}
	return len(w.Unrun)
}

// FormatWatch renders the report, empty when there is nothing to say.
func FormatWatch(w WatchReport) string {
	var b strings.Builder
	if len(w.Unrun) > 0 {
		b.WriteString("\nwatchOS test bundles nothing runs:\n")
		table := "[project.watch_tests]"
		if w.Products {
			table = "[product.watch_tests]   # under the [[product]] that ships this watch app"
		}
		for _, u := range w.Unrun {
			fmt.Fprintf(&b, "  %s\n", u.Target)
			b.WriteString("    Add to .lacquer.toml so the managed Watch Tests job runs it on every pull request:\n\n")
			fmt.Fprintf(&b, "      %s\n", table)
			switch len(u.Schemes) {
			case 0:
				fmt.Fprintf(&b, "      scheme      = \"<a scheme whose test action lists %s>\"\n", u.Target)
			default:
				fmt.Fprintf(&b, "      scheme      = %q\n", u.Schemes[0])
			}
			fmt.Fprintf(&b, "      test_target = %q\n\n", u.Target)
			switch {
			case len(u.Schemes) == 0:
				b.WriteString("    No shared scheme's test action lists it, so there is no scheme to name yet: add one.\n")
			case len(u.Schemes) > 1:
				fmt.Fprintf(&b, "    Several schemes test it (%s); any one will do.\n", strings.Join(u.Schemes, ", "))
			}
		}
		b.WriteString("    If a project-owned workflow already runs it, declare [[project.covered_elsewhere]]\n")
		b.WriteString("    with that workflow, its job, and a reason naming both; it is checked against the file.\n")
		if w.Blocks() {
			fmt.Fprintf(&b, "    BLOCKING since %s: `lacquer audit` exits 4 until each one runs or is declared.\n", WatchGateDate())
		} else {
			fmt.Fprintf(&b, "    Not blocking yet: from %s this fails `lacquer audit` (exit 4).\n", WatchGateDate())
		}
	}
	for _, a := range w.NoBundle {
		fmt.Fprintf(&b, "\nnotice: watch app %s has no test bundle; a [product.watch_tests] cannot be declared until it has one.\n", a.Name)
	}
	return b.String()
}

// ReadForAudit reads the Xcode project the audit compares selectors against.
//
// A TRACKED project.pbxproj is the project: it is what builds. When none is
// tracked and a project.yml sits beside the .xcodeproj, the spec is read
// instead, even if a generated pbxproj happens to be on disk: a clean checkout
// has none, so reading a generated one would make the audit's answer depend on
// whether xcodegen had run on this machine. Outside a git work tree a pbxproj on
// disk is taken as the project, as before.
//
// source names what was read, for the report.
func ReadForAudit(projectRoot, xcodeproj string) (p Project, read bool, source string, err error) {
	pbx := filepath.Join(projectRoot, filepath.FromSlash(xcodeproj), "project.pbxproj")
	spec := filepath.Join(projectRoot, filepath.FromSlash(filepath.ToSlash(filepath.Dir(xcodeproj))), "project.yml")
	if _, statErr := os.Stat(spec); statErr == nil && !tracked(projectRoot, pbx) {
		p, read, err = ParseSpec(spec)
		return p, read, spec, err
	}
	p, read, err = ParseProject(pbx)
	return p, read, pbx, err
}

// tracked reports whether path is committed to the git repository at root, or,
// when root is not a git work tree, whether it exists.
func tracked(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		_, statErr := os.Stat(path)
		return statErr == nil
	}
	cmd = exec.Command("git", "ls-files", "--error-unmatch", "--", filepath.ToSlash(rel))
	cmd.Dir = root
	return cmd.Run() == nil
}
