package ciwait

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// GH is the Runner that shells out to the real gh.
func GH(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, ErrGHNotFound
	}
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- fixed subcommand; args are a PR number, a slug and literals
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args[:2], " "), msg)
		}
		return nil, fmt.Errorf("gh: %s", msg)
	}
	return stdout.Bytes(), nil
}
