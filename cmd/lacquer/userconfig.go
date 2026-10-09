package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// The user config file is lacquer's per-user (not per-project) settings. It
// exists because a background agent session does not inherit the caller's
// environment, so $LACQUER_FLEET_REPO is gone by the time `lacquer decisions
// --fleet` runs there. It holds one key today, `fleet_repo = "owner/name"`.
const (
	userConfigRel      = "lacquer/config.toml"
	userConfigFleetKey = "fleet_repo"
)

// userConfigPath is where the user config lives: $XDG_CONFIG_HOME/lacquer/config.toml
// when that is set to an absolute path (the XDG rule: a relative one is ignored),
// else $HOME/.config/lacquer/config.toml. It is "" when neither is usable.
func userConfigPath(getenv func(string) string) string {
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, userConfigRel)
	}
	if h := getenv("HOME"); h != "" {
		return filepath.Join(h, ".config", userConfigRel)
	}
	return ""
}

// isOwnerName reports whether s is a GitHub owner/name.
func isOwnerName(s string) bool {
	o, n, ok := strings.Cut(s, "/")
	return ok && o != "" && n != "" && !strings.ContainsAny(s, " \t\r\n") && !strings.HasPrefix(s, "-") && !strings.Contains(n, "/")
}

// fleetRepoFromConfig reads fleet_repo from the user config. A missing file, or
// one that does not set the key, is "" with no error: the caller falls through.
// Anything else wrong with it (unreadable, not TOML, an unknown key, a value
// that is not owner/name) is an error that names the file, never a fall-through,
// because a typo'd key read as "not configured" is a state that looks like working.
func fleetRepoFromConfig(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("cannot read the user config %s: %w", path, err)
	}
	var cfg struct {
		FleetRepo string `toml:"fleet_repo"`
	}
	md, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return "", fmt.Errorf("the user config %s is not valid: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return "", fmt.Errorf("the user config %s has an unknown key %q; the only key is %s", path, u[0].String(), userConfigFleetKey)
	}
	if cfg.FleetRepo != "" && !isOwnerName(cfg.FleetRepo) {
		return "", fmt.Errorf("the user config %s sets %s = %q, which is not owner/name", path, userConfigFleetKey, cfg.FleetRepo)
	}
	return cfg.FleetRepo, nil
}

// resolveFleetRepo is the fleet repository: --fleet-repo, else $LACQUER_FLEET_REPO,
// else fleet_repo in the user config, else an error naming all three. A higher
// source wins without the lower ones being read, so a broken config file never
// blocks a command that was told the repository explicitly. There is no default:
// this repository is public.
func resolveFleetRepo(flagVal string, getenv func(string) string) (string, error) {
	if r := fleetRepoFrom(flagVal, getenv); r != "" {
		return r, nil
	}
	path := userConfigPath(getenv)
	r, err := fleetRepoFromConfig(path)
	if err != nil {
		return "", err
	}
	if r != "" {
		return r, nil
	}
	where := path
	if where == "" {
		where = "$XDG_CONFIG_HOME/" + userConfigRel + " or $HOME/.config/" + userConfigRel + " (neither is set)"
	}
	return "", fmt.Errorf("no fleet repository is configured; pass --fleet-repo owner/name, set $%s, or set %s = \"owner/name\" in %s", envFleetRepo, userConfigFleetKey, where)
}
