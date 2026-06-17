// Package initcmd implements `harness init`: detect a project's components and
// write a .harness.toml stub for the operator to complete.
package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/detect"
)

// Run detects components under root and writes a .harness.toml. It refuses to
// overwrite an existing manifest. It returns a human-readable summary of what it
// wrote and which [project] values still need filling.
func Run(root string) (string, error) {
	manifest := filepath.Join(root, ".harness.toml")
	if _, err := os.Stat(manifest); err == nil {
		return "", fmt.Errorf(".harness.toml already exists at %s; refusing to overwrite", manifest)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	comps, derived, err := detect.Components(root)
	if err != nil {
		return "", fmt.Errorf("detect components: %w", err)
	}

	name := derived.ProjectName
	if name == "" {
		name = filepath.Base(root)
	}

	var b strings.Builder
	b.WriteString("[project]\n")
	fmt.Fprintf(&b, "name = %q\n", name)
	fmt.Fprintf(&b, "project_name = %q\n", derived.ProjectName)
	fmt.Fprintf(&b, "scheme = %q\n", derived.Scheme)
	fmt.Fprintf(&b, "xcodeproj = %q\n", derived.Xcodeproj)
	b.WriteString("bundle_id = \"\"\n")
	b.WriteString("asc_app_id = \"\"\n")
	for _, c := range comps {
		b.WriteString("\n[[component]]\n")
		fmt.Fprintf(&b, "path = %q\n", c.Path)
		fmt.Fprintf(&b, "profiles = [%q]\n", c.Profiles[0])
	}

	if err := os.WriteFile(manifest, []byte(b.String()), 0o644); err != nil {
		return "", err
	}

	var s strings.Builder
	if len(comps) == 0 {
		s.WriteString("No components detected (no .xcodeproj / package.json / Cargo.toml / go.mod found).\n")
	} else {
		s.WriteString("Detected components:\n")
		for _, c := range comps {
			fmt.Fprintf(&s, "  %s -> %s\n", c.Path, c.Profiles[0])
		}
	}
	fmt.Fprintf(&s, "Wrote %s\n", manifest)
	s.WriteString("Fill any blank [project] values (e.g. bundle_id, asc_app_id), then run `harness sync`.")
	return s.String(), nil
}
