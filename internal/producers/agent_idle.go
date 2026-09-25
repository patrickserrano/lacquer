package producers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

const (
	// titleMessageMax bounds the slice of the agent's last message that goes in
	// the title, in runes: the title is one line on a phone.
	titleMessageMax = 100
	// bodyMessageMax bounds the message copied into the body, in bytes.
	bodyMessageMax = 2048
)

// StopHook is the part of Claude Code's Stop hook payload AgentIdle reads.
//
// Verified against a real `claude --bg` session (claude 2.1.282), not the docs:
// on an ordinary turn end stop_hook_active is FALSE and last_assistant_message
// is present. stop_hook_active is true only when a Stop hook already forced the
// turn to carry on, which is the case to skip, or a hook that blocks would loop.
type StopHook struct {
	SessionID            string `json:"session_id"`
	CWD                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// ParseStopHook decodes the Stop hook's stdin.
func ParseStopHook(raw []byte) (StopHook, error) {
	var h StopHook
	if err := json.Unmarshal(raw, &h); err != nil {
		return StopHook{}, fmt.Errorf("stop hook input is not JSON: %w", err)
	}
	return h, nil
}

// AgentIdleOptions is what AgentIdle needs beyond the hook payload. Name and
// Project are injected because the real ones shell out to `claude agents --json`
// and read the roster, and internal/console (which owns both) imports this
// package.
type AgentIdleOptions struct {
	InboxPath string
	// JobDir is $CLAUDE_JOB_DIR: set in a background session, never in an
	// interactive one. Empty means the operator is at the keyboard, and nothing
	// is written.
	JobDir string
	// Name returns the session's name; an error or "" falls back to the short id.
	Name func(sessionID string) (string, error)
	// Project maps a cwd to a roster project; "" falls back to the cwd's base name.
	Project func(cwd string) string
}

// AgentIdle records, for one Stop hook firing in a background session, that the
// agent has gone idle. It adds one UNREAD entry, ref "session:<id>", and returns
// added=false when there was nothing to add:
//
//	not a background session   the operator is watching this one
//	stop_hook_active           a Stop hook already continued this turn
//	no session id              no ref to dedupe on, so no entry
//	no last message            nothing to title the entry with
//	an open entry for the id   at most one open entry per session; once the
//	                           operator resolves it, the next idle adds a new one
//
// An error means the inbox could not be read or written. A failed name lookup is
// not an error: the entry is worth more with the short id than not at all.
func AgentIdle(o AgentIdleOptions, in StopHook) (entry inbox.Entry, added bool, warn string, err error) {
	if o.JobDir == "" || in.StopHookActive || in.SessionID == "" {
		return inbox.Entry{}, false, "", nil
	}
	if in.HookEventName != "" && in.HookEventName != "Stop" {
		return inbox.Entry{}, false, "", nil
	}
	first := firstLine(in.LastAssistantMessage)
	if first == "" {
		return inbox.Entry{}, false, "", nil
	}

	ref := "session:" + in.SessionID
	open, _, err := inbox.ListOpen(o.InboxPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return inbox.Entry{}, false, "", err
	}
	for _, e := range open {
		if e.Ref == ref {
			return inbox.Entry{}, false, "", nil
		}
	}

	name := ""
	if o.Name != nil {
		var nerr error
		if name, nerr = o.Name(in.SessionID); nerr != nil {
			warn = fmt.Sprintf("session name unavailable, using the id: %v", nerr)
		}
	}
	if name == "" {
		name = shortID(in.SessionID)
	}
	project := ""
	if o.Project != nil {
		project = o.Project(in.CWD)
	}
	if project == "" && in.CWD != "" {
		project = filepath.Base(in.CWD)
	}
	if project == "" {
		project = "unknown project"
	}

	body := truncateBytes(strings.TrimSpace(in.LastAssistantMessage), bodyMessageMax)
	if in.CWD != "" {
		body += "\ncwd: " + in.CWD
	}
	e, err := inbox.Add(o.InboxPath, inbox.Entry{
		Type:    inbox.Unread,
		Title:   fmt.Sprintf("%s is idle in %s: %s", name, project, truncateRunes(first, titleMessageMax)),
		Body:    body,
		Ref:     ref,
		Project: project,
	})
	if err != nil {
		return inbox.Entry{}, false, warn, err
	}
	return e, true, warn, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// truncateBytes cuts on a rune boundary so a multi-byte character is never split.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
