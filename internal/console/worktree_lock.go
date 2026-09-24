package console

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockWorktrees serializes Git's worktree metadata mutations across processes
// and linked checkouts. Concurrent adds can read another add's incomplete
// commondir file (#437), even when every branch and directory name is unique.
// Use the common directory, not a linked checkout's private --git-dir.
func lockWorktrees(dir string) (*os.File, error) {
	common, err := gitOutput(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	f, err := os.OpenFile(filepath.Join(common, "lacquer-worktrees.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open worktree lock: %w", err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock worktrees: %w", err)
	}
	// Closing releases the OS lock, including after a crash. Keep the file:
	// unlinking it would let a new opener bypass waiters on the old inode.
	return f, nil
}
