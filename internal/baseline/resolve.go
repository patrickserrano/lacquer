package baseline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolved retains uncertainty separately from absence: neither is evidence that
// a setting is compliant. Source names the layer as well as the file.
type resolved struct {
	value, source, unknown string
	present                bool
}

var baselineSettings = []string{"SWIFT_VERSION", WarningsKey, "SWIFT_STRICT_CONCURRENCY"}

func (d Declared) resolve(c Config, key string) resolved {
	if values, ok := d.resolved[c.ID]; ok {
		return values[key]
	}
	// In-memory declarations used by callers have no file-backed layers.
	var r resolved
	for _, p := range d.Configs {
		if p.ProjectLevel && p.Name == c.Name {
			r = inlineValue(r, p, key, "project pbxproj", d.Source)
		}
	}
	return inlineValue(r, c, key, "target pbxproj", d.Source)
}

func inlineValue(lower resolved, c Config, key, layer, file string) resolved {
	if c.conditional[key] {
		return resolved{unknown: "conditional " + key + " in " + layer + " " + file}
	}
	v, ok := c.Settings[key]
	if !ok {
		return lower
	}
	if v == "$(inherited)" || v == "${inherited}" {
		return lower
	}
	r := resolved{value: v, present: true, source: layer + " " + file}
	if strings.Contains(v, "$") {
		r.unknown = "unresolved build-setting expansion in " + r.source
	}
	return r
}

// resolveFiles evaluates low to high: project xcconfig, project pbxproj, target
// xcconfig, target pbxproj. It never writes a project setting.
func (d *Declared) resolveFiles(paths map[string]resolved) {
	d.resolved = map[string]map[string]resolved{}
	for _, c := range d.Configs {
		if c.ProjectLevel {
			continue
		}
		values := map[string]resolved{}
		for _, key := range baselineSettings {
			var r resolved
			for _, p := range d.Configs {
				if p.ProjectLevel && p.Name == c.Name {
					r = configValue(r, p, key, "project xcconfig", paths)
					r = inlineValue(r, p, key, "project pbxproj", d.Source)
				}
			}
			r = configValue(r, c, key, "target xcconfig", paths)
			values[key] = inlineValue(r, c, key, "target pbxproj", d.Source)
		}
		d.resolved[c.ID] = values
	}
}

func configValue(lower resolved, c Config, key, layer string, paths map[string]resolved) resolved {
	if c.baseReference == "" {
		return lower
	}
	p, ok := paths[c.baseReference]
	if !ok || p.unknown != "" {
		return resolved{unknown: fmt.Sprintf("%s reference %s: %s", layer, c.baseReference, p.unknown)}
	}
	return readConfig(p.value, key, c.Name, layer, lower, map[string]bool{})
}

// readConfig supports literal scalar assignments, inherited scalars, relative
// includes and optional includes. Conditional assignments,
// macros, malformed syntax and include failures are UNKNOWN, not guessed values.
func readConfig(path, key, name, layer string, lower resolved, stack map[string]bool) resolved {
	path = filepath.Clean(path)
	source := layer + " " + path
	uncertain := func(why string) resolved { return resolved{unknown: why + " in " + source, source: source} }
	if stack[path] || len(stack) >= 32 {
		return uncertain("include cycle or depth limit")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return uncertain(err.Error())
	}
	stack[path] = true
	defer delete(stack, path)
	r := lower
	var unsupported string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "#include"); ok {
			optional := strings.HasPrefix(rest, "?")
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "?"))
			if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' || strings.Contains(rest, "$") {
				unsupported = "unsupported include: " + line
				continue
			}
			inc := rest[1 : len(rest)-1]
			if !filepath.IsAbs(inc) {
				inc = filepath.Join(filepath.Dir(path), inc)
			}
			if _, err := os.Stat(inc); optional && os.IsNotExist(err) {
				continue
			}
			r = readConfig(inc, key, name, layer, r, stack)
			continue
		}
		m := xcconfigSetting.FindStringSubmatch(line)
		if m == nil {
			unsupported = "unsupported xcconfig syntax: " + line
			continue
		}
		if m[1] != key {
			continue
		}
		condition := m[2]
		if condition != "" {
			match := configCondition.FindStringSubmatch(condition)
			if match == nil || match[0] != condition {
				unsupported = "conditional setting: " + line
				continue
			}
			applies, err := filepath.Match(match[1], name)
			if err != nil {
				unsupported = "unsupported condition: " + line
				continue
			}
			if !applies {
				continue
			}
			// Conditional specificity relative to unconditional assignments requires
			// Xcode evaluation; do not infer a winner from file order.
			unsupported = "conditional setting: " + line
			continue
		}
		value := strings.Trim(strings.TrimSpace(m[3]), `"`)
		if value == "$(inherited)" || value == "${inherited}" {
			continue
		}
		r = resolved{value: value, source: source, present: true}
		if strings.ContainsAny(value, "$\\") || strings.Contains(value, "/*") {
			r = uncertain("unsupported value: " + value)
		}
	}
	if unsupported != "" {
		return uncertain(unsupported)
	}
	return r
}

// referencePaths follows PBXGroup ancestry rather than searching by basename:
// two Config/Base.xcconfig files must never pick an arbitrary winner.
func referencePaths(raw, projectDir string) map[string]resolved {
	type ref struct {
		kind, path, tree string
		children         []string
	}
	refs := map[string]*ref{}
	var cur *ref
	var root, projectOffset string
	children := false
	for _, original := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(original)
		if strings.Contains(line, " = {") && !strings.HasPrefix(line, "buildSettings") {
			id := openingID(line)
			cur = &ref{}
			refs[id] = cur
			children = false
			if strings.Contains(line, "isa = PBXFileReference;") {
				cur.kind, cur.path, cur.tree = "PBXFileReference", inlineSetting(line, "path"), inlineSetting(line, "sourceTree")
				cur = nil
				continue
			}
		}
		if cur == nil {
			continue
		}
		if v, ok := strings.CutPrefix(line, "isa = "); ok {
			cur.kind = strings.TrimSuffix(v, ";")
		}
		if v, ok := strings.CutPrefix(line, "mainGroup = "); ok {
			root = firstToken(strings.TrimSuffix(v, ";"))
		}
		if v, ok := strings.CutPrefix(line, "projectDirPath = "); ok {
			projectOffset = strings.Trim(strings.TrimSuffix(v, ";"), `"`)
		}
		if v, ok := strings.CutPrefix(line, "path = "); ok {
			cur.path = strings.Trim(strings.TrimSuffix(v, ";"), `"`)
		}
		if v, ok := strings.CutPrefix(line, "sourceTree = "); ok {
			cur.tree = strings.Trim(strings.TrimSuffix(v, ";"), `"`)
		}
		if line == "children = (" {
			children = true
			continue
		}
		if children {
			if line == ");" {
				children = false
			} else if id := memberID(line); id != "" {
				cur.children = append(cur.children, id)
			}
		}
	}
	parents := map[string][]string{}
	for id, r := range refs {
		if r.kind == "PBXGroup" {
			for _, child := range r.children {
				parents[child] = append(parents[child], id)
			}
		}
	}
	var resolve func(string, map[string]bool) resolved
	resolve = func(id string, seen map[string]bool) resolved {
		r := refs[id]
		bad := func(why string) resolved { return resolved{unknown: why + " (" + id + ")"} }
		if r == nil {
			return bad("missing file/group reference")
		}
		if seen[id] || len(seen) >= 128 {
			return bad("cyclic group ancestry")
		}
		seen[id] = true
		defer delete(seen, id)
		if strings.Contains(r.path, "$") || strings.Contains(projectOffset, "$") {
			return bad("unresolved path expansion")
		}
		var base string
		switch r.tree {
		case "SOURCE_ROOT":
			base = filepath.Join(projectDir, projectOffset)
		case "<absolute>":
			if !filepath.IsAbs(r.path) {
				return bad("non-absolute path")
			}
			return resolved{value: r.path}
		case "<group>", "":
			if id == root {
				base = filepath.Join(projectDir, projectOffset)
			} else {
				if len(parents[id]) != 1 {
					return bad("missing or ambiguous group ancestry")
				}
				parent := resolve(parents[id][0], seen)
				if parent.unknown != "" {
					return parent
				}
				base = parent.value
			}
		default:
			return bad("unsupported sourceTree " + r.tree)
		}
		if filepath.IsAbs(r.path) {
			return resolved{value: r.path}
		}
		return resolved{value: filepath.Join(base, r.path)}
	}
	out := map[string]resolved{}
	for id, r := range refs {
		if r.kind == "PBXFileReference" {
			out[id] = resolve(id, map[string]bool{})
		}
	}
	return out
}
