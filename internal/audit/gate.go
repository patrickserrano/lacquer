package audit

// Gate collects blocking findings independently of how a report is rendered.
// Audit and fleet share this policy, including exit-code precedence.
type Gate struct {
	Clobbered, Baseline, Exclusions, DepIgnores, NotRunInCI, Orphans, Undeclared int
	// LockMismatch counts Behind rows under `audit --ci`, where the lacquer is
	// the version the lock names (see ci.go). Always zero outside --ci, so
	// fleet and local audits are unchanged.
	LockMismatch int
	// UnrunWatch counts watchOS test bundles nothing runs, once the grace date
	// has passed (testtargets.WatchGateFrom); MissingWorkflows counts
	// [[project.covered_elsewhere]] entries naming a workflow that does not
	// exist. Both are audit-only.
	UnrunWatch, MissingWorkflows int
	// StraySwift counts .swift files under no declared Swift component, once
	// the same grace date has passed (swiftcomponents.GateFrom). They rank with
	// Undeclared: Swift nothing declares is a stack the manifest does not cover.
	StraySwift int
}

// ExitCode ranks a lock its own version never wrote first: every other
// attribution is made against that lock, so none of them can be trusted until
// it is re-synced. Then destructive drift, policy violations, and missing stack
// declarations. Informational findings do not block.
func (g Gate) ExitCode() int {
	switch {
	case g.LockMismatch > 0:
		return 8
	case g.Clobbered > 0:
		return 3
	case g.Baseline > 0 || g.Exclusions > 0 || g.DepIgnores > 0 || g.NotRunInCI > 0 || g.Orphans > 0 ||
		g.UnrunWatch > 0 || g.MissingWorkflows > 0:
		return 4
	case g.Undeclared > 0 || g.StraySwift > 0:
		return 6
	default:
		return 0
	}
}
