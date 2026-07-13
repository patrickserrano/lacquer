// Package pluginbootstrap installs the machine-level Claude Code plugins
// this fleet relies on (core/bootstrap/plugins.toml) via the `claude
// plugin` CLI. Unlike internal/skillsync (per-project, driven by
// [project].skills in a project's own .lacquer.toml), this always applies
// at user scope — plugins install once and are shared across every project
// on the machine, so the manifest lives in the lacquer repo itself.
package pluginbootstrap

import (
	"fmt"
	"os/exec"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Marketplace is one `claude plugin marketplace add <source>` entry.
type Marketplace struct {
	Name   string `toml:"name"`
	Source string `toml:"source"` // GitHub "<owner>/<repo>"
}

// Plugin is one `claude plugin install <name>` entry, name in the CLI's own
// "<plugin>@<marketplace>" form.
type Plugin struct {
	Name string `toml:"name"`
}

// Manifest is the parsed core/bootstrap/plugins.toml.
type Manifest struct {
	Marketplaces []Marketplace `toml:"marketplace"`
	Plugins      []Plugin      `toml:"plugin"`
}

// Values are passed to `claude` as separate argv elements, never
// shell-interpolated, but are still charset-validated so a malformed
// manifest entry fails at Load time with a clear error instead of a
// confusing CLI failure, and so a value can never be mistaken for a flag
// (no leading "-").
var (
	marketplaceNameVal = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	marketplaceSrcVal  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	pluginNameVal      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Load reads, parses, and validates path.
func Load(path string) (*Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, err
	}
	for _, mkt := range m.Marketplaces {
		if !marketplaceNameVal.MatchString(mkt.Name) {
			return nil, fmt.Errorf("invalid marketplace name %q", mkt.Name)
		}
		if !marketplaceSrcVal.MatchString(mkt.Source) {
			return nil, fmt.Errorf("invalid marketplace source %q (expected \"<owner>/<repo>\")", mkt.Source)
		}
	}
	for _, p := range m.Plugins {
		if !pluginNameVal.MatchString(p.Name) {
			return nil, fmt.Errorf("invalid plugin name %q (expected \"<plugin>@<marketplace>\")", p.Name)
		}
	}
	return &m, nil
}

// Runner executes one `claude` CLI invocation and returns its combined
// output. Injectable for tests — the real implementation shells out for
// real, which needs the `claude` binary on PATH and mutates the machine's
// global plugin state, neither available/safe in a unit test sandbox.
var Runner = func(args ...string) ([]byte, error) {
	cmd := exec.Command("claude", args...)
	return cmd.CombinedOutput()
}

// Result summarizes one Apply call.
type Result struct {
	Marketplaces []string          // marketplace names successfully added (or already present)
	Plugins      []string          // plugin names successfully installed (or already present)
	Failed       map[string]string // "marketplace:<name>" or "<plugin>@<marketplace>" -> error output
}

// Apply adds every declared marketplace, then installs every declared
// plugin (`claude plugin marketplace add`/`claude plugin install`, both
// confirmed idempotent against the live CLI — an already-configured
// marketplace or already-installed plugin is a clean no-op, not an error).
// It keeps going after an individual failure so one bad entry doesn't block
// the rest.
func Apply(m *Manifest) Result {
	res := Result{Failed: map[string]string{}}

	for _, mkt := range m.Marketplaces {
		out, err := Runner("plugin", "marketplace", "add", mkt.Source)
		if err != nil {
			res.Failed["marketplace:"+mkt.Name] = fmt.Sprintf("%v\n%s", err, out)
			continue
		}
		res.Marketplaces = append(res.Marketplaces, mkt.Name)
	}
	for _, p := range m.Plugins {
		out, err := Runner("plugin", "install", p.Name)
		if err != nil {
			res.Failed[p.Name] = fmt.Sprintf("%v\n%s", err, out)
			continue
		}
		res.Plugins = append(res.Plugins, p.Name)
	}
	return res
}
