// Package lacquer exposes the version of the SOURCE TREE this binary was
// compiled from, as distinct from the version of the content it later reads.
//
// Those two are routinely different, and until now nothing noticed. `sync`
// resolves its content from LACQUER_ROOT at run time, so a binary built weeks
// ago from a feature worktree renders TODAY's profiles with THAT DAY's logic —
// and the banner, which reports the root's VERSION, calls it current.
//
// Not hypothetical. A binary built from a worktree predating the lefthook merge
// (#193) rewrote a project's lefthook.yml from 135 lines to 60, silently
// dropping an entire profile's hooks, while printing "lacquer: 1.5.4".
//
// runtime/debug.ReadBuildInfo cannot substitute for this. Go resolves
// vcs.revision from the repository, and for a GIT WORKTREE it reports the MAIN
// checkout's HEAD rather than the worktree's — measured: a binary built at
// e5b55dd reported ecbe194. Worktree builds are exactly the ones that go stale
// here, so the automatic mechanism is blind precisely where it is needed.
//
// //go:embed is immune: it resolves at compile time against the source tree
// actually being compiled, whatever git believes about it.
package lacquer

import _ "embed"

// BuiltVersion is the contents of VERSION at compile time. Compare it against
// the VERSION under LACQUER_ROOT to detect a stale binary. Retains the file's
// trailing newline; callers should TrimSpace.
//
//go:embed VERSION
var BuiltVersion string
