package testtargets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"gopkg.in/yaml.v3"
)

// Half the fleet's iOS projects commit no project.pbxproj: XcodeGen generates it
// from project.yml, and the .xcodeproj is gitignored. Every watch app in the
// fleet is in one of those. The audit read only the pbxproj, so on a clean
// checkout (CI's drift job, which has no xcodegen) it read nothing for them, and
// a watch test bundle that nothing ran could not be seen at all.
//
// So the audit reads the spec when the pbxproj is not tracked, and both readers
// return the same Project. TestSpecParseMatchesPbxprojParseOnTheSameProject
// keeps them from drifting apart.

// WatchOS is the Platform of a watchOS target. Every other target, iOS included,
// has Platform "".
const WatchOS = "watchOS"

// App is an application target and the platform it builds for.
type App struct {
	Name     string
	Platform string
}

// Project is what the audit needs from an Xcode project: its test targets, its
// application targets, and which test targets each scheme's TestAction lists.
type Project struct {
	// Targets is every test target, exactly as Parse returns them, with Platform
	// set on a watchOS bundle.
	Targets []Target
	// Apps is every application target.
	Apps []App
	// Schemes maps a scheme name to the test targets its TestAction lists. Used
	// only to print the watch_tests a finding asks for; a scheme the reader
	// could not see is simply absent.
	Schemes map[string][]string
}

// WatchApps is every watchOS application target.
func (p Project) WatchApps() []App {
	var out []App
	for _, a := range p.Apps {
		if a.Platform == WatchOS {
			out = append(out, a)
		}
	}
	return out
}

// IsWatchBundle reports whether t is a watchOS UNIT test bundle: the one shape
// [product.watch_tests] runs. A watch UI-testing bundle is not one of these; it
// stays in the ordinary uncovered report.
func IsWatchBundle(t Target) bool {
	return t.Platform == WatchOS && !t.UI && t.Package == "" && t.Unread == ""
}

// SchemesTesting lists, sorted, every scheme whose TestAction names target.
func (p Project) SchemesTesting(target string) []string {
	var out []string
	for s, ts := range p.Schemes {
		for _, t := range ts {
			if t == target {
				out = append(out, s)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---- XcodeGen spec ----------------------------------------------------------

// specFile is the subset of an XcodeGen spec that names targets, their types and
// platforms, local packages, and scheme test lists.
type specFile struct {
	Include         yaml.Node              `yaml:"include"`
	Packages        map[string]specPackage `yaml:"packages"`
	Targets         map[string]specTarget  `yaml:"targets"`
	TargetTemplates map[string]specTarget  `yaml:"targetTemplates"`
	Schemes         map[string]specScheme  `yaml:"schemes"`
}

type specPackage struct {
	Path string `yaml:"path"`
}

type specTarget struct {
	Type                  string    `yaml:"type"`
	Platform              yaml.Node `yaml:"platform"`
	SupportedDestinations []string  `yaml:"supportedDestinations"`
	Templates             []string  `yaml:"templates"`
	Scheme                *struct {
		TestTargets []yaml.Node `yaml:"testTargets"`
	} `yaml:"scheme"`
}

type specScheme struct {
	Test struct {
		Targets []yaml.Node `yaml:"targets"`
	} `yaml:"test"`
}

// ParseSpec reads an XcodeGen project.yml, and the files it includes. The bool
// reports whether the spec was READ, with the meaning Parse gives it: an absent
// spec is not evidence of anything.
//
// It follows `include:` (relative to the including file) and fills a target's
// missing type and platform from its `templates:`. A target whose type or
// platform it still cannot establish is skipped rather than guessed; the
// generated project would be the only authority on it.
func ParseSpec(specPath string) (Project, bool, error) {
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return Project{}, false, nil
	}
	var spec specFile
	if err := loadSpec(specPath, &spec, map[string]bool{}); err != nil {
		return Project{}, false, err
	}

	p := Project{Schemes: map[string][]string{}}
	names := make([]string, 0, len(spec.Targets))
	for n := range spec.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t := spec.Targets[name]
		for _, tmpl := range t.Templates {
			base := spec.TargetTemplates[tmpl]
			if t.Type == "" {
				t.Type = base.Type
			}
			if t.Platform.Kind == 0 {
				t.Platform = base.Platform
			}
			if len(t.SupportedDestinations) == 0 {
				t.SupportedDestinations = base.SupportedDestinations
			}
		}
		for _, inst := range specInstances(name, t) {
			switch t.Type {
			case "bundle.unit-test":
				p.Targets = append(p.Targets, Target{Name: inst.name, Platform: inst.platform})
			case "bundle.ui-testing":
				p.Targets = append(p.Targets, Target{Name: inst.name, UI: true, Platform: inst.platform})
			case "application", "application.watchapp2":
				p.Apps = append(p.Apps, App{Name: inst.name, Platform: inst.platform})
			}
			if t.Scheme != nil {
				p.Schemes[inst.name] = append(p.Schemes[inst.name], specTestNames(t.Scheme.TestTargets)...)
			}
		}
	}
	for name, s := range spec.Schemes {
		p.Schemes[name] = append(p.Schemes[name], specTestNames(s.Test.Targets)...)
	}

	// Local packages, the way the pbxproj path reads XCLocalSwiftPackageReference:
	// XcodeGen generates the project beside the spec, so a package path relative
	// to the spec is relative to the project directory too.
	var rels []string
	pkgs := make([]string, 0, len(spec.Packages))
	for n := range spec.Packages {
		pkgs = append(pkgs, n)
	}
	sort.Strings(pkgs)
	for _, n := range pkgs {
		if path := spec.Packages[n].Path; path != "" {
			rels = append(rels, filepath.ToSlash(filepath.Clean(path)))
		}
	}
	p.Targets = append(p.Targets, packageTargetsAt(filepath.Dir(specPath), rels, "")...)
	sortTargets(p.Targets)
	return p, true, nil
}

// loadSpec decodes path into spec, includes first so the including file wins,
// the way XcodeGen merges them.
func loadSpec(path string, spec *specFile, seen map[string]bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if seen[abs] {
		return nil
	}
	seen[abs] = true
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var own specFile
	if err := yaml.Unmarshal(b, &own); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for _, inc := range specIncludes(own.Include) {
		if !filepath.IsAbs(inc) {
			inc = filepath.Join(filepath.Dir(path), inc)
		}
		if err := loadSpec(inc, spec, seen); err != nil {
			return err
		}
	}
	mergeSpec(spec, own)
	return nil
}

// specIncludes reads `include:` as a string, a list of strings, or a list of
// `{ path: … }` tables.
func specIncludes(n yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.ScalarNode:
		out = append(out, n.Value)
	case yaml.SequenceNode:
		for _, c := range n.Content {
			switch c.Kind {
			case yaml.ScalarNode:
				out = append(out, c.Value)
			case yaml.MappingNode:
				var m struct {
					Path string `yaml:"path"`
				}
				if c.Decode(&m) == nil && m.Path != "" {
					out = append(out, m.Path)
				}
			}
		}
	}
	return out
}

func mergeSpec(dst *specFile, src specFile) {
	if dst.Targets == nil {
		dst.Targets = map[string]specTarget{}
	}
	if dst.TargetTemplates == nil {
		dst.TargetTemplates = map[string]specTarget{}
	}
	if dst.Schemes == nil {
		dst.Schemes = map[string]specScheme{}
	}
	if dst.Packages == nil {
		dst.Packages = map[string]specPackage{}
	}
	for k, v := range src.Targets {
		dst.Targets[k] = v
	}
	for k, v := range src.TargetTemplates {
		dst.TargetTemplates[k] = v
	}
	for k, v := range src.Schemes {
		dst.Schemes[k] = v
	}
	for k, v := range src.Packages {
		dst.Packages[k] = v
	}
}

type specInstance struct{ name, platform string }

// specInstances expands a target into the targets XcodeGen generates from it. A
// `platform:` list makes one per platform, named `<name>_<platform>` unless the
// name carries `${platform}` itself.
func specInstances(name string, t specTarget) []specInstance {
	var platforms []string
	switch t.Platform.Kind {
	case yaml.ScalarNode:
		platforms = []string{t.Platform.Value}
	case yaml.SequenceNode:
		for _, c := range t.Platform.Content {
			platforms = append(platforms, c.Value)
		}
	}
	if len(platforms) == 0 {
		// supportedDestinations alone: a watch target only when watchOS is the
		// one destination, because a multi-destination target is not a
		// watchOS bundle.
		if len(t.SupportedDestinations) == 1 && t.SupportedDestinations[0] == WatchOS {
			return []specInstance{{name, WatchOS}}
		}
		return []specInstance{{name, ""}}
	}
	if len(platforms) == 1 {
		return []specInstance{{name, platformOf(platforms[0])}}
	}
	out := make([]specInstance, 0, len(platforms))
	for _, pl := range platforms {
		n := name + "_" + pl
		if strings.Contains(name, "${platform}") {
			n = strings.ReplaceAll(name, "${platform}", pl)
		}
		out = append(out, specInstance{n, platformOf(pl)})
	}
	return out
}

// platformOf maps an XcodeGen platform name to Target.Platform.
func platformOf(p string) string {
	if p == WatchOS {
		return WatchOS
	}
	return ""
}

// specTestNames reads a scheme's test target list: plain names, `{ name: … }`
// tables, and `{ package: Pkg/Target }` references, which name a package's test
// target by what follows the slash.
func specTestNames(ns []yaml.Node) []string {
	var out []string
	for _, n := range ns {
		switch n.Kind {
		case yaml.ScalarNode:
			out = append(out, n.Value)
		case yaml.MappingNode:
			var m struct {
				Name    string `yaml:"name"`
				Package string `yaml:"package"`
			}
			if n.Decode(&m) != nil {
				continue
			}
			switch {
			case m.Name != "":
				out = append(out, m.Name)
			case m.Package != "":
				_, t, _ := strings.Cut(m.Package, "/")
				out = append(out, t)
			}
		}
	}
	return out
}

// ---- project.pbxproj --------------------------------------------------------

// ParseProject is Parse plus what the watch findings need from a pbxproj:
// each test target's platform, the application targets, and the shared schemes'
// test lists.
//
// A target's platform is its SDKROOT, from its own build configurations or, when
// none sets one, the project's. A watchOS app is an application whose SDKROOT is
// watchos, or a legacy watchapp2; the iOS-side watchapp2-container stub is not
// one.
func ParseProject(pbxprojPath string) (Project, bool, error) {
	targets, read, err := Parse(pbxprojPath)
	if err != nil || !read {
		return Project{}, read, err
	}
	b, err := os.ReadFile(pbxprojPath)
	if err != nil {
		return Project{}, false, err
	}
	declared, err := baseline.ReadXcodeproj(pbxprojPath)
	if err != nil {
		return Project{}, false, err
	}
	sdk := map[string]string{}
	projectSDK := ""
	for _, c := range declared.Configs {
		v := c.Settings["SDKROOT"]
		if v == "" {
			continue
		}
		switch {
		case c.ProjectLevel:
			projectSDK = v
		case c.Target != "":
			if sdk[c.Target] == "" || v == "watchos" {
				sdk[c.Target] = v
			}
		}
	}
	platform := func(name string) string {
		v := sdk[name]
		if v == "" {
			v = projectSDK
		}
		if v == "watchos" {
			return WatchOS
		}
		return ""
	}

	p := Project{Schemes: readSchemes(filepath.Dir(pbxprojPath))}
	for _, t := range targets {
		if t.Package == "" && t.Unread == "" {
			t.Platform = platform(t.Name)
		}
		p.Targets = append(p.Targets, t)
	}
	for _, a := range nativeApps(string(b)) {
		switch a.productType {
		case "com.apple.product-type.application.watchapp2":
			p.Apps = append(p.Apps, App{Name: a.name, Platform: WatchOS})
		case "com.apple.product-type.application":
			p.Apps = append(p.Apps, App{Name: a.name, Platform: platform(a.name)})
		}
	}
	return p, true, nil
}

type nativeApp struct{ name, productType string }

// nativeApps reads every native target's name and product type, the way Parse
// reads the test targets.
func nativeApps(text string) []nativeApp {
	var out []nativeApp
	var inTarget bool
	var name string
	for _, line := range strings.Split(text, "\n") {
		if nativeTarget.MatchString(line) {
			inTarget, name = true, ""
			continue
		}
		if !inTarget {
			continue
		}
		if m := nameLine.FindStringSubmatch(line); m != nil {
			name = m[1]
			if name == "" {
				name = m[2]
			}
			continue
		}
		if m := productLine.FindStringSubmatch(line); m != nil {
			out = append(out, nativeApp{name, m[1]})
			inTarget = false
		}
	}
	return out
}

// readSchemes reads the shared schemes of the .xcodeproj at dir: for each, the
// BlueprintName of every TestableReference. A project with no shared schemes
// has none to report, which only leaves the finding without a scheme to suggest.
func readSchemes(xcodeproj string) map[string][]string {
	out := map[string][]string{}
	files, _ := filepath.Glob(filepath.Join(xcodeproj, "xcshareddata", "xcschemes", "*.xcscheme"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".xcscheme")
		text := string(b)
		for {
			i := strings.Index(text, "<TestableReference")
			if i < 0 {
				break
			}
			text = text[i:]
			end := strings.Index(text, "</TestableReference>")
			if end < 0 {
				break
			}
			block := text[:end]
			if m := blueprintName.FindStringSubmatch(block); m != nil {
				out[name] = append(out[name], m[1])
			}
			text = text[end:]
		}
	}
	return out
}

// sortTargets orders targets the way Parse does.
func sortTargets(ts []Target) {
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].Name != ts[j].Name {
			return ts[i].Name < ts[j].Name
		}
		return ts[i].Package < ts[j].Package
	})
}
