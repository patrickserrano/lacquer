package audit

// Gate collects blocking findings independently of how a report is rendered.
// Audit and fleet share this policy, including exit-code precedence.
type Gate struct {
	Clobbered, Baseline, Exclusions, DepIgnores, NotRunInCI, Orphans, Undeclared int
}

// ExitCode ranks destructive drift before policy violations, then missing stack
// declarations. Informational findings do not block.
func (g Gate) ExitCode() int {
	switch {
	case g.Clobbered > 0:
		return 3
	case g.Baseline > 0 || g.Exclusions > 0 || g.DepIgnores > 0 || g.NotRunInCI > 0 || g.Orphans > 0:
		return 4
	case g.Undeclared > 0:
		return 6
	default:
		return 0
	}
}
