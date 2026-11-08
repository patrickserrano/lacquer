package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// Disabling the hook must stop only its probes, before tools or fixtures run.
func TestDocumentationProbeOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "enabled"}[enabled], func(t *testing.T) {
			root, project := t.TempDir(), t.TempDir()
			write(t, filepath.Join(root, "profiles/ios/doctor.toml"), `
[[probe]]
name = "documentation"
ios_documentation_hook = true
argv = ["touch", "{root}/docs-ran"]
expect = "pass"
[[probe]]
name = "ordinary"
argv = ["touch", "{root}/ordinary-ran"]
expect = "pass"
`)
			cfg := cfgOne()
			cfg.Components[0].Profiles = []string{"ios"}
			if enabled {
				if _, err := toml.Decode("[ios]\ndocumentation_hook = true\n", cfg); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			results, err := Run(root, project, cfg, nil, &out)
			if err != nil || len(results) != 2 {
				t.Fatalf("results=%+v, err=%v", results, err)
			}
			if !results[0].OK || results[0].Skipped == enabled || !results[1].OK || results[1].Skipped {
				t.Fatalf("incorrect probe scope: %+v\n%s", results, &out)
			}
			for file, present := range map[string]bool{"docs-ran": enabled, "ordinary-ran": true} {
				_, err := os.Stat(filepath.Join(project, file))
				if present && err != nil || !present && !os.IsNotExist(err) {
					t.Errorf("%s present=%t: %v", file, present, err)
				}
			}
		})
	}
}
