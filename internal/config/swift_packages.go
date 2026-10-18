package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// validatePackages checks every `packages` list (#522 U4b). Each entry becomes a
// directory in rendered shell (`swift build --package-path '<dir>'`) and a
// directory CI must find in a checkout, so an entry is refused unless it is a
// plain, canonical, relative path to a directory that holds a Package.swift and
// stays inside the component that lists it, on disk as well as on paper.
//
// root is the project root. With none (a Config built in memory) the checks
// that read the filesystem are skipped; the ones about the spelling are not.
func validatePackages(root string, comps []Component) error {
	declared := map[string]string{} // clean component path -> as written
	for _, c := range comps {
		declared[path.Clean(filepath.ToSlash(c.Path))] = c.Path
	}
	seen := map[string]string{} // project-relative package dir -> component
	for _, c := range comps {
		if len(c.Packages) == 0 {
			continue
		}
		label := fmt.Sprintf("[[component]] %q", c.Path)
		if !isSwiftComponent(c) {
			return fmt.Errorf("%s: packages lists Swift packages, and this is not a Swift component "+
				"(it needs the ios profile or stack = \"ios\")", label)
		}
		cp := path.Clean(filepath.ToSlash(c.Path))
		for _, entry := range c.Packages {
			dir, err := validatePackageEntry(root, label, cp, entry)
			if err != nil {
				return err
			}
			if _, ok := declared[dir]; ok {
				return fmt.Errorf("%s: packages entry %q is the declared component %q. A directory is either a component "+
					"(linted and built on its own) or a package listed on the component that contains it, not both: "+
					"remove it from packages, or remove its [[component]]", label, entry, declared[dir])
			}
			if prev, ok := seen[dir]; ok {
				return fmt.Errorf("%s: packages entry %q (%s) is listed twice; %s already lists it", label, entry, dir, prev)
			}
			seen[dir] = label
		}
	}
	return nil
}

func isSwiftComponent(c Component) bool {
	if c.Stack == "ios" {
		return true
	}
	for _, p := range c.Profiles {
		if p == "ios" {
			return true
		}
	}
	return false
}

// validatePackageEntry returns the project-relative directory an entry names,
// or the reason it is refused.
func validatePackageEntry(root, label, comp, entry string) (string, error) {
	if entry == "" {
		return "", fmt.Errorf("%s: packages has an empty entry; list a directory relative to %s/", label, comp)
	}
	if filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
		return "", fmt.Errorf("%s: packages entry %q must be relative to the component (%s/), not absolute", label, entry, comp)
	}
	clean := path.Clean(filepath.ToSlash(entry))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s: packages entry %q escapes the component %q. A package outside it is not nested in it: "+
			"declare it as its own [[component]] (stack = \"ios\") beside this one", label, entry, comp)
	}
	if clean == "." {
		return "", fmt.Errorf("%s: packages entry %q is the component itself; list a package directory below %s/", label, entry, comp)
	}
	if !componentPathVal.MatchString(clean) {
		return "", fmt.Errorf("%s: packages entry %q contains unsafe characters; entries reach CI shell, so each segment is "+
			"letters, digits, '.', '_' or '-' and does not start with '-'", label, entry)
	}
	if entry != clean {
		return "", fmt.Errorf("%s: packages entry %q is not in canonical form; write %q", label, entry, clean)
	}
	dir := clean
	if comp != "." {
		dir = comp + "/" + clean
	}
	if root == "" {
		return dir, nil
	}
	if err := packageOnDisk(root, comp, clean, dir); err != nil {
		return "", fmt.Errorf("%s: packages entry %q: %w", label, entry, err)
	}
	return dir, nil
}

// packageOnDisk checks that dir is a directory holding a Package.swift, and
// that neither resolves (through any symlink) to somewhere outside the
// component. A symlink inside the component is fine, because the checkout CI
// builds is the same tree; one that leaves it would build a package the
// component does not own.
func packageOnDisk(root, comp, clean, dir string) error {
	full := filepath.Join(root, filepath.FromSlash(dir))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s does not exist", dir)
		}
		return err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory; list the directory that holds a Package.swift", dir)
	}
	compRoot, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(comp)))
	if err != nil {
		return err
	}
	if !within(compRoot, resolved) {
		return fmt.Errorf("%s is a symlink that leads outside the component %s/; list a real directory inside it", dir, comp)
	}
	manifest, err := filepath.EvalSymlinks(filepath.Join(resolved, "Package.swift"))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s has no Package.swift; list the directory that holds one", dir)
		}
		return err
	}
	if !within(compRoot, manifest) {
		return fmt.Errorf("%s/Package.swift is a symlink that leads outside the component %s/", dir, comp)
	}
	return nil
}

// within reports whether p is dir or beneath it. Both are already resolved.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
