// Package xcodegendrift reports settings lost or introduced by XcodeGen without
// replacing the project's own files. Findings are informational during rollout.
package xcodegendrift

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/baseline"
)

// Finding names a setting present on only one side, or why comparison failed.
type Finding struct {
	Project   string `json:"project"`
	Scope     string `json:"scope,omitempty"`
	Setting   string `json:"setting,omitempty"`
	Direction string `json:"direction,omitempty"`
	Unchecked string `json:"unchecked,omitempty"`
}

func (f Finding) String() string {
	if f.Unchecked != "" {
		return fmt.Sprintf("XcodeGen %s: NOT CHECKED — %s", f.Project, f.Unchecked)
	}
	return fmt.Sprintf("XcodeGen %s %s: %s %s", f.Project, f.Scope, f.Setting, f.Direction)
}

// Format is silent for a clean or out-of-scope project.
func Format(fs []Finding) string {
	var out strings.Builder
	for _, f := range fs {
		fmt.Fprintln(&out, f.String())
	}
	return out.String()
}

// Check compares tracked pbxproj files with a fresh generation from the current
// checkout (including local spec edits). A scratch copy preserves source-relative
// includes and prevents XcodeGen from overwriting the project or generated plists.
func Check(root string, targets []baseline.Target) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Xcodeproj == "" || seen[t.Xcodeproj] {
			continue
		}
		seen[t.Xcodeproj] = true
		spec := filepath.Join(filepath.Dir(t.Xcodeproj), "project.yml")
		if _, err := os.Stat(filepath.Join(root, spec)); os.IsNotExist(err) {
			continue
		}
		fs, err := checkProject(root, t.Xcodeproj)
		if err != nil {
			out = append(out, Finding{Project: t.Xcodeproj, Unchecked: err.Error()})
		} else {
			out = append(out, fs...)
		}
	}
	return out
}

func checkProject(root, project string) ([]Finding, error) {
	if !filepath.IsLocal(project) || filepath.Ext(project) != ".xcodeproj" {
		return nil, fmt.Errorf("cannot isolate project path %q", project)
	}
	pbx := filepath.ToSlash(filepath.Join(project, "project.pbxproj"))
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	files, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list project inputs: %w", err)
	}
	// Only tracked projects are in scope; intentionally ignored/generated bundles
	// have no committed artifact to drift from.
	cmd = exec.Command("git", "ls-files", "-z", "--", pbx)
	cmd.Dir = root
	tracked, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("check tracked project: %w", err)
	}
	if len(tracked) == 0 {
		return nil, nil
	}
	generator, err := exec.LookPath("xcodegen")
	if err != nil {
		return nil, fmt.Errorf("xcodegen unavailable: %w", err)
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		return nil, fmt.Errorf("plutil unavailable: %w", err)
	}
	before, err := readSettings(filepath.Join(root, pbx))
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "lacquer-xcodegen-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	if err := copyInputs(root, scratch, strings.Split(string(files), "\x00")); err != nil {
		return nil, err
	}
	// A generator that exits successfully but writes nothing must not compare
	// against the old artifact and report a false clean result.
	generated := filepath.Join(scratch, project)
	if err := os.RemoveAll(generated); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd = exec.CommandContext(ctx, generator, "dump", "--type", "json")
	cmd.Dir = filepath.Dir(generated)
	resolved, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve XcodeGen spec: %w", err)
	}
	if err := safeSpec(resolved, filepath.Dir(generated), scratch); err != nil {
		return nil, err
	}
	cmd = exec.CommandContext(ctx, generator, "generate")
	cmd.Dir = filepath.Dir(generated)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("xcodegen generate: %w: %s", err, strings.TrimSpace(string(output)))
	}
	after, err := readSettings(filepath.Join(generated, "project.pbxproj"))
	if err != nil {
		return nil, fmt.Errorf("generated project: %w", err)
	}
	return compare(project, before, after), nil
}

// Refuse symlinks rather than allow a generated output to follow one back into
// the original checkout. Excluded tool checkouts and build trees are not inputs.
func copyInputs(root, dest string, files []string) error {
	for _, rel := range files {
		if rel == "" {
			continue
		}
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("non-local project input %q", rel)
		}
		skip := false
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			switch part {
			case ".git", ".lacquer-checkout", ".worktrees", "worktrees":
				skip = true
			}
		}
		if skip {
			continue
		}
		src := filepath.Join(root, rel)
		info, err := os.Lstat(src)
		if err != nil {
			return fmt.Errorf("copy input %s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot isolate non-regular input %s", rel)
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		path := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

type scope struct{ owner, config string }
type settings map[scope]map[string]json.RawMessage

type object struct {
	ISA      string                     `json:"isa"`
	Name     string                     `json:"name"`
	List     string                     `json:"buildConfigurationList"`
	Configs  []string                   `json:"buildConfigurations"`
	Settings map[string]json.RawMessage `json:"buildSettings"`
}

// plutil parses OpenStep syntax, including quoted conditional keys and array
// values. Comparing object IDs or grepping lines would misattribute settings
// when XcodeGen regenerates IDs or multiple targets share a configuration name.
func readSettings(path string) (settings, error) {
	cmd := exec.Command("plutil", "-convert", "json", "-o", "-", path)
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parse %s with plutil: %w", path, err)
	}
	return decodeSettings(raw)
}

func decodeSettings(raw []byte) (settings, error) {
	var doc struct {
		Objects map[string]object `json:"objects"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := settings{}
	for _, o := range doc.Objects {
		if o.ISA != "PBXProject" && o.ISA != "PBXNativeTarget" && o.ISA != "PBXAggregateTarget" && o.ISA != "PBXLegacyTarget" {
			continue
		}
		owner := "target " + o.Name
		if o.ISA == "PBXProject" {
			owner = "project"
		} else if o.Name == "" {
			return nil, fmt.Errorf("target has no name")
		}
		list, ok := doc.Objects[o.List]
		if !ok || len(list.Configs) == 0 {
			return nil, fmt.Errorf("%s has no readable configuration list", owner)
		}
		for _, id := range list.Configs {
			c, ok := doc.Objects[id]
			if !ok || c.Name == "" || c.Settings == nil {
				return nil, fmt.Errorf("%s configuration %s is unreadable", owner, id)
			}
			key := scope{owner, c.Name}
			if _, duplicate := out[key]; duplicate {
				return nil, fmt.Errorf("ambiguous configuration %s/%s", owner, c.Name)
			}
			out[key] = c.Settings
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no project or target build configurations found")
	}
	return out, nil
}

func compare(project string, before, after settings) []Finding {
	var out []Finding
	for _, pair := range []struct {
		a, b      settings
		direction string
	}{
		{before, after, "present-in-committed, absent-from-generated"},
		{after, before, "absent-from-committed, present-in-generated"},
	} {
		for scope, values := range pair.a {
			for key := range values {
				if _, ok := pair.b[scope][key]; !ok {
					out = append(out, Finding{Project: project, Scope: scope.owner + "/" + scope.config, Setting: key, Direction: pair.direction})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// XcodeGen may run arbitrary pre/post-generation commands and write Info.plist
// or entitlement files outside the output bundle. A scratch output path alone
// is therefore insufficient to make an audit read-only.
func safeSpec(raw []byte, dir, scratch string) error {
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("read resolved XcodeGen spec: %w", err)
	}
	name, ok := spec["name"].(string)
	if !ok || name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("cannot isolate XcodeGen project name")
	}
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if (key == "preGenCommand" || key == "postGenCommand") && child != nil && fmt.Sprint(child) != "" {
					return fmt.Errorf("cannot isolate XcodeGen %s; generation hooks are not run by audit", key)
				}
				if key == "info" || key == "entitlements" {
					if file, ok := child.(map[string]any); ok {
						if path, ok := file["path"].(string); ok {
							abs := path
							if !filepath.IsAbs(abs) {
								abs = filepath.Join(dir, path)
							}
							rel, err := filepath.Rel(scratch, abs)
							if err != nil || !filepath.IsLocal(rel) {
								return fmt.Errorf("cannot isolate XcodeGen %s output %q", key, path)
							}
						}
					}
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(spec)
}
