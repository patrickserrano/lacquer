package audit

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/plugindefs"
)

// pluginDefRoots are the rendered directories that hold a synced project's
// agents/, skills/ and commands/, one per enabled tool.
var pluginDefRoots = []string{".claude", ".codex", ".agents"}

// PluginDefs is the result of checking a project's rendered agent, skill and
// command definitions.
//
// Claude Code skips a malformed definition WITHOUT an error, so a broken agent
// looks exactly like one that is never used. Two layers look for that, and they
// cover different things, which the report states so a clean result is not read
// as more than it proves:
//
//   - internal/plugindefs, the four objective breakages `claude plugin validate`
//     provably misses (see that package);
//   - `claude plugin validate --strict` over a temporary plugin wrapper, which
//     proves frontmatter present and a description, when the CLI is installed.
//
// Reported, not gated: a project cannot fix a definition lacquer rendered into
// it, and the CLI is not installed on most machines this runs on.
type PluginDefs struct {
	// Checked is how many definition files were found under Roots.
	Checked int
	Roots   []string
	// Findings are the internal/plugindefs defects, with repo-relative paths.
	Findings []plugindefs.Finding

	// CLIFound is false when no `claude` binary was on PATH; nothing below it
	// was checked then, and that is reported as such rather than as a pass.
	CLIFound   bool
	CLIVersion string
	CLIIssues  []plugindefs.CLIIssue
	// CLIErrors are runs that could not be completed. Never folded into a pass.
	CLIErrors []string
}

// PluginDefinitions checks projectRoot's rendered definitions, using the
// `claude` binary on PATH for the CLI layer when there is one.
func PluginDefinitions(projectRoot string) PluginDefs {
	claude, _ := exec.LookPath("claude")
	return PluginDefinitionsWith(projectRoot, claude)
}

// PluginDefinitionsWith is PluginDefinitions with the CLI path given; "" means
// the CLI is not installed.
func PluginDefinitionsWith(projectRoot, claude string) PluginDefs {
	r := PluginDefs{CLIFound: claude != ""}
	rel := func(p string) string {
		if x, err := filepath.Rel(projectRoot, p); err == nil {
			return filepath.ToSlash(x)
		}
		return p
	}
	for _, name := range pluginDefRoots {
		root := filepath.Join(projectRoot, name)
		files := plugindefs.Files(root)
		if len(files) == 0 {
			continue
		}
		r.Roots = append(r.Roots, name)
		r.Checked += len(files)
		for _, f := range plugindefs.Check(files) {
			f.Path = rel(f.Path)
			r.Findings = append(r.Findings, f)
		}
		if claude == "" {
			continue
		}
		res, err := plugindefs.ValidateWithCLI(claude, root)
		if err != nil {
			r.CLIErrors = append(r.CLIErrors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if res.Version != "" {
			r.CLIVersion = res.Version
		}
		for _, i := range res.Issues {
			i.Path = rel(i.Path)
			r.CLIIssues = append(r.CLIIssues, i)
		}
	}
	return r
}

// FormatPluginDefs renders the report. Empty when the project has no rendered
// definitions at all (nothing synced, or a stack without any).
func FormatPluginDefs(r PluginDefs) string {
	if r.Checked == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nagent, skill and command definitions Claude Code loads (%d in %s):\n", r.Checked, strings.Join(r.Roots, ", "))

	// The first layer always ran; say what it found.
	if len(r.Findings) == 0 {
		b.WriteString("  lacquer check: ok — every agent has frontmatter opening on line 1, and a `name` with no ':'\n")
	} else {
		fmt.Fprintf(&b, "  lacquer check: %d problem(s) — Claude Code skips these silently, so they read as an agent that is never used:\n", len(r.Findings))
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "    %s:%d  %s\n", f.Path, f.Line, f.Problem)
		}
	}

	switch {
	case !r.CLIFound:
		b.WriteString("  claude plugin validate: not checked (claude CLI not found)\n")
	case len(r.CLIIssues) == 0 && len(r.CLIErrors) == 0:
		fmt.Fprintf(&b, "  claude plugin validate --strict (claude %s): ok\n", orUnknown(r.CLIVersion))
	default:
		if len(r.CLIIssues) > 0 {
			fmt.Fprintf(&b, "  claude plugin validate --strict (claude %s): %d problem(s):\n", orUnknown(r.CLIVersion), len(r.CLIIssues))
			for _, i := range r.CLIIssues {
				fmt.Fprintf(&b, "    %s  %s: %s\n", i.Path, i.Field, i.Message)
			}
		}
		for _, e := range r.CLIErrors {
			fmt.Fprintf(&b, "  claude plugin validate: NOT checked, the run failed — %s\n", e)
		}
	}

	b.WriteString("  What each proves: the lacquer check covers a missing or misplaced opening `---` and a missing or\n" +
		"  colon-containing agent `name`. The CLI covers frontmatter present, a description and most YAML that\n" +
		"  fails to parse; it does NOT catch a missing `name` or a `name` with ':'. Neither proves a definition\n" +
		"  steers Claude well. Reported only; the exit code does not change.\n")
	return b.String()
}

func orUnknown(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}
