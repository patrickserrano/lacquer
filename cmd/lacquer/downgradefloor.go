package main

import (
	"fmt"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/ratchet"
)

// downgradeFloor reads .lacquer.lock at the PR's base and at the checkout and
// applies audit.DowngradeFloor. A base that cannot be read is an error, never
// "no lock at the base": that would pass every downgrade it failed to see.
func downgradeFloor(projectRoot string) (blocking bool, report string, err error) {
	commit, data, present, err := ratchet.ReadBaseFile(projectRoot, lock.Name)
	if err != nil {
		return false, "", fmt.Errorf("downgrade floor: the PR's base could not be read, so this PR's lacquer version is unverified: %w", err)
	}
	var base *lock.Lock
	if present {
		if base, err = lock.Parse(data); err != nil {
			return false, "", fmt.Errorf("downgrade floor: %s at the base commit %s does not parse: %w", lock.Name, commit, err)
		}
	}
	head, _, err := lock.Read(projectRoot)
	if err != nil {
		return false, "", fmt.Errorf("downgrade floor: read %s: %w", lock.Name, err)
	}
	blocking, report = audit.DowngradeFloor(commit, base, head)
	return blocking, report, nil
}
