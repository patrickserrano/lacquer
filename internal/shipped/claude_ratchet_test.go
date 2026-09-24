package shipped

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/region"
)

// Lower these ceilings in the same PR whenever managed text shrinks (including #453).
// Each ceiling includes marker lines and counts each rendered CLAUDE.md destination once.
const (
	rootappManagedLines    = 105
	multistackManagedLines = 137
	duoappManagedLines     = 105
	spmpackageManagedLines = 53
)

func TestManagedClaudeLineCeilings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ceiling int
	}{
		{"rootapp", rootappManagedLines}, {"multistack", multistackManagedLines},
		{"duoapp", duoappManagedLines}, {"spmpackage", spmpackageManagedLines},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fromFixture(t, tc.name)
			p.sync()
			regions := map[string]map[string]bool{"CLAUDE.md": {"core": true}}
			for _, c := range p.config().Components {
				path := filepath.Join(c.Path, "CLAUDE.md")
				if regions[path] == nil {
					regions[path] = map[string]bool{}
				}
				for _, profile := range c.Profiles {
					regions[path][profile] = true
				}
			}
			lines := 0
			for path, keys := range regions {
				if len(keys) == 0 {
					continue
				}
				content := p.read(path)
				for key := range keys {
					body, ok := region.ExtractBody(content, key)
					if !ok {
						t.Fatalf("%s missing %s region", path, key)
					}
					lines += strings.Count(body, "\n") + 3 // body lines plus both markers
				}
			}
			t.Logf("managed CLAUDE.md lines: %d; ceiling: %d", lines, tc.ceiling)
			if lines > tc.ceiling {
				t.Errorf("managed CLAUDE.md grew to %d lines; ceiling %d", lines, tc.ceiling)
			}
		})
	}
}
