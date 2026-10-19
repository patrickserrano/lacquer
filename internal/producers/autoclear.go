package producers

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// sessionRefPrefix is how AgentIdle refs an entry to the session it is about.
const sessionRefPrefix = "session:"

// autoClearNote is recorded on every entry AutoClear resolves.
const autoClearNote = "session ended: resolved automatically, not by a person"

// AutoClearOptions is what AutoClear needs.
type AutoClearOptions struct {
	InboxPath string
	// Live returns the id of every session `claude agents --json` still lists.
	// An error means the list could not be read; it must not be returned as an
	// empty list. Nil means there is no source, and nothing is resolved.
	Live func() ([]string, error)
	// DryRun lists what would be resolved and writes nothing.
	DryRun bool
}

// AutoClearResult is what AutoClear did, or with DryRun what it would do.
type AutoClearResult struct {
	// Entries are the entries resolved (DryRun: that would be).
	Entries []inbox.Entry
	// Notes say why nothing was done when that is not because nothing was due.
	Notes []string
}

// AutoClear resolves the idle entries of sessions that have ended.
//
// It resolves an entry only if all of these hold: it is open, its type is
// UNREAD (what AgentIdle writes), and its ref is "session:<id>" for an id the
// live list no longer contains. An ACTION is never touched, even one sharing the
// ref of an ended session, because an ACTION is a decision owed to a person and
// the session ending does not answer it. An entry with no session ref is never
// touched whatever its title says.
//
// If the live list cannot be read, nothing is resolved and a note says so.
func AutoClear(o AutoClearOptions) AutoClearResult {
	if o.Live == nil {
		return AutoClearResult{Notes: []string{"idle-entry autoclear: no session source; resolved nothing"}}
	}
	ids, err := o.Live()
	if err != nil {
		return AutoClearResult{Notes: []string{fmt.Sprintf("idle-entry autoclear: session list unavailable (%v); resolved nothing", err)}}
	}
	liveSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		liveSet[id] = true
	}

	open, _, err := inbox.ListOpen(o.InboxPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return AutoClearResult{}
		}
		return AutoClearResult{Notes: []string{fmt.Sprintf("idle-entry autoclear: inbox unreadable (%v); resolved nothing", err)}}
	}
	var res AutoClearResult
	var due []string
	for _, e := range open {
		if e.Type != inbox.Unread {
			continue
		}
		id, ok := strings.CutPrefix(e.Ref, sessionRefPrefix)
		if !ok || id == "" || liveSet[id] {
			continue
		}
		res.Entries = append(res.Entries, e)
		due = append(due, e.ID)
	}
	if o.DryRun || len(due) == 0 {
		return res
	}
	done, err := inbox.ResolveIDs(o.InboxPath, due, autoClearNote)
	res.Entries = done
	if err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("idle-entry autoclear: could not resolve (%v)", err))
	}
	return res
}
