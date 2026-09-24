package testtargets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A `-only-testing:` selector can name a test target in either of two places,
// and the audit used to look in only one.
//
// The first is a native target in project.pbxproj. The second is a `.testTarget`
// of a local Swift package the project references — rail's RailCoreTests and
// RailDataTests, which XcodeGen wires into the Rail scheme with
// `testTargets: [{ package: RailCore/RailCoreTests }]`. Those never appear as
// PBXNativeTarget, so reading only the native targets reported both as naming a
// target the project does not have: a false alarm on the one project in the
// fleet that had done the wiring correctly (#307 there).
//
// Only packages the PROJECT references are read. That is the set xcodebuild can
// select tests from; a package that merely sits in the repository, or one that
// is only another package's dependency, is not.
//
// What this does NOT establish: that the scheme's TestAction lists the target.
// `-only-testing:` also matches nothing for a real target the scheme leaves out,
// and that is equally true of native targets, which this check has never
// verified either. CI's "Verify Test Selectors Matched" step is what catches it
// at run time.

var (
	// A whole object block, so the relativePath read is the one belonging to
	// this isa and not to whatever object happens to follow it.
	localPackageBlock = regexp.MustCompile(`\{[^{}]*\bisa = XCLocalSwiftPackageReference;[^{}]*\}`)
	relativePathLine  = regexp.MustCompile(`\brelativePath = (?:"((?:[^"\\]|\\.)*)"|([^;\s]+));`)

	// Every `.testTarget(` call, and the subset whose name is a plain string
	// literal. The trailing [,)] refuses `name: "Rail" + suffix`, which is a
	// computed name and not the literal it begins with.
	testTargetCall  = regexp.MustCompile(`\.testTarget\s*\(`)
	testTargetNamed = regexp.MustCompile(`\.testTarget\s*\(\s*name\s*:\s*"([^"\\]+)"\s*[,)]`)
)

// localPackages returns the relativePath of every XCLocalSwiftPackageReference
// in a project.pbxproj, in file order, without duplicates. A reference with no
// readable relativePath is returned as "" so the caller can report it rather
// than skip it.
func localPackages(pbxproj string) []string {
	var out []string
	seen := map[string]bool{}
	for _, block := range localPackageBlock.FindAllString(pbxproj, -1) {
		rel := ""
		if m := relativePathLine.FindStringSubmatch(block); m != nil {
			rel = m[2]
			if m[1] != "" {
				rel = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(m[1])
			}
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	return out
}

// packageTargets reads the test targets of every local package the project
// references. projectDir is the directory holding the .xcodeproj, which is what
// relativePath is relative to (flare's Flare/Flare.xcodeproj references
// `../FlareCore`).
//
// A package that cannot be fully read contributes an entry with Unread set, in
// addition to whatever names it did yield. That entry is not a target: it is the
// record that there is a place a selector's target might be which was not
// examined, so Compare can say "could not check" instead of "not there".
func packageTargets(projectDir, pbxproj string) []Target {
	var out []Target
	for _, rel := range localPackages(pbxproj) {
		if rel == "" {
			out = append(out, Target{Unread: "project.pbxproj has an XCLocalSwiftPackageReference with no readable relativePath"})
			continue
		}
		dir := rel
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(projectDir, filepath.FromSlash(rel))
		}
		names, problem := testTargetNames(filepath.Join(dir, "Package.swift"), rel)
		for _, n := range names {
			out = append(out, Target{Name: n, Package: rel, dir: dir})
		}
		if problem != "" {
			out = append(out, Target{Package: rel, Unread: problem, dir: dir})
		}
	}
	return out
}

// testTargetNames returns the literal names of a Package.swift's test targets,
// and a non-empty problem when the file could not be read or declares a test
// target whose name is not a literal — both of which mean some test target of
// this package is unknown to the audit.
func testTargetNames(manifest, rel string) ([]string, string) {
	shown := filepath.ToSlash(filepath.Join(rel, "Package.swift"))
	b, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		return nil, shown + " does not exist"
	}
	if err != nil {
		return nil, fmt.Sprintf("%s could not be read: %v", shown, err)
	}
	src := stripSwiftComments(string(b))

	var names []string
	for _, m := range testTargetNamed.FindAllStringSubmatch(src, -1) {
		names = append(names, m[1])
	}
	if calls := len(testTargetCall.FindAllStringIndex(src, -1)); calls != len(names) {
		return names, fmt.Sprintf("%s declares %d test target(s), %d with a name written as a plain "+
			"string; the rest are computed and cannot be read without running Swift",
			shown, calls, len(names))
	}
	return names, ""
}

// stripSwiftComments blanks out `//` and `/* */` comments — which nest in Swift
// — while leaving string literals, including `"""` ones, intact. Blanked text
// becomes spaces and newlines are kept, so nothing on either side of a comment
// is joined into something it was not.
//
// A commented-out `.testTarget(name: "X")` is not a test target, and counting it
// as one is the defect lacquer#378 fixed for a `#` comment in a workflow. The
// string handling is what stops `.package(url: "https://…")` from being read as
// the start of a comment.
func stripSwiftComments(src string) string {
	out := []byte(src)
	blank := func(i int) {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], `"""`):
			end := strings.Index(src[i+3:], `"""`)
			if end < 0 {
				return string(out)
			}
			i += 3 + end + 3
		case src[i] == '"':
			i++
			for i < len(src) && src[i] != '"' && src[i] != '\n' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				blank(i)
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			depth := 0
			for i < len(src) {
				if strings.HasPrefix(src[i:], "/*") {
					depth++
					blank(i)
					blank(i + 1)
					i += 2
					continue
				}
				if strings.HasPrefix(src[i:], "*/") {
					depth--
					blank(i)
					blank(i + 1)
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				blank(i)
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}
