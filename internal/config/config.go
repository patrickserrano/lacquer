// Package config parses a project's .harness.toml manifest.
package config

import "github.com/BurntSushi/toml"

type Project struct {
	Name string `toml:"name"`
}

type Component struct {
	Path     string   `toml:"path"`
	Profiles []string `toml:"profiles"`
}

type Config struct {
	Project    Project     `toml:"project"`
	Components []Component `toml:"component"`
}

// Load reads and parses the .harness.toml at path.
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
