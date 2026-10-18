package procgroup

import "time"

// One limit per class of call. Each is generous on purpose: a limit that trips on
// a legitimately slow call turns a working audit into a failing one, and the only
// job here is to end a call that will never return. Every number is a multiple of
// the slowest healthy run seen, not a guess at the typical one.
const (
	// LocalGit bounds git calls that read the local repository (ls-files, rev-parse,
	// config, status, remote get-url). They finish in milliseconds; two minutes
	// covers a very large index on a cold disk and a waiting index.lock.
	LocalGit = 2 * time.Minute

	// GitHub bounds a `gh api` call. One HTTPS round trip that answers in a second
	// or two; a minute covers retries on a bad connection.
	GitHub = time.Minute

	// Plutil bounds `plutil -convert json` of one project file, which parses in
	// milliseconds even for a large project.
	Plutil = time.Minute

	// Xcrun bounds `xcrun` locating or running an Xcode tool. It is a path lookup
	// and a fork; the minute is for a first run after an Xcode update.
	Xcrun = time.Minute

	// XcodeGen bounds `xcodegen generate` (and the baseline's regeneration). It
	// reads a spec and writes a project; a large spec takes seconds. Five minutes
	// leaves room for a loaded shared runner.
	XcodeGen = 5 * time.Minute

	// BuildSettings bounds `xcodebuild -showBuildSettings`. It loads the project
	// and its package graph, and a cold derived-data directory is the slow case: a
	// cold run on a project with three package dependencies measured 68 s (9 s
	// warm). Fifteen minutes is over thirteen times that, for a slower disk or a
	// larger graph.
	BuildSettings = 15 * time.Minute

	// PluginCLI bounds one `claude plugin marketplace add` or `plugin install`.
	// Each clones or downloads from the network; a healthy run takes seconds to a
	// minute. The hang this guards is a prompt or login that never returns.
	PluginCLI = 5 * time.Minute

	// SkillInstall bounds one `npx skills@latest add`. npx may first download the
	// package, and the add then clones the skill's repository.
	SkillInstall = 10 * time.Minute
)
