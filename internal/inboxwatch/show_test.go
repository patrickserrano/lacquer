package inboxwatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

func showFixture(t *testing.T, entries ...inbox.Entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	for _, e := range entries {
		if _, err := inbox.Add(path, e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// What `--show` prints is foxy-inbox's detail_text: a header, the title, the
// project/createdAt/ref fields right-aligned to nine columns, then the body
// wrapped to 98.
func TestShowPrintsFoxyInboxDetailText(t *testing.T) {
	long := strings.Repeat("word ", 40) // 200 columns: wraps at 98
	path := showFixture(t, inbox.Entry{ID: "fx01", Type: inbox.Action, Title: "Decide the thing", Project: "lacquer", Ref: "https://github.com/o/r/pull/5",
		CreatedAt: time.Date(2026, 9, 24, 12, 30, 0, 0, time.UTC), Body: "first paragraph\n\n" + long + "\nlast"})
	got, err := Show(path, "fx01")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := wrap(long, 98)
	if len(wrapped) != 3 {
		t.Fatalf("fixture wraps to %d lines", len(wrapped))
	}
	want := strings.Join(append([]string{
		"ACTION  fx01",
		"Decide the thing",
		"",
		"  project: lacquer",
		"createdAt: 2026-09-24T12:30:00Z",
		"      ref: https://github.com/o/r/pull/5",
		"",
		"first paragraph",
		"",
	}, append(wrapped, "last")...), "\n") + "\n"
	if got != want {
		t.Errorf("Show =\n%s\nwant\n%s", got, want)
	}
	for i, l := range strings.Split(got, "\n") {
		if len([]rune(l)) > 98 && !strings.HasPrefix(l, "createdAt") && !strings.HasPrefix(l, "      ref") {
			t.Errorf("line %d is %d wide", i, len([]rune(l)))
		}
	}
}

// An entry with no project or ref shows neither field, and the latest record for
// an id is the one shown, resolved or not.
func TestShowSkipsAbsentFieldsAndUsesTheLatestRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	lines := `{"id":"a1","type":"unread","title":"old title","createdAt":"2026-09-24T12:30:00Z"}` + "\n" +
		`{"id":"b2","type":"action","title":"other","createdAt":"2026-09-24T12:30:00Z"}` + "\n" +
		`{"id":"a1","type":"unread","title":"new title","createdAt":"2026-09-24T12:30:00Z","resolvedAt":"2026-09-24T13:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Show(path, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "UNREAD  a1\nnew title\n\ncreatedAt: 2026-09-24T12:30:00Z\n\n\n"; got != want {
		t.Errorf("Show = %q, want %q", got, want)
	}
}

func TestShowSaysWhenThereIsNoSuchEntryOrNoInbox(t *testing.T) {
	path := showFixture(t, inbox.Entry{ID: "fx01", Type: inbox.Unread, Title: "x"})
	if out, err := Show(path, "nope"); err == nil || !strings.Contains(err.Error(), "no inbox entry nope") || out != "" {
		t.Errorf("a missing id: %q, %v", out, err)
	}
	if _, err := Show(filepath.Join(t.TempDir(), "absent.jsonl"), "x"); err == nil {
		t.Error("a missing inbox file printed nothing and reported nothing")
	}
}

// The text goes to a terminal, and agents write every field of it.
func TestShowSanitizesControlCharacters(t *testing.T) {
	path := showFixture(t, inbox.Entry{ID: "fx\x1b01", Type: inbox.Action, Title: "ti\x1b[2Jtle", Project: "pr\x07oj", Ref: "re\x1bf",
		Body: "body \x1b]52;c;ZXZpbA==\x07\nline\x00two"})
	got, err := Show(path, "fx\x1b01")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Errorf("a control character is in the output: %q", got)
	}
	for _, want := range []string{"fx^[01", "ti^[[2Jtle", "pr^Goj", "re^[f", "body ^[]52;c;ZXZpbA==^G", "line^@two"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if _, err := Show(path, "no\x1b[2Jpe"); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("the error carries the raw id: %v", err)
	}
}

func TestShowHandlesCRLFBodies(t *testing.T) {
	path := showFixture(t, inbox.Entry{ID: "fx01", Type: inbox.Action, Title: "t", Body: "line one\r\nline two\rline three"})
	got, err := Show(path, "fx01")
	if err != nil || strings.Contains(got, "^M") || !strings.Contains(got, "\nline one\nline two\nline three\n") {
		t.Errorf("Show = %q, %v", got, err)
	}
}
