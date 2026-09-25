package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SessionSource lists the live Claude Code sessions on this machine. It is an
// interface so tests inject fixtures and never shell out to the real claude.
type SessionSource interface {
	List(ctx context.Context) ([]Session, error)
}

// sessionTimeout bounds `claude agents --json`. A hung call must surface as
// "sessions: unavailable", not as a console that never prints.
const sessionTimeout = 10 * time.Second

// ClaudeAgents is the real SessionSource: `claude agents --json`, which
// reports every active session on the machine, interactive and background.
type ClaudeAgents struct {
	Timeout time.Duration
	// Run executes claude with args and returns stdout; nil means exec.
	Run func(ctx context.Context, args ...string) ([]byte, error)
}

func (c ClaudeAgents) List(ctx context.Context) ([]Session, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = sessionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := c.Run
	if run == nil {
		run = execClaude
	}
	out, err := run(ctx, "agents", "--json")
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("`claude agents --json` did not answer within %s", timeout)
		}
		return nil, err
	}
	return ParseSessions(out)
}

func execClaude(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// ParseSessions decodes `claude agents --json` output. Output that is not a
// JSON array is an error, never an empty list: unreadable must not render as
// "nothing running".
func ParseSessions(out []byte) ([]Session, error) {
	var s []Session
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	return normalize(s), nil
}

// Age is how long the session has been running as of now, or 0 when the
// session reports no start time.
func (s Session) Age(now time.Time) time.Duration {
	if s.StartedAt <= 0 {
		return 0
	}
	if d := now.Sub(time.UnixMilli(s.StartedAt)); d > 0 {
		return d
	}
	return 0
}

// Busy reports whether the session is doing work right now. claude reports
// "busy"; "working" is the older spelling the dashboard used to count.
func (s Session) Busy() bool { return s.Status == "busy" || s.Status == "working" }

func formatAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "?"
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
