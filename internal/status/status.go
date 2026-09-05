// Package status reports each project region's stamped version vs the lacquer latest.
package status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitattributes"
	"github.com/patrickserrano/lacquer/internal/gitignore"
	"github.com/patrickserrano/lacquer/internal/region"
	"github.com/patrickserrano/lacquer/internal/safepath"
	"github.com/patrickserrano/lacquer/internal/version"
)

type Row struct {
	Key     string          // "core" or a profile name
	Path    string          // file the region lives in, relative to project root
	Stamped version.Version // version found in the file (zero if absent)
	Found   bool
	Latest  version.Version
	Behind  bool
}

// Rows computes a status row for core and for each component profile.
func Rows(lacquerRoot, projectRoot string) ([]Row, error) {
	latest, err := version.Read(lacquerRoot)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
	if err != nil {
		return nil, err
	}

	var rows []Row
	rows = append(rows, rowFor(projectRoot, "CLAUDE.md", "core", latest, region.Markdown))
	for _, c := range cfg.Components {
		rel := filepath.Join(c.Path, "CLAUDE.md")
		for _, p := range c.Profiles {
			rows = append(rows, rowFor(projectRoot, rel, p, latest, region.Markdown))
		}
	}
	// The .gitignore region is stamped like any other, and a project that has
	// never received it reads as `missing` here — which is the state every
	// project in the fleet is in until it syncs, and the point of showing it.
	rows = append(rows, rowFor(projectRoot, gitignore.Name, gitignore.Key, latest, gitignore.Syntax))
	// Likewise .gitattributes. Listed rather than left off because `missing` here
	// is the actionable state: a project that has not synced this region is one
	// whose GitHub language bar is still counting ~620KB of lacquer-shipped
	// Python as its own source.
	rows = append(rows, rowFor(projectRoot, gitattributes.Name, gitattributes.Key, latest, gitattributes.Syntax))
	return rows, nil
}

func rowFor(projectRoot, rel, key string, latest version.Version, syn region.Syntax) Row {
	// Confine the read within the project root; a symlinked component dir that
	// escapes the root is treated as having no readable region rather than
	// reading a file outside the project.
	var content []byte
	if target, err := safepath.Resolve(projectRoot, rel); err == nil {
		content, _ = os.ReadFile(target)
	}
	stamped, found := syn.StampedVersion(string(content), key)
	return Row{
		Key:     key,
		Path:    rel,
		Stamped: stamped,
		Found:   found,
		Latest:  latest,
		Behind:  !found || stamped.Less(latest),
	}
}

// Format renders rows as an aligned text table.
func Format(rows []Row) string {
	var b strings.Builder
	b.WriteString("LAYER  PATH                 STAMPED  LATEST  STATUS\n")
	for _, r := range rows {
		status := "ok"
		if !r.Found {
			status = "missing"
		} else if r.Behind {
			status = "behind"
		}
		stamped := r.Stamped.String()
		if !r.Found {
			stamped = "-"
		}
		fmt.Fprintf(&b, "%-6s %-20s %-8s %-7s %s\n", r.Key, r.Path, stamped, r.Latest, status)
	}
	return b.String()
}
