package baseline

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is one XCBuildConfiguration: its object id, its name ("Debug" /
// "Release"), the build settings it declares itself, and whether it belongs to
// the project rather than to a target.
type Config struct {
	ID           string
	Name         string
	Settings     map[string]string
	ProjectLevel bool
}

// Declared is what a project's own files say about the build settings the
// baseline cares about.
type Declared struct {
	Configs []Config
}

// SwiftConfigs returns the configurations that compile Swift, identified by
// declaring SWIFT_VERSION.
//
// Xcode writes SWIFT_VERSION into exactly the target configurations that build
// Swift, which makes its presence the best available marker without resolving
// the full target graph. Note what this deliberately is NOT: "declares any
// SWIFT_* setting". Project-level configurations routinely declare
// SWIFT_OPTIMIZATION_LEVEL and SWIFT_ACTIVE_COMPILATION_CONDITIONS while
// carrying no language mode, so the looser test inflates the denominator and
// reports a fully compliant project as short of coverage — a false positive on
// the very thing this package exists to certify.
//
// The blind spot: a configuration that compiles Swift while declaring no
// SWIFT_VERSION of its own, inheriting it from the project level or an xcconfig,
// is not counted. That is why CI checks *effective* settings via
// `xcodebuild -showBuildSettings` — two independent checks at different fidelity,
// the same defense-in-depth shape ci.yml already uses for its SPM cache.
func (d Declared) SwiftConfigs() []Config {
	var out []Config
	for _, c := range d.Configs {
		if c.ProjectLevel {
			continue
		}
		if _, ok := c.Settings["SWIFT_VERSION"]; ok {
			out = append(out, c)
		}
	}
	return out
}

// Effective resolves key for one configuration: the configuration's own value if
// it declares one, otherwise the project-level configuration of the same name.
// That models Xcode's inheritance closely enough to avoid punishing a project
// that sets a value once at the project level and lets its targets inherit it —
// a legitimate and common layout.
func (d Declared) Effective(c Config, key string) (string, bool) {
	if v, ok := c.Settings[key]; ok {
		return v, true
	}
	for _, p := range d.Configs {
		if p.ProjectLevel && p.Name == c.Name {
			if v, ok := p.Settings[key]; ok {
				return v, true
			}
		}
	}
	return "", false
}

// Coverage reports how many Swift-compiling configurations resolve key to want,
// out of how many there are. A total of 0 means the project compiles no Swift
// that this reader can see, so there is nothing to enforce.
func (d Declared) Coverage(key, want string) (have, total int) {
	swift := d.SwiftConfigs()
	for _, c := range swift {
		if v, ok := d.Effective(c, key); ok && v == want {
			have++
		}
	}
	return have, len(swift)
}

// object kinds this scanner tracks.
const (
	kindNone = iota
	kindProject
	kindConfigList
	kindBuildConfig
)

// ReadXcodeproj parses every XCBuildConfiguration in a project, plus enough of
// the object graph to tell project-level configurations from target ones. path
// may be the .xcodeproj bundle or the project.pbxproj itself.
//
// This is a line scanner, not a plist parser: it needs configuration identity,
// names, declared settings, and which configuration list the PBXProject points
// at. The pbxproj grammar for that subset is regular enough to scan, and a full
// plist parser would be a large dependency for no additional answer.
//
// pbxproj writes an object as
//
//	<24-hex-id> /* comment */ = {
//		isa = XCBuildConfiguration;
//		...
//	};
//
// so the id sits on the line *before* the `isa` line the scanner keys off. One
// line of lookbehind covers it.
func ReadXcodeproj(path string) (Declared, error) {
	if filepath.Ext(path) == ".xcodeproj" {
		path = filepath.Join(path, "project.pbxproj")
	}
	f, err := os.Open(path)
	if err != nil {
		return Declared{}, fmt.Errorf("read xcodeproj: %w", err)
	}
	defer f.Close()

	var (
		configs     []Config
		cur         *Config                 // build config being filled
		lists       = map[string][]string{} // config-list id -> member config ids
		listID      string                  // config list being filled
		projectList string                  // the PBXProject's buildConfigurationList id
		kind        = kindNone
		prev        string // previous line, for the id lookbehind
	)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		if isa, ok := strings.CutPrefix(line, "isa = "); ok {
			id := openingID(prev)
			cur, kind = nil, kindNone
			switch strings.TrimSuffix(isa, ";") {
			case "PBXProject":
				kind = kindProject
			case "XCConfigurationList":
				kind, listID = kindConfigList, id
			case "XCBuildConfiguration":
				kind = kindBuildConfig
				configs = append(configs, Config{ID: id, Settings: map[string]string{}})
				cur = &configs[len(configs)-1]
			}
			prev = line
			continue
		}
		prev = line

		switch kind {
		case kindProject:
			if v, ok := strings.CutPrefix(line, "buildConfigurationList = "); ok {
				projectList = firstToken(strings.TrimSuffix(v, ";"))
			}
		case kindConfigList:
			// Members appear one per line as `ID /* Debug */,` inside
			// `buildConfigurations = ( ... );`.
			if id := memberID(line); id != "" {
				lists[listID] = append(lists[listID], id)
			}
		case kindBuildConfig:
			if cur == nil {
				continue
			}
			if v, ok := strings.CutPrefix(line, "name = "); ok {
				cur.Name = strings.Trim(strings.TrimSuffix(v, ";"), `"`)
				cur, kind = nil, kindNone // `name` is the block's last field
				continue
			}
			if key, val, ok := settingLine(line); ok {
				cur.Settings[key] = val
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Declared{}, fmt.Errorf("scan %s: %w", path, err)
	}

	projectMembers := map[string]bool{}
	for _, id := range lists[projectList] {
		projectMembers[id] = true
	}
	for i := range configs {
		configs[i].ProjectLevel = projectMembers[configs[i].ID]
	}
	return Declared{Configs: configs}, nil
}

// openingID pulls the object id out of a block-opening line, which pbxproj
// writes as `<id> /* comment */ = {`.
func openingID(line string) string {
	before, _, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return ""
	}
	return firstToken(before)
}

// firstToken returns the first whitespace-separated token, which for a pbxproj
// object reference is the object id.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// memberID extracts an id from a `buildConfigurations = ( ... )` member line,
// which pbxproj writes as `ID /* Debug */,`.
func memberID(line string) string {
	if !strings.HasSuffix(line, ",") {
		return ""
	}
	tok := firstToken(strings.TrimSuffix(line, ","))
	if tok == "" || strings.ContainsAny(tok, "(){}=") {
		return ""
	}
	return tok
}

// settingLine splits a `KEY = value;` build-settings line.
//
// A conditional variant is quoted by Xcode as `"KEY[sdk=iphoneos*]" = value;`.
// Those are rejected rather than folded into the plain key: an iphoneos-only
// override is not the same promise as an unconditional setting, and treating it
// as one would let partial coverage read as full.
func settingLine(line string) (key, val string, ok bool) {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")
	key, val, ok = strings.Cut(line, " = ")
	if !ok {
		return "", "", false
	}
	key = strings.Trim(strings.TrimSpace(key), `"`)
	if strings.ContainsAny(key, "[]") {
		return "", "", false
	}
	val = strings.Trim(strings.TrimSpace(val), `"`)
	if key == "" || val == "" {
		return "", "", false
	}
	return key, val, true
}
