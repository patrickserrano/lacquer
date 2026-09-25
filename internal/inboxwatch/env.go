package inboxwatch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/producers"
)

// Commander runs an external program. tmux, open and pbcopy all go through it,
// so a test can record what would have run instead of running it.
type Commander interface {
	Run(stdin, name string, args ...string) (stdout string, err error)
}

// OSCommander runs the real thing. On failure the error carries stderr.
type OSCommander struct{}

func (OSCommander) Run(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed program names; arguments are ids, urls and tmux targets
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), errors.New(msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// Overseer says where a reply is typed. There is no default and no guess: the
// operator's projects and roles are not this tool's to name, and typing into the
// wrong pane is worse than not replying.
type Overseer struct {
	// Pane is a tmux target: a pane id (%12), or session:window.pane.
	Pane string
	// Title, with Session, finds the pane instead: the one pane whose title is
	// Title in Session (every session when Session is empty). Zero or several
	// matches is an error, not a pick.
	Title   string
	Session string
}

// Configured reports whether a reply has anywhere to go.
func (o Overseer) Configured() bool { return o.Pane != "" || o.Title != "" }

// DisabledWhy is the sentence shown when replies are off.
const DisabledWhy = "no overseer pane configured: pass --overseer-pane or set $LACQUER_OVERSEER_PANE"

// target resolves the tmux target to type into.
func (o Overseer) target(c Commander) (string, error) {
	switch {
	case o.Pane != "":
		return o.Pane, nil
	case o.Title == "":
		return "", errors.New(DisabledWhy)
	}
	args := []string{"list-panes", "-F", "#{pane_id}\t#{pane_title}"}
	if o.Session != "" {
		args = append(args, "-s", "-t", o.Session)
	} else {
		args = append(args, "-a")
	}
	out, err := c.Run("", "tmux", args...)
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	var ids []string
	for _, l := range strings.Split(out, "\n") {
		id, title, _ := strings.Cut(l, "\t")
		if title == o.Title && id != "" {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("no pane titled %q: is the overseer running?", o.Title)
	}
	return "", fmt.Errorf("%d panes are titled %q (%s); name one with --overseer-pane", len(ids), o.Title, strings.Join(ids, ", "))
}

// Item is one open entry as the list shows it.
type Item struct {
	ID        string
	Type      inbox.Type
	CreatedAt time.Time
	Title     string
	Ref       string
	Replied   bool
}

// Data is one read of the inbox.
type Data struct {
	Items     []Item
	Malformed int
}

// sortItems puts what needs the operator first: ACTIONs they have not answered,
// then ACTIONs they answered (waiting on the overseer), then FYIs. The sort is
// stable, so within a group entries stay in file order, oldest first.
func sortItems(items []Item) {
	rank := func(i Item) int {
		r := 0
		if i.Type != inbox.Action {
			r += 2
		}
		if i.Replied {
			r++
		}
		return r
	}
	sort.SliceStable(items, func(a, b int) bool { return rank(items[a]) < rank(items[b]) })
}

// Cmd is a side effect a Program asks for. Programs never perform one: the loop
// hands it to Env.Exec and feeds the answer back as an Event.
type Cmd struct {
	Kind CmdKind
	ID   string
	Text string // a URL to open, text to copy, or a reply
}

type CmdKind int

const (
	CmdLoad    CmdKind = iota // read the inbox
	CmdEntry                  // read one entry for the detail view
	CmdResolve                // resolve ID
	CmdReply                  // type Text into the overseer pane for ID
	CmdOpen                   // open the URL in Text
	CmdCopy                   // copy Text
	CmdPopup                  // show ID's detail in a tmux popup
	CmdHarvest                // record PR merges
)

// Answers to Cmds.
type (
	// LoadedEvent answers CmdLoad.
	LoadedEvent struct {
		Data Data
		// Err means the inbox could not be read and Data is empty; Warn means it
		// was read and something beside it was not.
		Err  string
		Warn string
		At   time.Time
	}
	// EntryEvent answers CmdEntry.
	EntryEvent struct {
		Entry    inbox.Entry
		Found    bool
		Reply    Reply
		HasReply bool
		Err      string
	}
	// DoneEvent answers CmdResolve, CmdOpen, CmdCopy and CmdPopup: a note for the
	// status bar, and whether it worked.
	DoneEvent struct {
		Kind CmdKind
		ID   string
		OK   bool
		Note string
	}
	// RepliedEvent answers CmdReply.
	RepliedEvent struct {
		OK   bool
		Note string
	}
	// HarvestedEvent answers CmdHarvest.
	HarvestedEvent struct {
		Added       int
		Unavailable []string
		At          time.Time
	}
)

// Env is everything a Program's Cmds touch.
type Env struct {
	InboxPath string
	// InboxDefault is true when the path is the default one, where a missing
	// file means nothing has written to it yet rather than a bad --inbox.
	InboxDefault bool
	Overseer     Overseer
	// Roster and Run feed the merge harvest. A roster naming no repository
	// means merges are not being recorded, and the list says so.
	Roster fleet.Roster
	Run    ciwait.Runner
	Cmd    Commander
	// Resolve is the same function `lacquer console inbox resolve` calls.
	Resolve func(path, id string) (inbox.Entry, error)
	Now     func() time.Time
	// PopupArgv is the command tmux runs to show one entry's detail.
	PopupArgv func(id string) []string
	// InTmux says whether a popup can be shown.
	InTmux bool
}

// HasRepos reports whether the roster names any repository to harvest.
func (e Env) HasRepos() bool {
	for _, p := range e.Roster.Project {
		if p.Repo != "" {
			return true
		}
	}
	return false
}

// Exec performs c and returns what happened.
func (e Env) Exec(c Cmd) Event {
	switch c.Kind {
	case CmdLoad:
		return e.load()
	case CmdEntry:
		return e.entry(c.ID)
	case CmdResolve:
		if _, err := e.Resolve(e.InboxPath, c.ID); err != nil {
			return DoneEvent{Kind: c.Kind, ID: c.ID, Note: err.Error()}
		}
		return DoneEvent{Kind: c.Kind, ID: c.ID, OK: true, Note: "resolved " + c.ID}
	case CmdReply:
		return e.reply(c)
	case CmdOpen:
		if _, err := e.Cmd.Run("", openProgram(), c.Text); err != nil {
			return DoneEvent{Kind: c.Kind, Note: "open failed: " + err.Error()}
		}
		return DoneEvent{Kind: c.Kind, OK: true, Note: "opened link"}
	case CmdCopy:
		if _, err := e.Cmd.Run(c.Text, "pbcopy"); err != nil {
			return DoneEvent{Kind: c.Kind, Note: "copy failed: " + err.Error()}
		}
		return DoneEvent{Kind: c.Kind, ID: c.ID, OK: true, Note: "copied " + c.Text}
	case CmdPopup:
		return e.popup(c.ID)
	case CmdHarvest:
		return e.harvest()
	}
	return DoneEvent{Note: fmt.Sprintf("unknown command %d", c.Kind)}
}

func openProgram() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) load() LoadedEvent {
	at := e.now()
	entries, malformed, err := inbox.ListOpen(e.InboxPath)
	if err != nil && !(e.InboxDefault && errors.Is(err, fs.ErrNotExist)) {
		return LoadedEvent{Err: err.Error(), At: at}
	}
	replies, rerr := ReadReplies(RepliesPath(e.InboxPath))
	var items []Item
	for _, en := range entries {
		_, replied := replies[en.ID]
		items = append(items, Item{ID: en.ID, Type: en.Type, CreatedAt: en.CreatedAt, Title: en.Title, Ref: en.Ref, Replied: replied})
	}
	sortItems(items)
	ev := LoadedEvent{Data: Data{Items: items, Malformed: malformed}, At: at}
	if rerr != nil {
		ev.Warn = "replies unreadable: " + rerr.Error()
	}
	return ev
}

func (e Env) entry(id string) EntryEvent {
	all, _, err := inbox.ReadAll(e.InboxPath)
	if err != nil {
		return EntryEvent{Err: err.Error()}
	}
	var ev EntryEvent
	for _, en := range all { // the latest record for an id wins
		if en.ID == id {
			ev.Entry, ev.Found = en, true
		}
	}
	replies, rerr := ReadReplies(RepliesPath(e.InboxPath))
	if rerr != nil {
		ev.Err = "replies unreadable: " + rerr.Error()
	}
	ev.Reply, ev.HasReply = replies[id]
	return ev
}

// reply types "[inbox <id>] <text>" into the overseer pane, exactly as if the
// operator had typed it there, then records it. The entry's title is left out on
// purpose: it is agent-written text, and typed here it would arrive in the
// overseer's input looking like the operator's own words. The overseer looks the
// title up by id. (foxy-inbox made the same change, 4a8d084.) The record is written
// only after both keystrokes went in: a reply the overseer never got must not
// read as answered.
func (e Env) reply(c Cmd) Event {
	if !e.Overseer.Configured() {
		return RepliedEvent{Note: "reply disabled: " + DisabledWhy}
	}
	pane, err := e.Overseer.target(e.Cmd)
	if err != nil {
		return RepliedEvent{Note: err.Error()}
	}
	msg := fmt.Sprintf("[inbox %s] %s", clean(c.ID), c.Text)
	if _, err := e.Cmd.Run("", "tmux", "send-keys", "-t", pane, "-l", msg); err != nil {
		return RepliedEvent{Note: "tmux send-keys: " + err.Error()}
	}
	if _, err := e.Cmd.Run("", "tmux", "send-keys", "-t", pane, "Enter"); err != nil {
		return RepliedEvent{Note: "tmux send-keys Enter: " + err.Error()}
	}
	if err := AppendReply(RepliesPath(e.InboxPath), c.ID, e.now(), c.Text); err != nil {
		return RepliedEvent{Note: "sent, but the reply was not recorded: " + err.Error()}
	}
	return RepliedEvent{OK: true, Note: "sent"}
}

func (e Env) popup(id string) Event {
	if !e.InTmux {
		return DoneEvent{Kind: CmdPopup, Note: "the detail view is a tmux popup, and this is not tmux"}
	}
	cmd := shellJoin(e.PopupArgv(id))
	if strings.Contains(cmd, "#") {
		// tmux expands formats in the command on some versions and not on others
		// (3.7c passes it through untouched, so doubling every # would corrupt it
		// there), which leaves no escaping that is right on both. The command
		// carries no agent-written text (the id goes hex-encoded), so a # here is
		// in a path or setting the operator chose, and is refused rather than guessed.
		return DoneEvent{Kind: CmdPopup, ID: id, Note: "cannot open the popup: a # in the inbox path or overseer setting cannot be passed to tmux safely"}
	}
	args := []string{"display-popup", "-w", "80%", "-h", "70%",
		"-T", formatQuote(fmt.Sprintf(" inbox %s  (r reply · d resolve · o link · c copy · q close) ", clean(id))),
		"-E", cmd}
	if _, err := e.Cmd.Run("", "tmux", args...); err != nil {
		return DoneEvent{Kind: CmdPopup, ID: id, Note: "tmux display-popup: " + err.Error()}
	}
	return DoneEvent{Kind: CmdPopup, ID: id, OK: true}
}

func (e Env) harvest() Event {
	at := e.now()
	res := producers.HarvestMerges(producers.HarvestOptions{InboxPath: e.InboxPath, Roster: e.Roster, Now: at, Run: e.Run})
	return HarvestedEvent{Added: len(res.Added), Unavailable: res.Unavailable, At: at}
}

// formatQuote escapes what tmux would expand in a format string. -T is one on
// every version, so an id holding "#(cmd)" would run cmd when the popup opened
// (verified on tmux 3.7c), and the id comes from a file any agent can write.
// (foxy-inbox's tmux_literal.) The -E command is handled differently, see popup.
func formatQuote(s string) string { return strings.ReplaceAll(s, "#", "##") }

// shellJoin quotes argv for the shell tmux runs a popup command with.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:,@%+") == "" {
			q[i] = a
		} else {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(q, " ")
}
