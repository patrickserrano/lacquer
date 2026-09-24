package console

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const neverIndex = ".metadata_never_index"

// prepareWorktree runs before an agent can build. Exclusions are machine-local,
// not project policy: preserve the user's global file and append only our rule.
func prepareWorktree(root string) error {
	if err := excludeSpotlightMarker(root); err != nil {
		return err
	}
	marker := filepath.Join(root, neverIndex)
	// Do not follow a tracked symlink or truncate an existing file.
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		info, statErr := os.Lstat(marker)
		if statErr == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("create Spotlight marker %s: %w", marker, err)
	}
	return f.Close()
}

func excludeSpotlightMarker(root string) error {
	cmd := exec.Command("git", "-C", root, "config", "--global", "--path", "--get", "core.excludesFile")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return fmt.Errorf("read global excludes path: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base = filepath.Join(home, ".config")
		}
		path = filepath.Join(base, "git", "ignore")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("global core.excludesFile must be absolute: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create global excludes directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open global excludes %s: %w", path, err)
	}
	defer f.Close()
	// Different repositories can dispatch concurrently; their worktree locks
	// do not protect this shared file. Closing releases this advisory lock.
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("lock global excludes: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if !strings.Contains("\n"+string(data)+"\n", "\n"+neverIndex+"\n") {
		if _, err := f.WriteString("\n# lacquer worktree Spotlight exclusion\n" + neverIndex + "\n"); err != nil {
			return fmt.Errorf("write global excludes: %w", err)
		}
	}
	// Repository overrides/negations can defeat a global rule. Refuse to
	// launch rather than leave an untracked marker for the agent to commit.
	if _, err := gitOutput(root, "check-ignore", "-q", neverIndex); err != nil {
		return fmt.Errorf("%s is not ignored in %s; check core.excludesFile and repository ignore overrides: %w", neverIndex, root, err)
	}
	return nil
}
