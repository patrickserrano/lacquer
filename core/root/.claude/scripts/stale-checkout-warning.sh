#!/bin/sh
# SessionStart hook: warn, in one line per condition, when this checkout is
# stale. Two conditions, both read from what is already on disk. There is no
# network access and no fetch.
#
#   1. HEAD's branch is behind its upstream, as of the last fetch.
#      Fix named: `git merge --ff-only <upstream>`.
#   2. A `lacquer:<key>:start vX` marker in a tracked CLAUDE.md is older than
#      the version in `.lacquer.lock` AT THE UPSTREAM REF (`git show @{u}:...`),
#      i.e. the managed rules in this checkout predate what the project has
#      since synced. Fix named: `lacquer sync`.
#
# A stale checkout looks exactly like a healthy one: the rules the agent reads
# are simply old, and nothing says so. This hook is the something.
#
# It must never cost a session its start, so:
#   - it exits 0 on every path and prints nothing on any error;
#   - the work runs in its own process group under a hard time limit
#     (LIMIT seconds), and output from a run that did not finish is discarded;
#   - it uses only sh and git plus sed/awk/mktemp, so a consumer needs no lacquer
#     install for it to work.
#
# Silent on: an up-to-date checkout, a detached HEAD, a branch with no upstream,
# a repository with no readable `.lacquer.lock` at the upstream ref, no CLAUDE.md
# or no markers in it, and a directory that is not a git work tree.

LIMIT=3

exec 2>/dev/null
dir=${CLAUDE_PROJECT_DIR:-.}
out=$(mktemp "${TMPDIR:-/tmp}/stale-checkout.XXXXXX") || exit 0
trap 'rm -f "$out"' EXIT

# less A B: succeeds when version A is older than version B. A bare counter
# (`v72`) is the legacy stamp for 0.72.0, as in the lacquer's own parser.
less() {
	awk -v a="$1" -v b="$2" '
		function norm(v) { return (v ~ /\./) ? v : "0." v ".0" }
		BEGIN {
			split(norm(a), x, "."); split(norm(b), y, ".")
			for (i = 1; i <= 3; i++) {
				if (x[i] + 0 < y[i] + 0) exit 0
				if (x[i] + 0 > y[i] + 0) exit 1
			}
			exit 1
		}'
}

# work prints the warning lines. Any failure returns silently.
work() {
	cd "$dir" || return 0
	export GIT_TERMINAL_PROMPT=0 GIT_OPTIONAL_LOCKS=0
	[ "$(git rev-parse --is-inside-work-tree)" = true ] || return 0
	# Detached HEAD: nothing here follows an upstream, so there is nothing to
	# be behind.
	git symbolic-ref -q HEAD >/dev/null || return 0
	up=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}') || return 0
	[ -n "$up" ] || return 0

	behind=$(git rev-list --count 'HEAD..@{u}') || behind=
	case $behind in
	'' | *[!0-9]* | 0) ;;
	1) echo "lacquer: this checkout is 1 commit behind $up as of the last fetch; run \`git merge --ff-only $up\` to catch up." ;;
	*) echo "lacquer: this checkout is $behind commits behind $up as of the last fetch; run \`git merge --ff-only $up\` to catch up." ;;
	esac

	# The lock at the upstream ref, not the local one: the local lock moves with
	# the markers, upstream's is what the project has actually synced to.
	latest=$(git show '@{u}:.lacquer.lock' | sed -n 's/.*"version" *: *"\{0,1\}\([0-9][0-9.]*\).*/\1/p' | head -n 1)
	case $latest in '' | *[!0-9.]*) return 0 ;; esac

	stale=
	for f in $(git ls-files -- CLAUDE.md '*/CLAUDE.md'); do
		[ -f "$f" ] || continue
		for m in $(sed -n 's/^<!-- lacquer:\([^: ]*\):start v\([0-9][0-9.]*\) -->$/\1@\2/p' "$f"); do
			key=${m%@*}
			stamp=${m#*@}
			if less "$stamp" "$latest"; then
				stale="${stale:+$stale, }$key v$stamp ($f)"
			fi
		done
	done
	[ -n "$stale" ] || return 0
	echo "lacquer: managed regions older than v$latest at $up: $stale; run \`lacquer sync\` to refresh them."
}

# Job control gives the worker and the watchdog a process group each, so the
# time limit can take a hung git and anything it started down with it.
set -m
(work) >"$out" 2>/dev/null </dev/null &
worker=$!
(
	sleep "$LIMIT"
	kill -TERM -- "-$worker"
	kill -TERM "$worker"
) >/dev/null 2>&1 </dev/null &
watchdog=$!

wait "$worker"
status=$?
kill -TERM -- "-$watchdog"
kill -TERM "$watchdog"

# Only a worker that finished within the limit gets to speak.
if [ "$status" -eq 0 ]; then
	cat "$out"
fi
exit 0
