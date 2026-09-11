package pluginbootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Provided is a plugin a locally installed TOOL materializes on disk, rather
// than one a marketplace serves.
//
// Xcode 27 is the case that produced this. It ships a real Claude Code plugin --
// manifest, fifteen skills and an MCP server declaration -- but never publishes
// it anywhere `claude plugin install` can reach. It materializes it under
//
//	~/Library/Developer/Xcode/CodingAssistant/ExportedPlugins/<build>/<format>
//
// and that path carries the XCODE BUILD, so every Xcode upgrade moves it. A
// symlink into the user's skills directory therefore stops resolving the day
// Xcode updates, and the plugin silently stops loading -- no error, just fifteen
// skills and an MCP server quietly absent from every session. That is the defect
// class in issue #333 arriving through a filesystem path, and it is why this is
// re-applied rather than done once.
//
// The alternative was exporting copies with `skills export`. It is worse twice
// over: it produced TEN skills where the plugin has fifteen -- silently missing
// three accessibility specialists, translation and translation-coordinator --
// and a copy has no relationship to the Xcode that produced it, so nothing can
// tell a current copy from a stale one.
type Provided struct {
	Name     string `toml:"name"`
	Provider string `toml:"provider"` // closed set, see providers
	Format   string `toml:"format"`   // closed set, see formats
}

// provider is how one tool is asked where it put its plugin.
//
// Modelled as a closed set rather than argv in the manifest. The manifest is the
// lacquer's own, so this is not a privilege boundary -- it is a legibility one:
// "provider = xcode" says what it means, and an argv array would invite a
// manifest to become a place where commands are written.
type provider struct {
	// locate materializes the plugin and prints its path on stdout.
	locate func(format string) []string
	// toolDir is the per-format directory a plugin is linked into, relative to
	// the user's home.
	toolDir map[string]string
}

var providers = map[string]provider{
	"xcode": {
		locate: func(format string) []string {
			return []string{"mcpbridge", "run-agent", "plugin", "path", "--plugin-format", format}
		},
		toolDir: map[string]string{
			"claude": ".claude/skills",
			"codex":  ".codex/skills",
		},
	},
}

// ToolRunner runs the locating tool. A package var so tests can drive every
// branch without Xcode installed.
var ToolRunner = func(bin string, args ...string) ([]byte, error) {
	return runTool(bin, args...)
}

// LinkState is what happened to one Provided entry.
type LinkState struct {
	Name    string
	Format  string
	Path    string // the resolved plugin path, when known
	Action  string // "linked", "relinked", "current", "unavailable", "refused"
	Details string
}

// Ok reports whether the plugin is now present and pointing at the tool's
// current content.
func (l LinkState) Ok() bool {
	return l.Action == "linked" || l.Action == "relinked" || l.Action == "current"
}

// ApplyProvided links every declared tool-provided plugin into the user's tool
// directories, re-pointing any link the tool has since moved.
//
// home is the user's home directory, taken as an argument so a test never
// touches the real one.
//
// An absent tool is reported as "unavailable", never skipped silently: a machine
// without Xcode is a legitimate state, but "I did not link it" and "there was
// nothing to link" are different answers and collapsing them is how a bootstrap
// step starts lying about what it set up.
func ApplyProvided(home string, ps []Provided) []LinkState {
	var out []LinkState
	for _, p := range ps {
		prov, ok := providers[p.Provider]
		if !ok {
			out = append(out, LinkState{Name: p.Name, Format: p.Format, Action: "refused",
				Details: fmt.Sprintf("unknown provider %q", p.Provider)})
			continue
		}
		dir, ok := prov.toolDir[p.Format]
		if !ok {
			out = append(out, LinkState{Name: p.Name, Format: p.Format, Action: "refused",
				Details: fmt.Sprintf("provider %q does not serve format %q", p.Provider, p.Format)})
			continue
		}

		argv := prov.locate(p.Format)
		raw, err := ToolRunner(argv[0], argv[1:]...)
		if err != nil {
			out = append(out, LinkState{Name: p.Name, Format: p.Format, Action: "unavailable",
				Details: fmt.Sprintf("%s could not be asked where its plugin is: %v", p.Provider, err)})
			continue
		}
		// The tool prints the path on the last non-empty line; earlier lines can
		// carry progress ("Launching Xcode...").
		want := lastLine(string(raw))
		if want == "" || !filepath.IsAbs(want) {
			out = append(out, LinkState{Name: p.Name, Format: p.Format, Action: "unavailable",
				Details: fmt.Sprintf("%s printed no absolute path", p.Provider)})
			continue
		}
		if _, err := os.Stat(want); err != nil {
			out = append(out, LinkState{Name: p.Name, Format: p.Format, Path: want, Action: "unavailable",
				Details: fmt.Sprintf("%s named a path that does not exist: %s", p.Provider, want)})
			continue
		}

		link := filepath.Join(home, dir, p.Name)
		state, details := linkTo(link, want)
		out = append(out, LinkState{Name: p.Name, Format: p.Format, Path: want, Action: state, Details: details})
	}
	return out
}

// linkTo points link at want, and reports what it had to do.
//
// It REFUSES to replace anything that is not already a symlink. A real directory
// at that path is somebody's own plugin, or a copy they made deliberately, and
// silently deleting it to install ours would be the worst thing in this file.
func linkTo(link, want string) (state, details string) {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return "refused", err.Error()
	}
	fi, err := os.Lstat(link)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink == 0:
		return "refused", fmt.Sprintf("%s exists and is not a symlink; leaving it alone", link)
	case err == nil:
		had, _ := os.Readlink(link)
		if had == want {
			return "current", ""
		}
		if err := os.Remove(link); err != nil {
			return "refused", err.Error()
		}
		if err := os.Symlink(want, link); err != nil {
			return "refused", err.Error()
		}
		// A moved path is the ordinary case after a tool upgrade, and it is worth
		// naming: it is the difference between "nothing to do" and "this had
		// silently stopped loading".
		return "relinked", fmt.Sprintf("was %s", had)
	default:
		if err := os.Symlink(want, link); err != nil {
			return "refused", err.Error()
		}
		return "linked", ""
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}
