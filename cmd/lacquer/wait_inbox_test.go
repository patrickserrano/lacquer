package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

const prURL = "https://github.com/acme/widgets/pull/425"

// withURL is rollup() as gh renders it when the PR's url is asked for too.
func withURL(state string, nodes ...string) string {
	return fmt.Sprintf(`{"state":%q,"headRefOid":"0123456789abcdef","url":%q,"statusCheckRollup":[%s]}`, state, prURL, strings.Join(nodes, ","))
}

// waitInbox runs `wait pr` against inboxFile and returns the exit code, stderr
// and the entries the inbox holds afterwards (nil when the file does not exist).
func waitInbox(t *testing.T, inboxFile string, args ...string) (int, string, []inbox.Entry) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"wait", "pr"}, args...), func(k string) string {
		if k == "LACQUER_INBOX" {
			return inboxFile
		}
		return ""
	}, &out, &errb)
	entries, _, _ := inbox.ReadAll(inboxFile)
	return code, errb.String(), entries
}

// Each way a wait can end, through the real command and a real `gh` lookup:
// which ones put something in front of a human, and which type it is.
func TestWaitPRInboxPerOutcome(t *testing.T) {
	cases := []struct {
		name  string
		gh    string
		code  int
		title string // "" means the inbox must stay empty
	}{
		{"passed writes nothing", withURL("OPEN", checkRun("test", "COMPLETED", "SUCCESS")), 0, ""},
		{"a failed check writes nothing (decided: the author is already on it)", withURL("OPEN", checkRun("test", "COMPLETED", "FAILURE")), 1, ""},
		{"timed out is an ACTION", withURL("OPEN", checkRun("test", "IN_PROGRESS", "")), 2, "acme/widgets#425: CI timed out"},
		{"no checks is an ACTION", withURL("OPEN"), 3, "acme/widgets#425: no checks ran"},
		{"gh failing is an ACTION that names the reason", "ERR:HTTP 502 Bad Gateway", 4, "acme/widgets#425: wait pr could not run"},
		{"a merged PR is nothing to act on", withURL("MERGED"), 4, ""},
		{"a closed PR is nothing to act on", withURL("CLOSED"), 4, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, tc.gh)
			file := filepath.Join(t.TempDir(), "inbox.jsonl")
			code, errb, got := waitInbox(t, file, append([]string{"425", "--repo", "acme/widgets"}, fast...)...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstderr: %s", code, tc.code, errb)
			}
			if tc.title == "" {
				if len(got) != 0 {
					t.Fatalf("exit %d must write nothing, wrote %+v", code, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want exactly one entry, got %+v (stderr %s)", got, errb)
			}
			e := got[0]
			if e.Type != inbox.Action || !strings.Contains(e.Title, tc.title) || e.Ref != prURL || e.Project != "acme/widgets" {
				t.Errorf("entry = %+v, want an action titled %q with ref %s", e, tc.title, prURL)
			}
			if code != 4 && !strings.Contains(e.Body, "0123456789abcdef") {
				t.Errorf("the body must carry the head sha (it is the dedupe key): %q", e.Body)
			}
			if code == 4 && !strings.Contains(e.Body, "HTTP 502") {
				t.Errorf("an exit 4 entry must name the reason: %q", e.Body)
			}
		})
	}
}

// Waiting twice on the same unchanged PR is one entry, not two; a new head
// commit is a new problem and gets its own.
func TestWaitPRInboxIsIdempotentPerHead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	args := append([]string{"425", "--repo", "acme/widgets"}, fast...)

	fakeGH(t, withURL("OPEN", checkRun("test", "IN_PROGRESS", "")))
	waitInbox(t, file, args...)
	_, _, got := waitInbox(t, file, args...)
	if len(got) != 1 {
		t.Fatalf("the same timeout twice made %d entries, want 1: %+v", len(got), got)
	}

	fakeGH(t, strings.Replace(withURL("OPEN", checkRun("test", "IN_PROGRESS", "")), "0123456789abcdef", "fedcba9876543210", 1))
	_, _, got = waitInbox(t, file, args...)
	if len(got) != 2 {
		t.Fatalf("a new head commit must be a new entry, got %d: %+v", len(got), got)
	}
}

// A resolved entry no longer counts: the same problem coming back is news.
func TestWaitPRInboxReRaisesAfterResolve(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	args := append([]string{"425", "--repo", "acme/widgets"}, fast...)
	fakeGH(t, withURL("OPEN"))
	_, _, got := waitInbox(t, file, args...)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if _, err := inbox.Resolve(file, got[0].ID); err != nil {
		t.Fatal(err)
	}
	_, _, got = waitInbox(t, file, args...)
	if len(got) != 2 {
		t.Fatalf("after resolving, the same outcome must be raised again; got %d entries", len(got))
	}
}

// An inbox that cannot be written must never change the answer: exit 2 stays 2,
// and the failure is loud on stderr.
func TestWaitPRInboxWriteFailureKeepsTheExitCode(t *testing.T) {
	fakeGH(t, withURL("OPEN", checkRun("test", "IN_PROGRESS", "")))
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The inbox's parent is a regular file, so neither reading nor creating works.
	code, errb, _ := waitInbox(t, filepath.Join(blocker, "inbox.jsonl"), append([]string{"425", "--repo", "acme/widgets"}, fast...)...)
	if code != 2 {
		t.Fatalf("exit %d, want 2 (an inbox failure must not change it)", code)
	}
	if !strings.Contains(errb, "WARNING") || !strings.Contains(errb, "NOT written") {
		t.Errorf("a failed write must be loud on stderr:\n%s", errb)
	}
}

func TestWaitPRNoInboxWritesNothing(t *testing.T) {
	fakeGH(t, withURL("OPEN", checkRun("test", "IN_PROGRESS", "")))
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	code, _, _ := waitInbox(t, file, append([]string{"425", "--repo", "acme/widgets", "--no-inbox"}, fast...)...)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("--no-inbox created the inbox file")
	}
}

// With no --repo, gh infers it from the checkout; the entry still names the
// repository, taken from the PR's own URL.
func TestWaitPRInboxNamesTheRepoFromTheURL(t *testing.T) {
	fakeGH(t, withURL("OPEN"))
	file := filepath.Join(t.TempDir(), "inbox.jsonl")
	_, _, got := waitInbox(t, file, append([]string{"425"}, fast...)...)
	if len(got) != 1 || !strings.HasPrefix(got[0].Title, "acme/widgets#425:") {
		t.Fatalf("got %+v", got)
	}
}
