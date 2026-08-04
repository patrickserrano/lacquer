// Package doctor proves that a project's checks can actually fail.
//
// Every serious defect found while onboarding this fleet was the same shape: a
// check that ran, reported success, and verified nothing.
//
//   - The editor hook called `swiftlint lint --path FILE`. `--path` had been
//     removed from SwiftLint; the command errored on every invocation and
//     `2>/dev/null || true` ate the message. It linted nothing for months and
//     looked healthy doing it.
//   - The DocC gate passed `DOCC_FLAGS=--warnings-as-errors`. That is a real
//     build setting name and it is silently ignored; a deliberately broken
//     symbol link still exited 0.
//   - The drift job ran `go build ./.lacquer-checkout/cmd/lacquer` from a
//     non-module directory. It failed in every repo, on every PR, for a reason
//     that had nothing to do with drift.
//
// None of these was caught by running the check. They were caught by accident,
// long after. A green check is not evidence unless you know it can go red.
//
// So doctor does not ask "does the check pass on this project" — CI already
// answers that. It writes a fixture that is KNOWN BAD, runs the check against
// it, and asserts the check FAILS. A probe that passes on known-bad input is
// reported as broken, because it is.
package doctor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Probe is one self-test: a fixture, a command, and the outcome that proves the
// check is wired up.
type Probe struct {
	Name string `toml:"name"`
	// Why this probe exists — printed on failure, so the person reading the
	// output learns what broke rather than just which assertion tripped.
	Why string `toml:"why"`
	// File and Content describe the fixture written into a scratch directory.
	// A probe with no File runs its command with no fixture (a tool-presence
	// check, say).
	File    string `toml:"file"`
	Content string `toml:"content"`
	// Argv is the command. Placeholders: {dir} is the scratch directory,
	// {component} the absolute component directory holding the synced configs.
	Argv []string `toml:"argv"`
	// Requires names tools the probe cannot run without: a bare name is looked
	// up on PATH, anything containing "/" is a path (placeholders substituted)
	// that must exist and be executable.
	//
	// Without this, a probe whose argv[0] is a shell wrapper is unsound in the
	// direction that matters. Argv[0] gets a PATH check below, which is enough
	// when argv[0] IS the tool — but a probe that runs `sh -c "…biome ci…"`
	// passes that check via /bin/sh, and then a MISSING biome exits non-zero,
	// which an expect="fail" probe reads as "the check correctly rejected the
	// bad input". A silent pass, produced by the very absence the probe exists
	// to notice. Every node-based check is in that shape, since its binary lives
	// in node_modules/.bin rather than on PATH.
	Requires []string `toml:"requires"`
	// Expect is "fail" (the usual case: known-bad input must be rejected) or
	// "pass" (the command must succeed, e.g. a tool exists and runs).
	Expect string `toml:"expect"`
}

type probeFile struct {
	Probe []Probe `toml:"probe"`
}

// Result is one probe's outcome.
type Result struct {
	Component string
	Profile   string
	Name      string
	OK        bool
	Detail    string
}

// LoadProbes reads a profile's self-tests. A profile shipping none is fine.
func LoadProbes(lacquerRoot, profile string) ([]Probe, error) {
	path := filepath.Join(lacquerRoot, "profiles", profile, "doctor.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f probeFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, p := range f.Probe {
		if p.Name == "" || len(p.Argv) == 0 {
			return nil, fmt.Errorf("%s: probe %d needs a name and argv", path, i)
		}
		switch p.Expect {
		case "fail", "pass":
		default:
			return nil, fmt.Errorf("%s: probe %q has expect=%q, want \"fail\" or \"pass\"", path, p.Name, p.Expect)
		}
	}
	return f.Probe, nil
}

// Run executes every probe for every profile the project declares.
//
// Probes run in a scratch directory OUTSIDE the project, so a fixture that is
// deliberately malformed can never be mistaken for project source, land in a
// commit, or be picked up by a watcher.
func Run(lacquerRoot, projectRoot string, cfg *config.Config, out io.Writer) ([]Result, error) {
	var results []Result

	comps := append([]config.Component(nil), cfg.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].Path < comps[j].Path })

	for _, c := range comps {
		compDir := projectRoot
		if c.Path != "." {
			resolved, err := safepath.Resolve(projectRoot, c.Path)
			if err != nil {
				return nil, fmt.Errorf("resolve component %s: %w", c.Path, err)
			}
			compDir = resolved
		}
		profiles := append([]string(nil), c.Profiles...)
		sort.Strings(profiles)

		for _, p := range profiles {
			probes, err := LoadProbes(lacquerRoot, p)
			if err != nil {
				return nil, err
			}
			for _, pr := range probes {
				r := runProbe(pr, c.Path, p, compDir)
				results = append(results, r)
				mark := "ok  "
				if !r.OK {
					mark = "FAIL"
				}
				fmt.Fprintf(out, "  %s  %s\n", mark, r.Name)
				if !r.OK {
					fmt.Fprintf(out, "        %s\n", r.Detail)
					if pr.Why != "" {
						fmt.Fprintf(out, "        why this matters: %s\n", pr.Why)
					}
				}
			}
		}
	}
	return results, nil
}

func runProbe(p Probe, compPath, profile, compDir string) Result {
	r := Result{Component: compPath, Profile: profile, Name: p.Name}

	dir, err := os.MkdirTemp("", "lacquer-doctor-")
	if err != nil {
		r.Detail = fmt.Sprintf("could not create a scratch dir: %v", err)
		return r
	}
	defer os.RemoveAll(dir)

	if p.File != "" {
		if err := os.WriteFile(filepath.Join(dir, p.File), []byte(p.Content), 0o644); err != nil {
			r.Detail = fmt.Sprintf("could not write the fixture: %v", err)
			return r
		}
	}

	subst := func(s string) string {
		s = strings.ReplaceAll(s, "{dir}", dir)
		return strings.ReplaceAll(s, "{component}", compDir)
	}

	argv := make([]string, len(p.Argv))
	for i, a := range p.Argv {
		argv[i] = subst(a)
	}

	// Requirements first: a missing tool must be reported as a missing tool
	// whatever the probe expects, never allowed to satisfy an expect="fail".
	for _, req := range p.Requires {
		if missing := missingTool(subst(req)); missing != "" {
			r.Detail = missing
			return r
		}
	}

	if _, err := exec.LookPath(argv[0]); err != nil {
		// A missing tool is a real finding: the check it backs cannot be running
		// wherever this is true. Not the same as the check being mis-wired, so
		// it is reported distinctly.
		r.Detail = fmt.Sprintf("%s is not installed, so this check cannot be running at all", argv[0])
		return r
	}

	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv comes from a lacquer-owned doctor.toml
	cmd.Dir = dir
	output, runErr := cmd.CombinedOutput()
	failed := runErr != nil

	switch p.Expect {
	case "fail":
		if failed {
			r.OK = true
			return r
		}
		r.Detail = "the check PASSED on deliberately broken input — it is not verifying what it claims to"
		if s := firstLine(output); s != "" {
			r.Detail += "\n        tool said: " + s
		}
	case "pass":
		if !failed {
			r.OK = true
			return r
		}
		r.Detail = fmt.Sprintf("expected success, got %v", runErr)
		if s := firstLine(output); s != "" {
			r.Detail += "\n        tool said: " + s
		}
	}
	return r
}

// missingTool returns a human-readable finding when req is unavailable, or "".
// A bare name is resolved on PATH; anything with a "/" must exist and carry an
// executable bit.
func missingTool(req string) string {
	if !strings.Contains(req, "/") {
		if _, err := exec.LookPath(req); err != nil {
			return fmt.Sprintf("%s is not installed, so this check cannot be running at all", req)
		}
		return ""
	}
	fi, err := os.Stat(req)
	if err != nil {
		return fmt.Sprintf("%s does not exist, so this check cannot be running at all (run the project's install step first)", req)
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return fmt.Sprintf("%s is not executable, so this check cannot be running at all", req)
	}
	return ""
}

// Failures returns the probes that did not behave as expected.
func Failures(rs []Result) []Result {
	var out []Result
	for _, r := range rs {
		if !r.OK {
			out = append(out, r)
		}
	}
	return out
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
