package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default `lacquer console` harvests PR merges with the real gh lookup: the
// first run records a cursor and nothing else, a later one adds the merge, and a
// third adds nothing more. The wiring test for main.go's MergeRun; the logic is
// tested in internal/producers.
func TestConsoleHarvestsMergesEndToEnd(t *testing.T) {
	lq := realLacquer(t)
	bin := t.TempDir()
	merged := filepath.Join(bin, "merged.json")
	script := `#!/bin/sh
case "$*" in
  *"--state merged"*) cat "` + merged + `" 2>/dev/null || echo '[]' ;;
  *) echo '[]' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	proj := t.TempDir()
	roster := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(roster, []byte("[[project]]\nname = \"widgets\"\npath = \""+proj+"\"\nrepo = \"acme/widgets\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": state, "HOME": t.TempDir(), "LACQUER_ROSTER": roster}
	inboxFile := filepath.Join(state, "lacquer", "inbox.jsonl")

	out, errb, code := runConsoleEnv(t, lq, env, nil)
	if code != 0 || !strings.Contains(out, "not backfilled") {
		t.Fatalf("first run: code %d\nout:\n%s\nerr:\n%s", code, out, errb)
	}
	if _, err := os.Stat(filepath.Join(state, "lacquer", "merge-cursor.json")); err != nil {
		t.Fatalf("the first run left no cursor: %v", err)
	}

	if err := os.WriteFile(merged, []byte(`[{"number":5,"title":"ship it","url":"https://github.com/acme/widgets/pull/5","mergedAt":"2999-01-01T00:00:00Z"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, errb, code = runConsoleEnv(t, lq, env, nil)
		if code != 0 || !strings.Contains(out, "acme/widgets#5 merged: ship it") {
			t.Fatalf("run %d: code %d\nout:\n%s\nerr:\n%s", i+2, code, out, errb)
		}
	}
	b, _ := os.ReadFile(inboxFile)
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("two runs saw one merge and made %d entries:\n%s", n, b)
	}
}
