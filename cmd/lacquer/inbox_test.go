package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runConsole is a small helper around run() for `console` invocations that
// need a valid LACQUER_ROOT (requireLacquerRoot gates the whole `console`
// case) but nothing else -- inbox subcommands need neither a roster nor a
// roles file.
func runConsole(t *testing.T, lq string, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	env := envMap(map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()})
	code = run(append([]string{"console"}, args...), env, &out, &errb)
	return out.String(), errb.String(), code
}

// End to end: add prints exactly the new id, list shows it as open, resolve
// closes it, and list without --all then hides it while --all still shows it.
// This is the wiring test for main.go's "inbox" case, not a retest of
// internal/inbox's own package tests.
func TestConsoleInboxAddListResolveRoundTrips(t *testing.T) {
	lq := realLacquer(t)
	inboxPath := filepath.Join(t.TempDir(), "inbox.jsonl")

	out, errb, code := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "add",
		"--type", "action", "--title", "ship the widget-fleet eval as blocking?", "--ref", "#374", "--project", "glowroot-widget-co"})
	if code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errb)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatalf("add printed no id; stdout=%q stderr=%q", out, errb)
	}
	// "Must be usable non-interactively by an agent in one shell line" --
	// stdout on success is the id and nothing else, so a script can capture
	// it directly without any stripping.
	if strings.Contains(out, "\n"+id) || strings.Count(out, "\n") > 1 {
		t.Errorf("add's stdout must be exactly the id, got %q", out)
	}

	out, errb, code = runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "list"})
	if code != 0 {
		t.Fatalf("list failed (code %d): %s", code, errb)
	}
	if !strings.Contains(out, id) || !strings.Contains(out, "ship the widget-fleet eval as blocking?") {
		t.Fatalf("list did not show the open entry:\n%s", out)
	}

	out, errb, code = runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "resolve", id})
	if code != 0 {
		t.Fatalf("resolve failed (code %d): %s", code, errb)
	}
	if !strings.Contains(out, id) {
		t.Errorf("resolve's confirmation is missing the id:\n%s", out)
	}

	out, _, code = runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "list"})
	if code != 0 {
		t.Fatalf("list after resolve failed: %s", out)
	}
	if strings.Contains(out, id) {
		t.Fatalf("a resolved entry must not appear in the default (open-only) list:\n%s", out)
	}

	out, _, code = runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "list", "--all"})
	if code != 0 {
		t.Fatalf("list --all failed: %s", out)
	}
	if !strings.Contains(out, id) {
		t.Fatalf("list --all must still show a resolved entry:\n%s", out)
	}
}

func TestConsoleInboxAddRejectsMissingType(t *testing.T) {
	lq := realLacquer(t)
	inboxPath := filepath.Join(t.TempDir(), "inbox.jsonl")
	_, errb, code := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "add", "--title", "x"})
	if code == 0 {
		t.Fatal("expected rejection of a missing --type")
	}
	if !strings.Contains(errb, "type") {
		t.Errorf("error should mention the missing --type, got: %s", errb)
	}
}

func TestConsoleInboxResolveRejectsUnknownID(t *testing.T) {
	lq := realLacquer(t)
	inboxPath := filepath.Join(t.TempDir(), "inbox.jsonl")
	if _, _, code := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "add", "--type", "unread", "--title", "x"}); code != 0 {
		t.Fatal("setup add failed")
	}
	if _, _, code := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "resolve", "not-a-real-id"}); code == 0 {
		t.Fatal("expected rejection of an unknown id")
	}
}

// runConsoleEnv is runConsole with extra environment, for the default-path
// tests: HOME and XDG_STATE_HOME always point into a temp dir so nothing here
// can touch the real inbox.
func runConsoleEnv(t *testing.T, lq string, extra map[string]string, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	m := map[string]string{"LACQUER_ROOT": lq}
	for k, v := range extra {
		m[k] = v
	}
	code = run(append([]string{"console"}, args...), envMap(m), &out, &errb)
	return out.String(), errb.String(), code
}

// With no --inbox and no $LACQUER_INBOX the inbox lives under $XDG_STATE_HOME,
// is created on the first add, and is the file list then reads.
func TestConsoleInboxDefaultsToTheStateDirAndCreatesItOnFirstWrite(t *testing.T) {
	lq := realLacquer(t)
	state := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": state, "HOME": t.TempDir()}
	want := filepath.Join(state, "lacquer", "inbox.jsonl")

	out, errb, code := runConsoleEnv(t, lq, env, []string{"inbox", "list"})
	if code != 0 || !strings.Contains(out, "no inbox entries") {
		t.Fatalf("list on a fresh machine: code %d out %q err %q", code, out, errb)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("list must not create the file")
	}

	out, errb, code = runConsoleEnv(t, lq, env, []string{"inbox", "add", "--type", "action", "--title", "first"})
	if code != 0 {
		t.Fatalf("add failed: %s", errb)
	}
	id := strings.TrimSpace(out)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("add did not create %s: %v", want, err)
	}
	out, _, _ = runConsoleEnv(t, lq, env, []string{"inbox", "list"})
	if !strings.Contains(out, id) {
		t.Errorf("list did not read the default file:\n%s", out)
	}
}

func TestConsoleInboxHomeFallbackWhenNoXDG(t *testing.T) {
	lq := realLacquer(t)
	home := t.TempDir()
	if _, errb, code := runConsoleEnv(t, lq, map[string]string{"HOME": home}, []string{"inbox", "add", "--type", "unread", "--title", "x"}); code != 0 {
		t.Fatal(errb)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "lacquer", "inbox.jsonl")); err != nil {
		t.Fatalf("expected ~/.local/state/lacquer/inbox.jsonl: %v", err)
	}
}

// Precedence at the CLI: --inbox beats $LACQUER_INBOX beats the default.
func TestConsoleInboxFlagBeatsEnvBeatsDefault(t *testing.T) {
	lq := realLacquer(t)
	dir := t.TempDir()
	flagP, envP, state := filepath.Join(dir, "flag.jsonl"), filepath.Join(dir, "env.jsonl"), filepath.Join(dir, "state")
	env := map[string]string{"LACQUER_INBOX": envP, "XDG_STATE_HOME": state}
	if _, e, c := runConsoleEnv(t, lq, env, []string{"--inbox", flagP, "inbox", "add", "--type", "action", "--title", "a"}); c != 0 {
		t.Fatal(e)
	}
	if _, e, c := runConsoleEnv(t, lq, env, []string{"inbox", "add", "--type", "action", "--title", "b"}); c != 0 {
		t.Fatal(e)
	}
	for _, p := range []string{flagP, envP} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not written: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "lacquer")); err == nil {
		t.Error("the default path was used although a flag and an env var were set")
	}
}

// The console dashboard itself must render the ACTION section for an entry
// added through the CLI, ahead of the "roster is empty" line -- proving the
// --inbox flag on plain `console` (not just `console inbox ...`) is wired to
// Gather, not just parsed and discarded.
func TestPlainConsoleRendersActionsFromTheInboxFlag(t *testing.T) {
	lq := realLacquer(t)
	inboxPath := filepath.Join(t.TempDir(), "inbox.jsonl")
	if _, errb, code := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "add",
		"--type", "action", "--title", "decide the widget-fleet eval gate"}); code != 0 {
		t.Fatalf("setup add failed: %s", errb)
	}

	// `console` (no subcommand) requires --roster, but this test is only
	// exercising the inbox wiring, not the fleet side -- one project entry
	// pointing at an empty temp dir is enough for LoadRoster to accept it.
	projectDir := t.TempDir()
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	rosterBody := fmt.Sprintf("[[project]]\nname = \"glowroot-widget-co\"\npath = %q\n", projectDir)
	if err := os.WriteFile(rosterPath, []byte(rosterBody), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errb, code := runConsole(t, lq, []string{"--roster", rosterPath, "--inbox", inboxPath})
	if code != 0 {
		t.Fatalf("plain console failed (code %d): %s", code, errb)
	}
	if !strings.Contains(out, "ACTION") || !strings.Contains(out, "decide the widget-fleet eval gate") {
		t.Fatalf("the console dashboard did not render the ACTION entry from --inbox:\n%s", out)
	}
}
