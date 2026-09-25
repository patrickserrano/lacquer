// Package inbox is the fourth source `lacquer console` joins, alongside
// `lacquer fleet` (TRUE), `claude agents --json` (IN FLIGHT) and `gh pr list`
// (AWAITING REVIEW) -- see internal/console's package doc. It answers the two
// questions those three cannot: what is waiting on the OPERATOR to decide,
// and what work finished and nobody has looked at yet.
//
// Both fall through the existing three sources by construction. A decision
// raised in a prose message inside a session's scrollback is not a PR, not a
// live session, and not a fleet finding -- it is gone the moment that
// terminal is closed. A subagent that completes, reports a result to its
// caller, and exits leaves nothing behind either: `claude agents --json`
// reports LIVE sessions only, so the moment one finishes it simply
// disappears from that view, cost analysis and proof evidence and all.
//
// The store is a flat JSONL file, one Entry per line, append-only except for
// Resolve (which rewrites the file to flip one entry's ResolvedAt). Appends
// must tolerate several sessions and subagents on the same machine writing
// concurrently (Add uses O_APPEND with a single Write of one line -- see its
// doc comment), and reads must tolerate a line left corrupt by an interrupted
// write (ReadAll skips and counts them, it does not fail the whole read).
//
// # Who writes it
//
// A queue that depends on someone remembering to write to it rots, and the
// first version of this package proved it: every entry existed because a human
// or an agent ran `lacquer console inbox add`, and the evidence that this fails
// was on the machine already (~/Developer/fleet-ops/sessions.jsonl has ten
// entries, all from one day, with nothing since). So the writers now sit in the
// processes that see the events (internal/producers, internal/cirounds):
//
//   - `lacquer wait pr` adds an ACTION when a PR's wait ends timed out,
//     untested or unable to run.
//
//   - `lacquer console` harvests PR merges on read, one UNREAD per merged PR.
//
//   - `lacquer ci-round` adds an ACTION when an agent's CI budget is spent.
//
//   - `lacquer console inbox hook stop` (a Stop hook) adds an UNREAD when a background agent idles.
//
// `console inbox add` remains for a decision raised in conversation. Producers
// use only the two types and the fields below, because the phone mirror reads this file, and they check for an
// existing entry (by ref, and by the head commit in the body) before adding.
package inbox

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Type says which of the two questions an Entry answers.
type Type string

const (
	// Action means a human still needs to decide something. It is not a PR,
	// not a live session, not a fleet finding -- it is a question raised in
	// conversation with nowhere else to land.
	Action Type = "action"
	// Unread means work finished and nobody has acknowledged it -- the
	// subagent-completion case: a result was reported once, in one
	// conversation's scrollback, and is otherwise gone.
	Unread Type = "unread"
)

// Entry is one line in the inbox file.
//
// ResolvedAt is *time.Time, not time.Time, and carries `omitempty`: a zero
// time.Time serializes as "0001-01-01T00:00:00Z", which is a value, not an
// absence, and would make every open entry look like it resolved on day one
// of the Gregorian calendar. A nil pointer is omitted from the JSON line
// entirely while open, and becomes a real RFC3339 timestamp (time.Time's own
// MarshalJSON) the moment Resolve sets it -- which is the "empty while open"
// property the brief for this package asked for, expressed in a way
// encoding/json already gets right without a custom marshaller.
type Entry struct {
	ID         string     `json:"id"`
	Type       Type       `json:"type"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Project    string     `json:"project,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// Open reports whether e is still awaiting resolution.
func (e Entry) Open() bool { return e.ResolvedAt == nil }

// WriteGuard, when set, is asked before every write and can veto it. Only
// internal/inbox/inboxtest sets it, to keep test binaries off the operator's
// real inbox; nothing in a shipped binary does.
var WriteGuard func(path string) error

// Add appends a new entry, assigning it an ID (if the caller left one blank
// -- callers should always leave it blank; the ID is derived, not invented by
// the caller, per the brief this package was built from) and a CreatedAt (if
// zero). It creates the file and any missing parent directory, matching
// internal/console/record.go's AppendRecord.
//
// Concurrency-safe by construction, not by locking: the file is opened
// O_APPEND, and the encoded line is handed to a SINGLE os.File.Write call.
// POSIX guarantees an O_APPEND write is applied atomically to the file's
// current end-of-file, and a single write() syscall of any of these lines
// (well under PIPE_BUF, the 4096-byte guarantee every relevant OS honors) is
// atomic with respect to other writers -- so two processes appending at the
// same instant cannot interleave and produce a torn line, they simply land in
// whichever order the kernel serializes their write()s. This is exactly the
// pattern record.go's AppendRecord already uses for the sessions file; the
// difference here is that this package's own tests exercise it under
// concurrent load (see TestConcurrentAddsProduceNoTornLines), because unlike
// the sessions file -- one dispatcher at a time, by that file's own doc
// comment -- this store is written by multiple sessions and subagents on the
// same machine at once.
func Add(path string, e Entry) (Entry, error) {
	if e.Type != Action && e.Type != Unread {
		return Entry{}, fmt.Errorf("inbox entry has invalid type %q (want %q or %q)", e.Type, Action, Unread)
	}
	if strings.TrimSpace(e.Title) == "" {
		return Entry{}, fmt.Errorf("inbox entry needs a title")
	}
	if e.ID == "" {
		id, err := newID()
		if err != nil {
			return Entry{}, err
		}
		e.ID = id
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}

	if WriteGuard != nil {
		if err := WriteGuard(path); err != nil {
			return Entry{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Entry{}, fmt.Errorf("create inbox file directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, fmt.Errorf("open inbox file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(e)
	if err != nil {
		return Entry{}, fmt.Errorf("encode inbox entry: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return Entry{}, fmt.Errorf("write inbox entry: %w", err)
	}
	return e, nil
}

// newID derives a short id the caller never has to invent. 5 random bytes (10
// hex characters) keeps a birthday collision implausible at the scale this
// store actually runs at -- a single operator's fleet, not a multi-tenant
// system -- while staying short enough to type on a command line
// (`lacquer console inbox resolve <id>`).
func newID() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate inbox entry id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ReadAll loads every entry in the inbox file, in the order they were
// appended.
//
// malformed counts lines that failed to parse -- e.g. a truncated line left
// by an Add that was interrupted mid-write -- WITHOUT failing the read: one
// corrupt line must not sink visibility into every entry recorded before it,
// the same reasoning record.go's ReadRecords already applies to the sessions
// file.
//
// err is returned ONLY when the file itself could not be opened at all --
// missing, or unreadable (permissions, a path pointing into a torn-down
// worktree, whatever). That is a DELIBERATE difference from ReadRecords,
// which treats a missing sessions file as "nothing dispatched yet" and
// returns no error: this package cannot tell "operator configured --inbox but
// never ran `add`" apart from "operator's --inbox path is stale or
// misconfigured" from the file alone, and the console's existing degrade
// contract (console.go: "A MISSING TOOL DEGRADES, IT DOES NOT FAIL") already
// answers this exact ambiguity for `gh` and `claude agents` the same way --
// report it as unavailable rather than silently rendering an empty inbox that
// reads as "nothing to do" when the truth might be "could not check".
// console.Gather is the caller that turns this err into an Unavailable entry;
// see its doc comment there.
func ReadAll(path string) (entries []Entry, malformed int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if jsonErr := json.Unmarshal([]byte(line), &e); jsonErr != nil {
			malformed++
			continue
		}
		entries = append(entries, e)
	}
	if scanErr := sc.Err(); scanErr != nil {
		return entries, malformed, fmt.Errorf("read inbox file: %w", scanErr)
	}
	return entries, malformed, nil
}

// ListOpen returns every entry that has not been resolved, in the order they
// were appended (oldest first) -- the same order the file itself holds them
// in. Ordering by how long a decision has been waiting, not by anything else,
// is the caller's job (console.Gather sorts by CreatedAt for display); this
// stays a plain filter over ReadAll.
func ListOpen(path string) (entries []Entry, malformed int, err error) {
	all, malformed, err := ReadAll(path)
	if err != nil {
		return nil, malformed, err
	}
	for _, e := range all {
		if e.Open() {
			entries = append(entries, e)
		}
	}
	return entries, malformed, nil
}

// Resolve marks the entry with the given id resolved and rewrites the file.
//
// This is a read-modify-rewrite (read every entry, flip one, write a temp
// file, rename over the original) rather than an append -- the same shape
// kill.go's RemoveRecord already uses for the sessions file. It is NOT safe
// against two concurrent Resolve calls racing each other (the second's read
// will not see the first's write, and whichever rename lands last wins,
// silently dropping the other's change) -- unlike Add, which many sessions
// call constantly and unattended, Resolve is a deliberate, low-frequency,
// human-initiated action (an operator running `lacquer console inbox
// resolve <id>` once), so the same protection Add needs would be overhead
// bought for a race this store is not actually exposed to in practice.
func Resolve(path, id string) (Entry, error) {
	entries, _, err := ReadAll(path)
	if err != nil {
		return Entry{}, err
	}
	idx := -1
	for i, e := range entries {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Entry{}, fmt.Errorf("no inbox entry with id %q", id)
	}
	if !entries[idx].Open() {
		return Entry{}, fmt.Errorf("inbox entry %q was already resolved at %s", id, entries[idx].ResolvedAt.Format(time.RFC3339))
	}
	now := time.Now().UTC()
	entries[idx].ResolvedAt = &now

	if err := rewrite(path, entries); err != nil {
		return Entry{}, err
	}
	return entries[idx], nil
}

// rewrite replaces the inbox file's content with entries, one JSON object per
// line, via a temp file + rename so a crash mid-write leaves either the old
// file or the new one intact, never a half-written one.
func rewrite(path string, entries []Entry) error {
	if WriteGuard != nil {
		if err := WriteGuard(path); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write inbox file: %w", err)
	}
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encode inbox entry: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write inbox entry: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Path resolves the inbox file: an explicit --inbox wins, then $LACQUER_INBOX,
// then $XDG_STATE_HOME/lacquer/inbox.jsonl, then ~/.local/state/lacquer/inbox.jsonl.
//
// The last two are the path the phone mirror and the inbox UI already fall back
// to, so a default here changes nothing downstream. isDefault reports that
// neither the flag nor the environment named a file, which matters to readers:
// a default path that does not exist yet means nothing has written to it, while
// an explicit one that does not exist is a misconfiguration.
func Path(flagValue string, getenv func(string) string) (path string, isDefault bool, err error) {
	if flagValue != "" {
		return flagValue, false, nil
	}
	if v := getenv("LACQUER_INBOX"); v != "" {
		return v, false, nil
	}
	// XDG says a relative $XDG_STATE_HOME must be ignored.
	if x := getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "lacquer", "inbox.jsonl"), true, nil
	}
	home := getenv("HOME")
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return "", false, fmt.Errorf("no inbox path: pass --inbox, set LACQUER_INBOX, or set HOME (%w)", err)
		}
	}
	return filepath.Join(home, ".local", "state", "lacquer", "inbox.jsonl"), true, nil
}
