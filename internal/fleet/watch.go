package fleet

import (
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/testtargets"
)

const (
	WatchDeclared  = "declared"
	WatchElsewhere = "covered elsewhere"
	WatchUnrun     = "unrun"
	WatchNoTests   = "no tests"
)

// watchState reads a project the way `lacquer audit` does and reports how its
// watchOS test bundle is run, "" when it has no watch app or could not be read.
// blocks is whether that state fails the sweep today.
//
// It runs the audit's own comparison, so the sweep and the audit cannot disagree
// about which bundle nothing runs; the one simplification is that a
// covered_elsewhere entry is not checked against the lacquer's managed files,
// which only `audit` has resolved.
func watchState(root string, cfg *config.Config, now time.Time) (state string, blocks bool) {
	if cfg.Project.Xcodeproj == "" {
		return "", false
	}
	proj, read, _, err := testtargets.ReadForAudit(root, cfg.Project.Xcodeproj)
	if err != nil || !read || len(proj.WatchApps()) == 0 {
		return "", false
	}
	var selectors []string
	declared := false
	for _, p := range cfg.Products() {
		selectors = append(selectors, p.TestSelectors()...)
		selectors = append(selectors, p.WatchTestSelectors()...)
		if p.WatchTests != nil {
			declared = true
		}
	}
	var decls []testtargets.Declaration
	for _, c := range cfg.Project.CoveredElsewhere {
		decls = append(decls, testtargets.Declaration{Target: c.Target, Workflow: c.Workflow, Job: c.Job, Reason: c.Reason})
	}
	var notRun []testtargets.NotRun
	for _, n := range cfg.Project.NotRunInCI {
		notRun = append(notRun, testtargets.NotRun{Target: n.Target, Reason: n.Reason, Until: n.Until})
	}
	report := testtargets.Apply(testtargets.Compare(proj.Targets, selectors), testtargets.Verify(root, decls, proj.Targets, nil))
	report = testtargets.Deliberate(report, proj.Targets, notRun, now)
	w := testtargets.Watch(proj, report, len(cfg.Product) > 0, now)
	switch {
	case len(w.Unrun) > 0:
		return WatchUnrun, w.Blocks()
	case len(w.NoBundle) > 0:
		return WatchNoTests, false
	case declared:
		return WatchDeclared, false
	default:
		return WatchElsewhere, false
	}
}
