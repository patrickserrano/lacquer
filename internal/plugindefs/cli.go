package plugindefs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The manifest the wrapper needs. `claude plugin validate <dir>` only reads
// components when it finds a manifest; pointed at a bare directory it prints
// "Validation passed" without reading anything. The author is there because its
// absence is a warning, and --strict turns every warning into a failure.
const wrapperManifest = `{"name":"lacquer-validate","version":"0.0.0","description":"temporary wrapper for validating definitions","author":{"name":"lacquer"}}` + "\n"

// BuildWrapper copies the definitions under root into dst as a minimal plugin
// the CLI will actually read, and returns wrapper-path -> original-path so a
// finding in the wrapper can be reported against the real file.
//
// Copies, never symlinks: the CLI does not follow symlinks, and rendered trees
// use them. Only the definition files are copied (a skill's SKILL.md, not its
// references). It errors on a root with no definitions, because the CLI passes
// an empty plugin.
func BuildWrapper(root, dst string) (map[string]string, error) {
	files := Files(root)
	if len(files) == 0 {
		return nil, fmt.Errorf("no agents, skills or commands under %s", root)
	}
	if err := os.MkdirAll(filepath.Join(dst, ".claude-plugin"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dst, ".claude-plugin", "plugin.json"), []byte(wrapperManifest), 0o644); err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, f := range files {
		rel, err := filepath.Rel(root, f.Path)
		if err != nil {
			return nil, err
		}
		out := filepath.Join(dst, rel)
		b, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return nil, err
		}
		m[out] = f.Path
	}
	return m, nil
}

// CLIIssue is one problem the CLI reported for one file.
type CLIIssue struct {
	// Path is the original file, mapped back from the wrapper.
	Path    string
	Field   string
	Message string
}

// CLIResult is one `claude plugin validate --strict` run over one root.
type CLIResult struct {
	Version string
	Checked int
	Issues  []CLIIssue
}

// ValidateWithCLI builds a wrapper for root and runs
// `claude plugin validate --strict --json` on it. --strict, because without it
// every finding is a warning and the exit code is 0.
//
// A non-zero exit with no issue we can attribute to a file is an error, not a
// pass: the run failed for a reason this function cannot describe, and reporting
// it as clean would be the "never ran" state that looks like success.
func ValidateWithCLI(claude, root string) (CLIResult, error) {
	tmp, err := os.MkdirTemp("", "lacquer-plugindefs-*")
	if err != nil {
		return CLIResult{}, err
	}
	defer os.RemoveAll(tmp)
	m, err := BuildWrapper(root, tmp)
	if err != nil {
		return CLIResult{}, err
	}
	res := CLIResult{Checked: len(m), Version: cliVersion(claude)}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "plugin", "validate", "--strict", "--json", tmp)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var rep struct {
		Success  bool `json:"success"`
		Manifest *struct {
			Errors   []issue `json:"errors"`
			Warnings []issue `json:"warnings"`
		} `json:"manifest"`
		Contents []struct {
			File     string  `json:"file"`
			Errors   []issue `json:"errors"`
			Warnings []issue `json:"warnings"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		return res, fmt.Errorf("claude plugin validate produced no JSON report (%v): %s", runErr, firstLine(stderr.String(), stdout.String()))
	}
	for _, c := range rep.Contents {
		orig, ok := m[c.File]
		if !ok {
			orig = c.File
		}
		// Under --strict a warning is a failure; report both.
		for _, i := range append(append([]issue{}, c.Errors...), c.Warnings...) {
			res.Issues = append(res.Issues, CLIIssue{Path: orig, Field: i.Path, Message: i.Message})
		}
	}
	if rep.Manifest != nil {
		for _, i := range append(append([]issue{}, rep.Manifest.Errors...), rep.Manifest.Warnings...) {
			// The wrapper's own manifest is ours, not the project's; a problem
			// with it is a bug here and must not read as a finding about them.
			return res, fmt.Errorf("the temporary plugin manifest was rejected: %s: %s", i.Path, i.Message)
		}
	}
	if (runErr != nil || !rep.Success) && len(res.Issues) == 0 {
		return res, errors.New("claude plugin validate failed without naming a file: " + firstLine(stderr.String(), stdout.String()))
	}
	return res, nil
}

type issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func cliVersion(claude string) string {
	out, err := exec.Command(claude, "--version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func firstLine(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return strings.SplitN(s, "\n", 2)[0]
		}
	}
	return "(no output)"
}
