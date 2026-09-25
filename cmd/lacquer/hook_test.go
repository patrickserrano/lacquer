package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/console"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

type stubSessions struct {
	list []console.Session
	err  error
}

func (s stubSessions) List(context.Context) ([]console.Session, error) { return s.list, s.err }

const hookSID = "e8bfa25b-3acb-41b2-a2eb-7cba69e5454e"

func stopPayload(cwd, msg string) string {
	return `{"session_id":"` + hookSID + `","cwd":"` + cwd + `","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"` + msg + `"}`
}

// hookRun runs the hook with stdin as the payload and a background-session env
// unless jobDir is "".
func hookRun(t *testing.T, stdin, jobDir, inboxFile string, sessions console.SessionSource, extra ...string) (int, string) {
	t.Helper()
	old := hookStdin
	hookStdin = strings.NewReader(stdin)
	t.Cleanup(func() { hookStdin = old })
	var errb bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "CLAUDE_JOB_DIR":
			return jobDir
		case "LACQUER_INBOX":
			return inboxFile
		}
		return ""
	}
	code := runInboxHookStop(extra, getenv, sessions, &errb)
	return code, errb.String()
}

func TestHookStopNoJobDirWritesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	code, errb := hookRun(t, stopPayload("/w/p", "done"), "", file, stubSessions{})
	if code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("an interactive session wrote to the inbox")
	}
}

func TestHookStopBackgroundSessionWritesOneEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "inbox.jsonl")
	roster := filepath.Join(dir, "fleet.toml")
	os.WriteFile(roster, []byte("[[project]]\nname = \"widgets\"\npath = \""+dir+"/widgets\"\n"), 0o644)
	sessions := stubSessions{list: []console.Session{{SessionID: "other", Name: "nope"}, {SessionID: hookSID, Name: "ic-widgets"}}}
	cwd := dir + "/widgets/.claude/worktrees/x"

	code, errb := hookRun(t, stopPayload(cwd, "Opened PR #5.\\nMore."), "/j", file, sessions, "--roster", roster)
	if code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	entries, _, err := inbox.ReadAll(file)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	e := entries[0]
	if e.Title != "ic-widgets is idle in widgets: Opened PR #5." || e.Ref != "session:"+hookSID || e.Project != "widgets" || e.Type != inbox.Unread {
		t.Fatalf("entry %+v", e)
	}

	// The second idle, while the first is open.
	hookRun(t, stopPayload(cwd, "again"), "/j", file, sessions, "--roster", roster)
	if entries, _, _ = inbox.ReadAll(file); len(entries) != 1 {
		t.Fatalf("%d entries after a second idle, want 1", len(entries))
	}
}

func TestHookStopBadInputExitsZeroWithWarning(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	code, errb := hookRun(t, "this is not json", "/j", file, stubSessions{})
	if code != 0 || !strings.Contains(errb, "not JSON") {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("bad input wrote an entry")
	}
}

func TestHookStopUnwritableInboxExitsZeroWithWarning(t *testing.T) {
	code, errb := hookRun(t, stopPayload("/w/p", "done"), "/j", t.TempDir(), stubSessions{})
	if code != 0 || !strings.Contains(errb, "lacquer inbox hook stop:") {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
}

func TestHookStopSessionLookupFailureStillWritesAndWarns(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	code, errb := hookRun(t, stopPayload("/w/p", "done"), "/j", file, stubSessions{err: errors.New("claude agents timed out")})
	if code != 0 || !strings.Contains(errb, "timed out") {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	entries, _, _ := inbox.ReadAll(file)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Title, "e8bfa25b is idle in p:") {
		t.Fatalf("entries %+v", entries)
	}
}

func TestHookStopStopHookActiveWritesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	p := strings.Replace(stopPayload("/w/p", "done"), `"stop_hook_active":false`, `"stop_hook_active":true`, 1)
	if code, errb := hookRun(t, p, "/j", file, stubSessions{}); code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("wrote while stop_hook_active")
	}
}

// Through run(): `console inbox hook stop` must be routed before the
// lacquer-root checks (LACQUER_ROOT is unset here, which fails every other
// console subcommand) and must exit 0 whatever happens.
func TestHookStopRoutedThroughRunIgnoresRootChecks(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho '[{\"sessionId\":\""+hookSID+"\",\"name\":\"ic-routed\"}]'\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	old := hookStdin
	hookStdin = strings.NewReader(stopPayload("/w/p", "done"))
	t.Cleanup(func() { hookStdin = old })
	file := filepath.Join(t.TempDir(), "inbox.jsonl")

	var out, errb bytes.Buffer
	code := run([]string{"console", "inbox", "hook", "stop"}, func(k string) string {
		switch k {
		case "CLAUDE_JOB_DIR":
			return "/j"
		case "LACQUER_INBOX":
			return file
		}
		return ""
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	entries, _, _ := inbox.ReadAll(file)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Title, "ic-routed is idle in p:") {
		t.Fatalf("entries %+v (stderr %q)", entries, errb.String())
	}
}
