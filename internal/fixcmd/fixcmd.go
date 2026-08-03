// Package fixcmd runs each profile's autofixers inside the components that
// declare it, so adopting the lacquer costs a formatter run rather than a
// thousand hand edits.
//
// It exists because of what onboarding ten apps actually looked like: the
// synced .swiftlint.yml found 509 violations in one app and 290 in another, and
// roughly two thirds of them were mechanical — member ordering, import sorting,
// trailing closures. Every one of those was a merge blocker a tool could have
// fixed. `sync` put the config in place and then left the work to a human.
//
// FIXERS ARE NOT CHECKS, and the failure rules are deliberately opposite. A
// check whose tool is missing must BLOCK: reporting success for something never
// verified is how a hook rots for months (see scripts/precommit-swift.sh). A
// fixer whose tool is missing must SKIP loudly: the code is merely unfixed, the
// CI check will still catch it, and failing the sync would make a missing
// Homebrew formula block adoption entirely.
package fixcmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Command is one autofixer.
type Command struct {
	Name string   `toml:"name"`
	Argv []string `toml:"argv"`
}

type fixFile struct {
	Command []Command `toml:"command"`
}

// maxPasses bounds the convergence loop below. Three is empirically enough:
// every project measured settled on the second pass.
const maxPasses = 3

// Result is what happened to one command in one component.
type Result struct {
	Component string
	Profile   string
	Name      string
	Status    string // "ran", "skipped: <tool> not installed", or "failed: <err>"
	Passes    int    // how many times it ran before the tree stopped changing
}

// LoadCommands reads a profile's autofixers. A profile shipping no fix.toml has
// none, which is not an error.
func LoadCommands(lacquerRoot, profile string) ([]Command, error) {
	path := filepath.Join(lacquerRoot, "profiles", profile, "fix.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f fixFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, c := range f.Command {
		if c.Name == "" || len(c.Argv) == 0 {
			return nil, fmt.Errorf("%s: command %d needs both name and argv", path, i)
		}
	}
	return f.Command, nil
}

// Run executes every profile's autofixers in each component declaring it.
//
// Commands run with the component directory as the working directory, because
// every fixer here is configured by a file that sync writes into that directory
// (.swiftformat, biome.json, deno.jsonc). Running from the project root would
// pick up the wrong config, or none.
func Run(lacquerRoot, projectRoot string, cfg *config.Config, out io.Writer) ([]Result, error) {
	var results []Result

	comps := make([]config.Component, len(cfg.Components))
	copy(comps, cfg.Components)
	sort.Slice(comps, func(i, j int) bool { return comps[i].Path < comps[j].Path })

	for _, c := range comps {
		// "." is the project root itself. Routing it through safepath.Resolve
		// asks "does this path escape the root", and the answer for the root is
		// a symlink comparison against itself — which fails whenever the root is
		// reached through a symlink (every macOS /var/folders temp dir, and any
		// checkout under one). Config already validates component paths, so the
		// root needs no confinement check: it cannot escape itself.
		dir := projectRoot
		if c.Path != "." {
			resolved, err := safepath.Resolve(projectRoot, c.Path)
			if err != nil {
				return nil, fmt.Errorf("resolve component %s: %w", c.Path, err)
			}
			dir = resolved
		}
		profiles := append([]string(nil), c.Profiles...)
		sort.Strings(profiles)

		for _, p := range profiles {
			cmds, err := LoadCommands(lacquerRoot, p)
			if err != nil {
				return nil, err
			}
			for _, cmd := range cmds {
				r := Result{Component: c.Path, Profile: p, Name: cmd.Name}

				// Resolve the binary before running so "not installed" is
				// reported as itself rather than as an opaque exec error.
				if _, err := exec.LookPath(cmd.Argv[0]); err != nil {
					r.Status = fmt.Sprintf("skipped: %s is not installed", cmd.Argv[0])
					fmt.Fprintf(out, "  %-24s %s\n", cmd.Name, r.Status)
					results = append(results, r)
					continue
				}

				// Run to a FIXED POINT, not once. swiftformat with
				// organizeDeclarations is not idempotent in one pass: reordering
				// members changes their nesting, and the indent rule only sees
				// the new layout on the next run. A single pass therefore left
				// kit with 6 files that `swiftformat --lint` still rejected —
				// which its own pre-push hook caught, after `lacquer fix` had
				// reported success. A fixer that leaves the tree failing the
				// check it exists to satisfy has not finished.
				var combined []byte
				var runErr error
				passes := 0
				// Compare the tree BETWEEN passes. "Is the tree dirty?" is the
				// wrong question — sync just wrote hundreds of files, so it is
				// always dirty and the loop would always run to the cap. What
				// matters is whether THIS pass moved anything.
				prev, stateErr := treeState(dir)
				for passes < maxPasses {
					ex := exec.Command(cmd.Argv[0], cmd.Argv[1:]...) // #nosec G204 -- argv comes from a lacquer-owned fix.toml, never user input
					ex.Dir = dir
					combined, runErr = ex.CombinedOutput()
					passes++
					if stateErr != nil {
						break // no git to compare against: one pass, as before
					}
					cur, err := treeState(dir)
					if err != nil || cur == prev {
						break
					}
					prev = cur
				}
				r.Passes = passes
				if runErr != nil {
					// A fixer that exits non-zero has usually still fixed what
					// it could (swiftlint --fix exits non-zero when unfixable
					// violations remain). Report it; never abort the run.
					r.Status = fmt.Sprintf("finished with %v", runErr)
					fmt.Fprintf(out, "  %-24s %s\n", cmd.Name, r.Status)
					if len(combined) > 0 {
						fmt.Fprintf(out, "    %s\n", lastLine(combined))
					}
					results = append(results, r)
					continue
				}
				r.Status = "ran"
				fmt.Fprintf(out, "  %-24s ran (%d pass%s)\n", cmd.Name, passes, plural(passes))
				results = append(results, r)
			}
		}
	}
	return results, nil
}

// lastLine returns the final non-empty line of tool output, which is where both
// swiftlint and swiftformat put their summary.
func lastLine(b []byte) string {
	lines := splitLines(string(b))
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			return lines[i]
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimCR(s[start:]))
	return out
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

// treeState is a cheap fingerprint of the working tree, used only to compare
// one pass against the next. `git status --porcelain` names every changed path;
// it does not change when file CONTENT changes but the set of dirty paths does
// not, so pair it with the diff hash.
func treeState(dir string) (string, error) {
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = dir
	s, err := status.Output()
	if err != nil {
		return "", err
	}
	// `git diff` covers content changes to already-dirty files, which is the
	// common case here: pass two reindents a file pass one had already moved.
	diff := exec.Command("git", "diff")
	diff.Dir = dir
	d, err := diff.Output()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x|%x", sha256.Sum256(s), sha256.Sum256(d)), nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
