package plugindefs

import (
	"os/exec"
	"syscall"
	"time"
)

// reapWait is how long Wait lets a killed command's pipes drain before it gives
// up on them. A grandchild that outlives the group kill (it changed its own
// process group) would otherwise hold stdout open and keep Wait blocked.
const reapWait = 2 * time.Second

// ownGroup starts cmd in its own process group and makes a context expiry kill
// the whole group, not just the direct child. `claude` is a launcher that can
// spawn helpers; killing only the parent leaves them running, blocked, at 0% CPU.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// A negative pid signals the group; with Setpgid the group id is the child's pid.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = reapWait
}
