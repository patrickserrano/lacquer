package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/inboxwatch"
)

// systemTerm and stdinIsTerminal are what `inbox watch` and its popup run on;
// tests replace them, since a test has no terminal.
var (
	systemTerm      = inboxwatch.SystemTerm
	stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) }
	executablePath  = os.Executable
)

// overseerFlags is where a reply is typed, from the flags and $LACQUER_OVERSEER_*.
type overseerFlags struct{ pane, title, session string }

func (o overseerFlags) overseer() inboxwatch.Overseer {
	return inboxwatch.Overseer{Pane: o.pane, Title: o.title, Session: o.session}
}

// argv is the flags that carry this configuration to a process tmux starts,
// which does not inherit them: a popup is run by the tmux server, not by us.
// All three are passed even when empty. The popup's flags default to
// $LACQUER_OVERSEER_*, read from the tmux server's environment, so leaving one
// off could switch reply on in the popup when the list has it off.
func (o overseerFlags) argv() []string {
	return []string{"--overseer-pane=" + o.pane, "--overseer-title=" + o.title, "--overseer-session=" + o.session}
}

func addOverseerFlags(fs *flag.FlagSet, getenv func(string) string) *overseerFlags {
	o := &overseerFlags{}
	fs.StringVar(&o.pane, "overseer-pane", getenv("LACQUER_OVERSEER_PANE"), "with inbox watch: the tmux target a reply is typed into, e.g. %12 or work:0.1 (or $LACQUER_OVERSEER_PANE). With none configured, reply is off")
	fs.StringVar(&o.title, "overseer-title", getenv("LACQUER_OVERSEER_TITLE"), "with inbox watch: find the reply pane by its title instead (or $LACQUER_OVERSEER_TITLE); zero or several matches is an error, never a guess")
	fs.StringVar(&o.session, "overseer-session", getenv("LACQUER_OVERSEER_SESSION"), "with --overseer-title: the tmux session to look in (or $LACQUER_OVERSEER_SESSION); default every session")
	return o
}

func newWatchEnv(inboxPath string, isDefault bool, o overseerFlags, roster fleet.Roster, getenv func(string) string, extra ...string) inboxwatch.Env {
	exe, err := executablePath()
	if err != nil {
		exe = "lacquer"
	}
	env := inboxwatch.Env{
		ExtraRepos:   extra,
		InboxPath:    inboxPath,
		InboxDefault: isDefault,
		Overseer:     o.overseer(),
		Roster:       roster,
		Run:          ciwait.GH,
		Cmd:          inboxwatch.OSCommander{},
		// The same function `lacquer console inbox resolve` calls.
		Resolve: inbox.Resolve,
		InTmux:  getenv("TMUX") != "",
		// An id or an issue ref is agent-written and goes to tmux as part of a
		// command tmux may expand as a format, so it travels hex-encoded: no # can
		// be in it.
	}
	// The popup is a separate process that knows only what its command line says,
	// so the repositories a reply may be commented on travel with it.
	repos := env.KnownRepos()
	env.PopupArgv = func(id string) []string { return popupArgv(exe, inboxPath, o, repos, "--id-hex=", id) }
	env.IssueArgv = func(ref string) []string { return popupArgv(exe, inboxPath, o, repos, "--issue-hex=", ref) }
	return env
}

func popupArgv(exe, inboxPath string, o overseerFlags, repos []string, flag, text string) []string {
	argv := append([]string{exe, "console", "inbox", "popup", "--inbox", inboxPath}, o.argv()...)
	for _, r := range repos {
		argv = append(argv, "--repo="+r)
	}
	return append(argv, flag+hex.EncodeToString([]byte(text)))
}

// extraRepoFlags is the repositories the Later and PRs tabs cover besides the
// roster's: foxy-prs added lacquer, fleet-ops and rail-web this way, since a
// roster sweeps projects and these are the tooling around them. Repeat the flag,
// or comma-separate $LACQUER_EXTRA_REPOS.
type extraRepoFlags struct {
	list   []string
	envErr error // a bad $LACQUER_EXTRA_REPOS, reported when the view starts
}

func (e *extraRepoFlags) String() string { return strings.Join(e.list, ",") }
func (e *extraRepoFlags) Set(v string) error {
	for _, r := range strings.Split(v, ",") {
		if r = strings.TrimSpace(r); r != "" {
			if o, n, ok := strings.Cut(r, "/"); !ok || o == "" || n == "" || strings.Contains(n, "/") {
				return fmt.Errorf("%q is not owner/name", r)
			}
			e.list = append(e.list, r)
		}
	}
	return nil
}

func addExtraRepoFlag(fs *flag.FlagSet, getenv func(string) string) *extraRepoFlags {
	e := &extraRepoFlags{}
	// The environment is the default; a flag on the command line adds to it.
	if v := getenv("LACQUER_EXTRA_REPOS"); v != "" {
		if err := e.Set(v); err != nil {
			e.envErr = fmt.Errorf("$LACQUER_EXTRA_REPOS: %w", err)
		}
	}
	fs.Var(e, "extra-repo", "with inbox watch: an owner/name the Later and PRs tabs cover besides the roster's (repeatable; or comma-separated $LACQUER_EXTRA_REPOS)")
	return e
}

// runInboxShow is `inbox watch --show ID`: one entry's detail, printed.
func runInboxShow(inboxPath, id string, stdout, stderr io.Writer) int {
	text, err := inboxwatch.Show(inboxPath, id)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprint(stdout, text)
	return 0
}

// runInboxWatch is `lacquer console inbox watch`: the live inbox list.
func runInboxWatch(t inboxwatch.Term, env inboxwatch.Env, stderr io.Writer) int {
	m := inboxwatch.NewModel(inboxwatch.Config{
		Tabs:     inboxwatch.AllTabs,
		CanReply: env.Overseer.Configured(),
		HasRepos: env.HasRepos(),
	}, 0, 0)
	if err := inboxwatch.Run(t, m, env); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// isInboxPopup reports whether the console arguments are `inbox popup`. Like
// the Stop hook it is decided before the lacquer-root checks: tmux starts it
// with none of the operator's shell environment, and a detail view needs only
// the inbox file.
func isInboxPopup(args []string) bool {
	return len(args) >= 2 && args[0] == "inbox" && args[1] == "popup"
}

// runInboxPopup is the hidden `lacquer console inbox popup [flags] <id>`, the
// detail view that tmux display-popup runs. A popup is closed by tmux the moment
// its command exits, so an error printed and then exited on is never seen: on a
// terminal it waits for a key first.
func runInboxPopup(args []string, getenv func(string) string, stderr io.Writer) int {
	code := popupMain(args, getenv, stderr)
	if code != 0 && stdinIsTerminal() {
		fmt.Fprint(stderr, "\n(press any key to close)")
		waitForKey()
	}
	return code
}

func popupMain(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("inbox popup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inboxFlag := fs.String("inbox", "", "")
	idHex := fs.String("id-hex", "", "the entry id, hex-encoded (what the list passes)")
	issueHex := fs.String("issue-hex", "", "show a GitHub issue instead: its owner/name#number, hex-encoded (what a Later row passes)")
	o := addOverseerFlags(fs, getenv)
	repos := &extraRepoFlags{}
	fs.Var(repos, "repo", "a repository a reply may also be commented on (repeatable; what the list passes)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id := fs.Arg(0)
	switch {
	case *issueHex != "":
		raw, err := hex.DecodeString(*issueHex)
		if err != nil || fs.NArg() != 0 || *idHex != "" {
			return fail(stderr, fmt.Errorf("inbox popup: --issue-hex is not hex, or an id was also given"))
		}
		id = string(raw)
	case *idHex != "":
		raw, err := hex.DecodeString(*idHex)
		if err != nil || fs.NArg() != 0 {
			return fail(stderr, fmt.Errorf("inbox popup: --id-hex is not hex, or an id was also given"))
		}
		id = string(raw)
	case fs.NArg() != 1:
		return fail(stderr, fmt.Errorf("usage: lacquer console inbox popup [--inbox F] <id>"))
	}
	inboxPath, isDefault, err := inbox.Path(*inboxFlag, getenv)
	if err != nil {
		return fail(stderr, err)
	}
	if !stdinIsTerminal() {
		return fail(stderr, fmt.Errorf("inbox popup needs a terminal; it is what a tmux popup runs"))
	}
	env := newWatchEnv(inboxPath, isDefault, *o, fleet.Roster{}, getenv, repos.list...)
	detail := inboxwatch.NewDetail(id, env.Overseer.Configured(), 0, 0)
	detail.Repos = env.KnownRepos()
	var p inboxwatch.Program = detail
	if *issueHex != "" {
		p = inboxwatch.NewIssuePopup(id, env.Overseer.Configured(), 0, 0)
	}
	if err := inboxwatch.Run(systemTerm(), p, env); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// waitForKey blocks for one key press on the terminal.
var waitForKey = func() {
	fd := int(os.Stdin.Fd())
	if old, err := term.MakeRaw(fd); err == nil {
		defer term.Restore(fd, old)
	}
	os.Stdin.Read(make([]byte, 1))
}
