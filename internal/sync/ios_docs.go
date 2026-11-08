package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitguard"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/safepath"
)

type documentationRetirement struct {
	dest, hash string
}

// prepareDocumentationRetirement authorizes only the retired docs assets.
// Both lock ownership and a known legacy render must match. This is not
// general orphan cleanup: edited, untracked and project-owned files survive.
func prepareDocumentationRetirement(lacquerRoot, projectRoot string, cfg *config.Config, current []assets.Asset) ([]documentationRetirement, []string, error) {
	if cfg.IOS.DocumentationHook {
		return nil, nil, nil
	}
	want := map[string]bool{}
	for _, comp := range cfg.Components {
		for _, profile := range comp.Profiles {
			if profile == "ios" {
				want["scripts/docs-hook.sh"] = true
				want["scripts/docs-relaxation.sh"] = true
				want[filepath.ToSlash(filepath.Join(comp.Path, ".swiftlint-docs.yml"))] = true
			}
		}
	}
	if len(want) == 0 {
		return nil, nil, nil
	}
	if fi, err := os.Lstat(filepath.Join(projectRoot, lock.Name)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("refusing documentation cleanup through a symlinked %s", lock.Name)
	}
	lk, present, err := lock.Read(projectRoot)
	if err != nil || !present {
		return nil, nil, err
	}
	known := false
	for dest := range want {
		known = known || lk.Files[dest] != ""
	}
	if !known {
		return nil, nil, nil
	}
	legacy := *cfg
	legacy.IOS.DocumentationHook = true
	plan, err := assets.Plan(lacquerRoot, &legacy)
	if err != nil {
		return nil, nil, err
	}
	tracked, err := gitguard.Tracked(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	owned := map[string]bool{}
	for _, dest := range tracked {
		owned[dest] = true
	}
	dirty, err := gitguard.DirtyPaths(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	active := map[string]bool{}
	for _, asset := range current {
		active[filepath.ToSlash(asset.Dest)] = true
	}
	var remove []documentationRetirement
	var preserve []string
	for _, asset := range plan {
		dest := filepath.ToSlash(asset.Dest)
		if !want[dest] || active[dest] || cfg.Project.Excludes(dest) || cfg.Project.SeedsOnce(dest) {
			continue
		}
		target, err := safepath.Resolve(projectRoot, dest)
		if err != nil {
			return nil, nil, err
		}
		fi, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !fi.Mode().IsRegular() || !owned[dest] || dirty[dest] || lk.Files[dest] == "" {
			preserve = append(preserve, dest)
			continue
		}
		body, missing, err := assets.Render(asset, &legacy)
		if err != nil {
			return nil, nil, fmt.Errorf("render legacy documentation file %s: %w", dest, err)
		}
		if len(missing) != 0 {
			return nil, nil, fmt.Errorf("legacy documentation file %s has missing tokens: %v", dest, missing)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, nil, err
		}
		hash := lock.Hash(string(body))
		observed := lock.Hash(string(data))
		if observed != lk.Files[dest] || (observed != hash && !publishedDocumentationHash(dest, observed)) {
			preserve = append(preserve, dest)
			continue
		}
		remove = append(remove, documentationRetirement{dest: dest, hash: observed})
	}
	return remove, preserve, nil
}

// removeDocumentationRetirements runs after all ordinary sync preflights and
// writes succeed. Recheck paths, git state and bytes before removing a file.
func removeDocumentationRetirements(projectRoot string, files []documentationRetirement) (removed, preserved []string, err error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	dirty, err := gitguard.DirtyPaths(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, file := range files {
		target, err := safepath.Resolve(projectRoot, file.dest)
		if err != nil {
			return removed, preserved, err
		}
		fi, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, preserved, err
		}
		if !fi.Mode().IsRegular() || dirty[file.dest] {
			preserved = append(preserved, file.dest)
			continue
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return removed, preserved, err
		}
		if lock.Hash(string(data)) != file.hash {
			preserved = append(preserved, file.dest)
			continue
		}
		if err := os.Remove(target); err != nil {
			return removed, preserved, err
		}
		removed = append(removed, file.dest)
	}
	return removed, preserved, nil
}

// These published sources contained no placeholders, so their source digests
// are also their exact render digests. Retain older entries for safe upgrades.
func publishedDocumentationHash(dest, hash string) bool {
	switch filepath.Base(dest) {
	case "docs-relaxation.sh":
		switch hash {
		case "17f84600416bb08111465e6f3f1f846e39033d00bf6675baee624244203b562e", // 98940b0
			"b2773a038c1fb691bd51a1796932970629653ecae68a8a5ef7a3dd31c6d49aa4": // c8cf78e
			return true
		}
	case "docs-hook.sh":
		switch hash {
		case "8c47f75c0fd05131bbfb370d773ed646e794b70a7e6a1fb6d43a09e4ef39d4ea", // 413a099
			"793edb24b0a9120f9de28501169e5675949185c5f41a32a2d8b656690719e06f", // 308bd83
			"3d801acebe38539cf53de9a88c8da4eb6aa6ac3f7f3dfb323f5adde644d8a419": // 9c51e2e
			return true
		}
	case ".swiftlint-docs.yml":
		switch hash {
		case "635a0c8619e7483e78554f91d3c6cc288dbf4c007a35d2ee270322a06bf01930", // f5dda41
			"1e96b174a7d9ee34a1bb09034c9bb94e3b81b0cf020098f352a49e3d0556bfe4", // 58faf9c
			"3adf3f0b29ab89164a154c2851e38540d3cab22e7c01a1eeac97737b450bf267", // 3ffa4a3
			"7d73fdc4d6ad6a5ca4f09db124cdceaf1baf65e213a04d68a6c6ea85822e6c74": // c8cf78e
			return true
		}
	}
	return false
}
