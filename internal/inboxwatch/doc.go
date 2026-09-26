// Package inboxwatch is the live inbox view: a pure model, a renderer and a thin
// loop, with every side effect (gh, tmux, open, pbcopy, the inbox file) behind Env.
//
// # Seeing a PR's diff or an agent's plan (#429)
//
// In an entry's detail popup, v opens what its ref names: the diff of a pull
// request, or the text of a `file:` ref. Both are read-only, sanitised (control
// characters become ^X; a tab and a newline are kept) and scrollable, and both are
// fetched off the UI loop, on the keypress only, never on a refresh.
//
// The diff view needs the PR's repository to be in the roster or the extras, and
// checks that before gh runs. It shows the changed files (`gh pr view --json
// files,headRefOid`) and the diff (`gh pr diff`); a gh failure is said as
// "couldn't load the diff: <reason>", never drawn as an empty diff. Each open asks
// gh which commit the PR is at; the diff itself is fetched once per process for a
// given owner/repo#n@headsha and reused while the head is unchanged. r on a +, -
// or context line is "not this line": see line.go.
//
// A `file:` ref is `file:/abs/path` or `file:~/path`. It must be a regular file
// whose resolved path (symlinks followed) lies under one of two plan roots,
// ~/Developer and ~/.claude/plans (planRoots: a constant, never set by an entry, a
// flag or the environment). Anything else is refused as "outside the plan roots".
// The test is by inode, os.SameFile against each ancestor directory, so a
// differently cased or normalised spelling of a root is still that root and a
// lookalike (~/Developer-evil) is not. Under a root, no path component may start
// with a dot (.ssh, .env, .git), except a `.worktrees` directory and
// `.claude/worktrees`, where the fleet's briefs live. At most 1 MB is shown, with
// a visible marker when the file is longer. Accepted limits: the check and the
// open are two steps, and a hard link placed under a root passes; each takes an
// agent that can already write under $HOME.
//
// # App Store rows on the Stuck tab (#424b)
//
// The Stuck tab also lists three App Store conditions, at the operator's
// thresholds: a version REJECTED or DEVELOPER_REJECTED for 3h, a VALID build
// attached to no version for 3h (only the newest build of its app and platform,
// so a superseded one never sits there), and a version WAITING_FOR_REVIEW for 24h.
// They are computed from asc-snapshot.json, beside the inbox file (ASCSnapshotFile),
// which fleet-ops' asc-status writes on a schedule. lacquer holds no App Store
// Connect credentials and never calls the ASC API: reading that file, through Env
// and on the inbox's own refresh, is the only I/O, and the model stays pure.
//
// A snapshot that is missing, unreadable, malformed, of another schema version,
// over 90 minutes old (measured from its generatedAt), dated in the future, or that
// lists no apps is shown as "couldn't check", never as nothing stuck; so is each
// entry in its errors[], while the other apps' rows still show. A time taken from
// the producer's own first sighting is shown as "at least". A rejected row says
// to dismiss it until Apple responds if you have replied in Resolution Center,
// because the API keeps saying REJECTED then; dismissal is the same x and period
// as any row. The schema and every rule are in docs/asc-snapshot.md.
//
// # The diff cache, for the phone mirror
//
// Every time the diff view fetches a diff (and only then), Env writes the phone's
// copy beside the inbox file:
//
//	<inbox dir>/diffs/<owner>_<repo>_<n>_<headsha>.diff
//
// for example ~/.local/state/lacquer/diffs/patrickserrano_lacquer_496_<40 hex>.diff.
// The head sha is the full 40 (or 64) hex digits gh reports. "/" in owner/repo
// becomes "_", so a name is not unique across repositories whose names contain
// underscores; that is accepted, because the PR number and the sha make a
// collision between two live PRs implausible.
//
// The file is the diff and nothing else: no entry title, no body, no header, no
// agent-written text outside what `gh pr diff` printed. It is sanitised exactly as
// the popup is (every C0 control except newline and tab becomes ^X, DEL becomes ^?,
// C1 becomes ?), so a reader can show it without a terminal escape reaching the
// screen. Each file is at most 256 KB. A longer diff is cut at a line boundary and
// its last line is exactly
//
//	[lacquer: diff truncated at 256 KB]
//
// (DiffCacheMarker), so a file that does not end with that line is the whole diff.
// Only the newest head of a PR is kept: writing a new one removes that PR's files
// for other heads, and any ".write-<digits>.tmp" left by a write that never
// finished, once it is ten minutes old. A file is written to a temporary name (".write-*.tmp") and
// renamed, so a reader never sees half of one; ignore names that do not end in
// ".diff". The directory is 0700 and the files 0600.
package inboxwatch
