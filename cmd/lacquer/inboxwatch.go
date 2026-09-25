package main

import (
	"encoding/hex"
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
			// The id is agent-written and goes to tmux as part of a command tmux may
			// expand as a format, so it travels hex-encoded: no # can be in it.
			argv := []string{exe, "console", "inbox", "popup", "--inbox", inboxPath}
			return append(append(argv, o.argv()...), "--id-hex="+hex.EncodeToString([]byte(id)))
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
	o := addOverseerFlags(fs, getenv)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id := fs.Arg(0)
	switch {
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
	env := newWatchEnv(inboxPath, isDefault, *o, fleet.Roster{}, getenv)
	d := inboxwatch.NewDetail(id, env.Overseer.Configured(), 0, 0)
	if err := inboxwatch.Run(systemTerm(), d, env); err != nil {
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
