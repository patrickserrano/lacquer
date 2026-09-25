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
// that resolves, symlinks followed, to somewhere under $HOME, and no path
// component may be a dot-directory or dot-file (~/.ssh, ~/.config/op, ~/.netrc)
// or ~/Library. The exceptions are the fleet's own: any `.worktrees` directory and
// `.claude/worktrees` and `.claude/plans`, where briefs and plans live. At most
// 1 MB is shown, with a visible marker when the file is longer.
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
// for other heads. A file is written to a temporary name (".write-*.tmp") and
// renamed, so a reader never sees half of one; ignore names that do not end in
// ".diff". The directory is 0700 and the files 0600.
package inboxwatch
