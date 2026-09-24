package console

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundDispatchWithDifferentPathCase(t *testing.T) {
	for _, sub := range []string{"", "Sources"} {
		t.Run(sub, func(t *testing.T) {
			parent := realPath(t, t.TempDir())
			repo := filepath.Join(parent, "Project")
			if err := os.MkdirAll(filepath.Join(repo, "Sources"), 0o755); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "PROJECT")
			if sub != "" {
				dir = filepath.Join(dir, "SOURCES")
			}
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				t.Skip("requires a case-insensitive filesystem")
			} else if err != nil {
				t.Fatal(err)
			}
			initGitRepo(t, repo)
			if err := os.WriteFile(filepath.Join(repo, "Sources", "file.txt"), []byte("source"), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, repo, "add", ".")
			git(t, repo, "commit", "-qm", "source")
			calls := fakeClaude(t)
			launch, err := dispatchCallers[0].run(t, "alpha", dir, "task", Background)
			if err != nil {
				t.Fatalf("dispatch: %v\n%s", err, launch.Output)
			}
			got := claudeCalls(t, calls)
			if len(got) != 1 {
				t.Fatalf("calls = %v", got)
			}
			want := filepath.Join(launch.Record.Worktree, sub)
			if got[0].cwd != want {
				t.Fatalf("cwd = %q, want %q", got[0].cwd, want)
			}
		})
	}
}
