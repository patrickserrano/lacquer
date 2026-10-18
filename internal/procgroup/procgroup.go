// Package procgroup runs external commands under a time limit and reaps their
// whole process tree on expiry.
//
// A bare exec.Command has neither. The hang class this exists for: a launcher
// (`claude`, `npx`, `gh`, `xcodebuild`) blocks at 0% CPU on a prompt, a lock or
// the network, and the audit or sync that called it never returns, with no
// output and no error. Killing only the direct child is not enough either, since
// the launcher's helpers keep running and keep the output pipes open.
package procgroup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ReapWait is how long Wait lets a killed command's pipes drain before it gives
// up on them. A grandchild that outlives the group kill (it changed its own
// process group) would otherwise hold stdout open and keep Wait blocked.
const ReapWait = 2 * time.Second

// Bounded is an exec.Cmd that is killed, with its whole process group, once its
// limit passes. Set Dir, Env, Stdin, Stdout and Stderr on the embedded Cmd as
// usual, then finish with Run, Output or CombinedOutput. Those three shadow the
// exec.Cmd methods of the same name and are the only ways to run it: Start and
// Wait would skip the timeout translation.
type Bounded struct {
	*exec.Cmd
	limit time.Duration
	desc  string
	ctx   context.Context
	stop  context.CancelFunc
}

// New returns a command that runs in its own process group and is killed, group
// and all, after limit. The clock starts when New is called, so build the command
// immediately before running it.
func New(limit time.Duration, name string, args ...string) *Bounded {
	ctx, stop := context.WithTimeout(context.Background(), limit)
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- callers pass fixed program names
	Own(cmd)
	return &Bounded{Cmd: cmd, limit: limit, desc: describe(name, args), ctx: ctx, stop: stop}
}

// Own starts cmd in its own process group and makes a context expiry kill the
// whole group, not just the direct child. cmd must have been built with
// exec.CommandContext, or nothing ever triggers the kill.
func Own(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// A negative pid signals the group; with Setpgid the group id is the child's pid.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = ReapWait
}

// TimeoutError is returned when a command is killed for running past its limit.
// It is never a success: callers that treat a failed command as "could not
// verify" must treat this the same way.
type TimeoutError struct {
	Command string        // the command line, program and arguments
	Limit   time.Duration // the limit that was exceeded
	Err     error         // what the killed process reported
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("`%s` did not finish within %s and was killed", e.Command, e.Limit)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// IsTimeout reports whether err came from a command that ran past its limit.
func IsTimeout(err error) bool {
	var te *TimeoutError
	return errors.As(err, &te)
}

// Run is exec.Cmd.Run under the limit.
func (b *Bounded) Run() error {
	defer b.stop()
	return b.wrap(b.Cmd.Run())
}

// Output is exec.Cmd.Output under the limit.
func (b *Bounded) Output() ([]byte, error) {
	defer b.stop()
	out, err := b.Cmd.Output()
	return out, b.wrap(err)
}

// CombinedOutput is exec.Cmd.CombinedOutput under the limit.
func (b *Bounded) CombinedOutput() ([]byte, error) {
	defer b.stop()
	out, err := b.Cmd.CombinedOutput()
	return out, b.wrap(err)
}

// wrap turns any failure after the deadline into a TimeoutError. A command that
// finished with an error before the deadline keeps its own error.
func (b *Bounded) wrap(err error) error {
	if err != nil && errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
		return &TimeoutError{Command: b.desc, Limit: b.limit, Err: err}
	}
	return err
}

// describe renders a command line for an error, naming the program by its base
// name (a resolved absolute path is noise), capped so a long argument list
// (a path-per-file git call) cannot bury the message.
func describe(name string, args []string) string {
	s := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	const max = 120
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}
