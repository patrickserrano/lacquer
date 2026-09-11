package audit

import (
	"regexp"
	"strings"
)

// ActionBump is one `uses:` reference that differs between a managed workflow as
// it sits in the project and the same workflow as the lacquer would render it:
// the same action, a different version.
//
// From is the lacquer's ref and To is the project's, because that is the
// direction of the fix — the project is AHEAD and the lacquer has to catch up.
// Every other divergence this package reports runs the other way, so the naming
// is worth stating rather than inferring.
type ActionBump struct {
	Action string // "actions/checkout"
	From   string // the ref profiles/ renders today
	To     string // the ref on disk in the project
}

// usesRef matches a workflow step's action reference and splits it into the part
// that must be identical (everything left of the version) and the part that is
// allowed to move.
//
// The action name is required to look like `owner/repo` — one or more path
// segments of word characters — which is what excludes the two `uses:` forms
// that are not versioned dependencies at all: a local composite action
// (`./.github/actions/build`, no `@`, and a leading dot the first class rejects)
// and a container step (`docker://alpine:3`, whose `//` cannot match a path
// segment). Neither is something Dependabot bumps, and treating one as a bump
// would classify a real edit to a workflow as promotable churn.
//
// The trailing group is the `# v7.0.1` comment convention. It is allowed to
// differ, because a SHA pin and its comment move together and a rule that froze
// the comment would refuse every SHA bump — the exact case this is for.
var usesRef = regexp.MustCompile(`^(\s*(?:-\s+)?uses:\s+)([A-Za-z0-9][\w.-]*(?:/[\w.-]+)+)@(\S+)((?:\s*#.*)?)$`)

// ActionBumps returns the action-version changes that turn the lacquer's
// rendered content into what the project has on disk — or nil when the two
// differ in ANY other way.
//
// Nil is the important half. This is the predicate behind a claim ("this edit is
// safe to promote and nothing else in the file changed"), so it has to be a
// proof rather than a heuristic: equal line counts, every differing line a
// `uses:` line on both sides, identical indentation and identical action name,
// and an actual version change rather than a reworded comment. A line inserted,
// a step reordered, a `with:` argument edited — any of those changes the line
// count or fails the pair match, and the whole file falls back to being ordinary
// drift. There is no partial answer: "mostly an action bump" is the shape that
// would let a real local change ride into the lacquer unread.
//
// Duplicates collapse. darndest-api-proxy's Dependabot pull request of
// 2026-09-11 rewrote `actions/checkout` on eight separate lines across three
// workflows; the promotion is one edit, and reporting it eight times is how a
// report stops being read.
func ActionBumps(rendered, onDisk string) []ActionBump {
	if rendered == onDisk {
		return nil
	}
	want := strings.Split(rendered, "\n")
	got := strings.Split(onDisk, "\n")
	// A line added or removed is not a version bump, whatever else the file says.
	if len(want) != len(got) {
		return nil
	}
	var out []ActionBump
	seen := map[ActionBump]bool{}
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		w := usesRef.FindStringSubmatch(want[i])
		g := usesRef.FindStringSubmatch(got[i])
		switch {
		case w == nil || g == nil:
			return nil // not an action reference on one side or the other
		case w[1] != g[1] || w[2] != g[2]:
			return nil // re-indented, or a different action entirely
		case w[3] == g[3]:
			return nil // same version, so the only edit was to the comment
		}
		b := ActionBump{Action: w[2], From: w[3], To: g[3]}
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

// Promotable returns the rows whose entire divergence is an action-version bump.
//
// These stay Modified/Conflict and still clobber, so the exit code and the sync
// refusal are unchanged: the project's edit really will be lost to the next
// sync, and saying otherwise would be the second-author problem wearing a
// friendlier label. What changes is what the report can tell you to DO about it.
// Before this, a Dependabot bump to a managed workflow arrived as an
// unattributed "you changed it, the lacquer didn't" against a file the project
// did not knowingly touch, and the cheapest response was to revert it — which is
// how a wanted security bump gets thrown away.
func Promotable(rows []Row) []Row {
	var out []Row
	for _, r := range rows {
		if len(r.Bumps) > 0 {
			out = append(out, r)
		}
	}
	return out
}
