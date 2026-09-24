package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/patrickserrano/lacquer/internal/actionlint"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

// Configuration probes inspect the synced file, not the source template. The
// negative controls keep an empty config from standing in for a working check.
func checkConfig(check, root, component string) error {
	switch check {
	case "actionlint-labels":
		if actionlint.Check([]byte("self-hosted-runner:\n  labels: []\n")) == nil {
			return fmt.Errorf("runner-label check accepted an empty label list")
		}
		path, err := safepath.Resolve(root, actionlint.Name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return actionlint.Check(data)
	case "biome-ignores":
		cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Join(component, "biome.json"))
		if err != nil {
			return err
		}
		path, err := safepath.Resolve(root, rel)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if checkBiomeIgnores([]byte(`{"files":{"includes":["**"]}}`), []string{"!**/doctor-generated.ts"}) == nil {
			return fmt.Errorf("ignore check accepted a missing declared ignore")
		}
		return checkBiomeIgnores(data, cfg.Web.BiomeIgnores)
	default:
		return fmt.Errorf("unknown configuration probe %q", check)
	}
}

func checkBiomeIgnores(data []byte, ignores []string) error {
	var doc struct {
		Files struct {
			Includes []string `json:"includes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Files.Includes) == 0 {
		return fmt.Errorf("biome.json has no files.includes")
	}
	for _, pattern := range ignores {
		if !slices.Contains(doc.Files.Includes, pattern) {
			return fmt.Errorf("biome.json is missing declared ignore %q", pattern)
		}
	}
	return nil
}
