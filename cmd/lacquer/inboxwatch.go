package main

import (
	"flag"
	"fmt"
	"io"
	"os"

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
func (o overseerFlags) argv() []string {
	var a []string
	for _, f := range [][2]string{{"--overseer-pane", o.pane}, {"--overseer-title", o.title}, {"--overseer-session", o.session}} {
		if f[1] != "" {
			a = append(a, f[0], f[1])
		}
	}
	return a
}

func addOverseerFlags(fs *flag.FlagSet, getenv func(string) string) *overseerFlags {
	o := &overseerFlags{}
	fs.StringVar(&o.pane, "overseer-pane", getenv("LACQUER_OVERSEER_PANE"), "with inbox watch: the tmux target a reply is typed into, e.g. %12 or work:0.1 (or $LACQUER_OVERSEER_PANE). With none configured, reply is off")
	fs.StringVar(&o.title, "overseer-title", getenv("LACQUER_OVERSEER_TITLE"), "with inbox watch: find the reply pane by its title instead (or $LACQUER_OVERSEER_TITLE); zero or several matches is an error, never a guess")
	fs.StringVar(&o.session, "overseer-session", getenv("LACQUER_OVERSEER_SESSION"), "with --overseer-title: the tmux session to look in (or $LACQUER_OVERSEER_SESSION); default every session")
	return o
}

func newWatchEnv(inboxPath string, isDefault bool, o overseerFlags, roster fleet.Roster, getenv func(string) string) inboxwatch.Env {
	exe, err := executablePath()
	if err != nil {
		exe = "lacquer"
	}
	return inboxwatch.Env{
		InboxPath:    inboxPath,
		InboxDefault: isDefault,
		Overseer:     o.overseer(),
		Roster:       roster,
		Run:          ciwait.GH,
		Cmd:          inboxwatch.OSCommander{},
		// The same function `lacquer console inbox resolve` calls.
		Resolve: inbox.Resolve,
		InTmux:  getenv("TMUX") != "",
		PopupArgv: func(id string) []string {
			argv := []string{exe, "console", "inbox", "popup", "--inbox", inboxPath}
			return append(append(argv, o.argv()...), id)
		},
	}
}

// runInboxWatch is `lacquer console inbox watch`: the live inbox list.
func runInboxWatch(t inboxwatch.Term, env inboxwatch.Env, stderr io.Writer) int {
	m := inboxwatch.NewModel(inboxwatch.Config{
		Tabs:     []inboxwatch.Tab{inboxwatch.InboxTab},
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
// detail view that tmux display-popup runs.
func runInboxPopup(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("inbox popup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inboxFlag := fs.String("inbox", "", "")
	o := addOverseerFlags(fs, getenv)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return fail(stderr, fmt.Errorf("usage: lacquer console inbox popup [--inbox F] <id>"))
	}
	inboxPath, isDefault, err := inbox.Path(*inboxFlag, getenv)
	if err != nil {
		return fail(stderr, err)
	}
	if !stdinIsTerminal() {
		return fail(stderr, fmt.Errorf("inbox popup needs a terminal; it is what a tmux popup runs"))
	}
	env := newWatchEnv(inboxPath, isDefault, *o, fleet.Roster{}, getenv)
	d := inboxwatch.NewDetail(fs.Arg(0), env.Overseer.Configured(), 0, 0)
	if err := inboxwatch.Run(systemTerm(), d, env); err != nil {
		return fail(stderr, err)
	}
	return 0
}
